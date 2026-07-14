package update

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/tallyfy/cli/internal/config"
	"github.com/tallyfy/cli/internal/version"
)

// clearEnv unsets variables for the test with automatic restore.
func clearEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "") // registers restoration of the original value
		_ = os.Unsetenv(k)
	}
}

// noticeSetup makes every gate pass: release version, TTY stderr, no CI env,
// fresh temp state file seeded with the given state.
func noticeSetup(t *testing.T, st State) {
	t.Helper()
	fakeStatePath(t)
	if err := writeState(st); err != nil {
		t.Fatal(err)
	}

	oldTTY := stderrIsTerminal
	stderrIsTerminal = func() bool { return true }
	t.Cleanup(func() { stderrIsTerminal = oldTTY })

	oldVersion := version.Version
	version.Version = "0.1.0"
	t.Cleanup(func() { version.Version = oldVersion })

	clearEnv(t, EnvNoUpdateCheck, "CI", "GITHUB_ACTIONS", "TF_BUILD", "JENKINS_URL")
}

func noticeCfg() *config.Resolved {
	return &config.Resolved{UpdateAutoUpdate: true, UpdateChannel: "stable"}
}

const wantNotice = "A new version of tallyfy is available: v0.1.0 -> v0.2.0 (run: tallyfy update)\n"

func TestMaybeNoticePrints(t *testing.T) {
	noticeSetup(t, State{LastCheckUnix: time.Now().Unix(), LatestSeen: "v0.2.0"})
	var buf bytes.Buffer
	MaybeNotice(noticeCfg(), &buf, "table", false)
	if buf.String() != wantNotice {
		t.Errorf("got %q, want %q", buf.String(), wantNotice)
	}
}

func TestMaybeNoticeEnvZeroStillPrints(t *testing.T) {
	noticeSetup(t, State{LastCheckUnix: time.Now().Unix(), LatestSeen: "v0.2.0"})
	t.Setenv(EnvNoUpdateCheck, "0")
	var buf bytes.Buffer
	MaybeNotice(noticeCfg(), &buf, "table", false)
	if buf.String() != wantNotice {
		t.Errorf("TALLYFY_NO_UPDATE_CHECK=0 must not suppress: got %q", buf.String())
	}
}

func TestMaybeNoticeGates(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, cfg *config.Resolved) (outputMode string, quiet bool)
	}{
		{"quiet", func(t *testing.T, cfg *config.Resolved) (string, bool) { return "table", true }},
		{"json mode", func(t *testing.T, cfg *config.Resolved) (string, bool) { return "json", false }},
		{"csv mode", func(t *testing.T, cfg *config.Resolved) (string, bool) { return "csv", false }},
		{"ndjson mode", func(t *testing.T, cfg *config.Resolved) (string, bool) { return "ndjson", false }},
		{"autoUpdate off", func(t *testing.T, cfg *config.Resolved) (string, bool) {
			cfg.UpdateAutoUpdate = false
			return "table", false
		}},
		{"env opt-out 1", func(t *testing.T, cfg *config.Resolved) (string, bool) {
			t.Setenv(EnvNoUpdateCheck, "1")
			return "table", false
		}},
		{"env opt-out true", func(t *testing.T, cfg *config.Resolved) (string, bool) {
			t.Setenv(EnvNoUpdateCheck, "true")
			return "table", false
		}},
		{"CI", func(t *testing.T, cfg *config.Resolved) (string, bool) {
			t.Setenv("CI", "true")
			return "table", false
		}},
		{"GITHUB_ACTIONS", func(t *testing.T, cfg *config.Resolved) (string, bool) {
			t.Setenv("GITHUB_ACTIONS", "true")
			return "table", false
		}},
		{"TF_BUILD", func(t *testing.T, cfg *config.Resolved) (string, bool) {
			t.Setenv("TF_BUILD", "True")
			return "table", false
		}},
		{"JENKINS_URL", func(t *testing.T, cfg *config.Resolved) (string, bool) {
			t.Setenv("JENKINS_URL", "https://ci.local")
			return "table", false
		}},
		{"not a TTY", func(t *testing.T, cfg *config.Resolved) (string, bool) {
			stderrIsTerminal = func() bool { return false }
			return "table", false
		}},
		{"dev build", func(t *testing.T, cfg *config.Resolved) (string, bool) {
			version.Version = "dev"
			return "table", false
		}},
		{"nil cfg handled by caller guard", func(t *testing.T, cfg *config.Resolved) (string, bool) {
			return "table", false
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Fresh state per subtest: notice would fire if gates passed.
			noticeSetup(t, State{LastCheckUnix: time.Now().Unix(), LatestSeen: "v0.2.0"})
			cfg := noticeCfg()
			mode, quiet := tc.mutate(t, cfg)
			if tc.name == "nil cfg handled by caller guard" {
				cfg = nil
			}
			var buf bytes.Buffer
			MaybeNotice(cfg, &buf, mode, quiet)
			if buf.Len() != 0 {
				t.Errorf("gate %q failed: printed %q", tc.name, buf.String())
			}
		})
	}
}

func TestMaybeNoticeUpToDateStaysSilent(t *testing.T) {
	for _, seen := range []string{"", "v0.1.0", "v0.0.9"} {
		noticeSetup(t, State{LastCheckUnix: time.Now().Unix(), LatestSeen: seen})
		var buf bytes.Buffer
		MaybeNotice(noticeCfg(), &buf, "table", false)
		if buf.Len() != 0 {
			t.Errorf("latest_seen=%q: unexpectedly printed %q", seen, buf.String())
		}
	}
}

func TestMaybeNoticeBackgroundRefresh(t *testing.T) {
	// Stale last check (0) triggers the fire-and-forget refresh goroutine.
	noticeSetup(t, State{LastCheckUnix: 0, LatestSeen: ""})
	githubStub(t, stubRelease("v0.9.0", false, false, true), nil, nil)

	var buf bytes.Buffer
	MaybeNotice(noticeCfg(), &buf, "table", false)
	if buf.Len() != 0 {
		t.Errorf("nothing seen yet, must not print: %q", buf.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st := readState(); st.LatestSeen == "v0.9.0" && st.LastCheckUnix > 0 {
			return // state rewritten by the background goroutine
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("background check never rewrote state: %+v", readState())
}

func TestMaybeNoticeFreshCheckDoesNotRefresh(t *testing.T) {
	noticeSetup(t, State{LastCheckUnix: time.Now().Unix(), LatestSeen: "v0.2.0"})
	// No github stub: a spawned goroutine would error against the real API
	// base within 1500ms, but more importantly the state must stay put.
	before := readState()
	var buf bytes.Buffer
	MaybeNotice(noticeCfg(), &buf, "table", false)
	time.Sleep(50 * time.Millisecond)
	if after := readState(); after != before {
		t.Errorf("fresh state must not be rewritten: before %+v after %+v", before, after)
	}
}
