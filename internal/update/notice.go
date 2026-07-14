package update

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	"golang.org/x/term"

	"github.com/tallyfy/cli/internal/config"
	"github.com/tallyfy/cli/internal/version"
)

// stderrIsTerminal probes whether stderr is a TTY; package variable so tests
// can force either answer.
var stderrIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// timeNow is the clock; package variable for tests.
var timeNow = time.Now

// recheckAfter is how stale the last background check may be before a new
// one is spawned.
const recheckAfter = 24 * time.Hour

// noticeCheckTimeout bounds the fire-and-forget background check.
const noticeCheckTimeout = 1500 * time.Millisecond

// ciEnvVars suppress the passive notice when present in the environment.
var ciEnvVars = []string{"CI", "GITHUB_ACTIONS", "TF_BUILD", "JENKINS_URL"}

// MaybeNotice prints the passive once-per-24h update notice to w (stderr)
// and opportunistically refreshes the cached "latest" version in the
// background. It never errors, never blocks meaningfully, and prints nothing
// unless ALL gates hold:
//
//   - not --quiet, and output mode is "table" (never pollute machine output)
//   - update.autoUpdate is enabled in settings
//   - TALLYFY_NO_UPDATE_CHECK is unset (or "0")
//   - not in CI (CI, GITHUB_ACTIONS, TF_BUILD, JENKINS_URL all absent)
//   - stderr is a TTY
//   - this is a release build (version != "dev")
//
// When the state file's latest_seen is newer than the running version, one
// line is printed. Independently, when the last background check is older
// than 24h, a goroutine with a 1500ms budget re-checks GitHub and rewrites
// the state file; the process may exit first, which is fine - the next run
// benefits.
func MaybeNotice(cfg *config.Resolved, w io.Writer, outputMode string, quiet bool) {
	if quiet || outputMode != "table" || cfg == nil || !cfg.UpdateAutoUpdate {
		return
	}
	if v := os.Getenv(EnvNoUpdateCheck); v != "" && v != "0" {
		return
	}
	for _, name := range ciEnvVars {
		if _, ok := os.LookupEnv(name); ok {
			return
		}
	}
	if !stderrIsTerminal() {
		return
	}
	if version.Version == "dev" {
		return
	}

	current := "v" + strings.TrimPrefix(version.Version, "v")
	st := readState()
	if st.LatestSeen != "" {
		latest := "v" + strings.TrimPrefix(st.LatestSeen, "v")
		if semver.IsValid(latest) && semver.IsValid(current) && semver.Compare(latest, current) > 0 {
			_, _ = fmt.Fprintf(w, "A new version of tallyfy is available: %s -> %s (run: tallyfy update)\n", current, latest)
		}
	}

	if timeNow().Unix()-st.LastCheckUnix > int64(recheckAfter/time.Second) {
		channel := Channel(cfg.UpdateChannel)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), noticeCheckTimeout)
			defer cancel()
			res, err := Check(ctx, version.Version, channel)
			if err != nil {
				return
			}
			_ = writeState(State{LastCheckUnix: timeNow().Unix(), LatestSeen: res.Latest})
		}()
	}
}
