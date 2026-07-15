// Package cli builds the tallyfy command tree and maps errors to the stable
// exit-code contract.
package cli

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/telemetry"
	"github.com/tallyfy/cli/internal/update"
	"github.com/tallyfy/cli/internal/version"
)

// NewRootCmd constructs the full command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "tallyfy",
		Short: "Deterministic, scriptable Tallyfy workflow automation",
		Long: `tallyfy is the official command-line interface for Tallyfy.

Launch processes, complete tasks, export and import blueprints, and gate
CI/CD pipelines on human approvals - deterministically, from any terminal
or pipeline, on macOS, Windows, and Linux.

Vocabulary: Tallyfy's UI says "blueprint" and "process"; the API says
"checklist" and "run". This CLI leads with the UI words and accepts the
API words as aliases.

Docs:   https://tallyfy.com/products/pro/integrations/cli/
Source: https://github.com/tallyfy/cli`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	pf := root.PersistentFlags()
	pf.String("org", "", "organization ID (overrides settings and `tallyfy org use`)")
	pf.StringP("output", "o", "", "output format: table|json|csv|ndjson")
	pf.Bool("json", false, "shorthand for -o json")
	pf.BoolP("quiet", "q", false, "suppress non-essential output")
	pf.BoolP("yes", "y", false, "assume yes for prompts (required for destructive/ask actions in scripts)")
	pf.Bool("dry-run", false, "print intended API calls without executing them")
	pf.Bool("no-input", false, "never prompt; fail instead (auto-detected when not a TTY)")
	pf.String("settings", "", "extra settings: path to a JSON file or an inline JSON object")
	pf.String("api-key", "", "API token (prefer TALLYFY_API_TOKEN in scripts)")
	pf.String("base-url", "", "API base URL (default https://api.tallyfy.com)")
	pf.Bool("verbose", false, "verbose request logging (tokens redacted)")
	pf.Bool("no-color", false, "disable color output")

	registerCommands(root)
	return root
}

// commandRegistrars is appended to by each command file's init(); this keeps
// per-resource command files self-contained and independently ownable.
var commandRegistrars []func(*cobra.Command)

func register(fn func(*cobra.Command)) { commandRegistrars = append(commandRegistrars, fn) }

func registerCommands(root *cobra.Command) {
	for _, fn := range commandRegistrars {
		fn(root)
	}
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	start := time.Now()
	// Best-effort cleanup of a leftover <binary>.old from a prior self-update
	// (notably the Windows rename dance); never blocks or errors the run.
	update.CleanupOldBinary()
	root := NewRootCmd()
	err := root.Execute()

	code := exitCodeFor(err)
	if err != nil {
		// cobra flag/arg errors are usage errors.
		if code == ExitGeneric && isCobraUsageError(err) {
			code = ExitUsage
		}
		fmt.Fprintln(os.Stderr, "tallyfy:", err.Error())
	}

	finish(start, code)
	return code
}

// isCobraUsageError detects cobra's own flag/argument parse failures.
func isCobraUsageError(err error) bool {
	msg := err.Error()
	for _, s := range []string{"unknown flag", "unknown command", "unknown shorthand flag", "invalid argument", "requires at least", "accepts at most", "arg(s), received", "flag needs an argument"} {
		if len(msg) >= len(s) && containsFold(msg, s) {
			return true
		}
	}
	return false
}

func containsFold(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			a, b := haystack[i+j], needle[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// finish emits telemetry (when enabled) and the passive update notice.
func finish(start time.Time, code int) {
	ctx := lastContext
	if ctx == nil {
		return
	}
	telemetry.Emit(ctx.Cfg, telemetry.Event{
		Command:    ctx.cmdPath,
		DurationMS: time.Since(start).Milliseconds(),
		ExitCode:   code,
		Version:    version.Version,
		OS:         runtime.GOOS + "/" + runtime.GOARCH,
	})
	update.MaybeNotice(ctx.Cfg, os.Stderr, string(ctx.OutputMode), ctx.Quiet)
}
