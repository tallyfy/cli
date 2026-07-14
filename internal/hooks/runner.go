package hooks

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tallyfy/cli/internal/config"
)

// defaultTimeout applies to exec hooks when Options.Timeout is unset.
const defaultTimeout = 30 * time.Second

type runner struct {
	opts Options
}

// NewRunner builds the hook Runner consumed by internal/cli. A zero (or
// negative) Options.Timeout falls back to 30s per exec hook.
func NewRunner(opts Options) Runner {
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	return &runner{opts: opts}
}

// blocking reports whether hook failures block the command: only Pre* events
// enforce (spec §6.7); everything else is advisory.
func blocking(event string) bool { return strings.HasPrefix(event, "Pre") }

// Fire runs the hooks registered for event, in order. The payload is
// marshalled once and written to each exec hook's stdin / POSTed to each
// HTTP hook. On a Pre* event the first failing hook returns *BlockError and
// stops the remaining hooks; advisory failures and skipped hooks only append
// warnings. Warnings collected so far are returned in all cases.
func (r *runner) Fire(event string, hs []config.Hook, payload Payload) ([]string, error) {
	if len(hs) == 0 {
		return nil, nil
	}
	var warnings []string
	body, err := json.Marshal(payload)
	if err != nil {
		// Without a payload no hook can run. Fail closed on enforcing
		// (Pre*) events; downgrade to a warning on advisory ones.
		if blocking(event) {
			return warnings, fmt.Errorf("hooks: marshal %s payload: %w", event, err)
		}
		return append(warnings, fmt.Sprintf("hooks: marshal %s payload: %v", event, err)), nil
	}
	for _, h := range hs {
		// A committed .tallyfy/settings.json from a cloned repo must never
		// execute code before `tallyfy trust` (spec §6.7 security rule).
		if h.Scope == config.ScopeProject && !r.opts.WorkspaceTrusted {
			warnings = append(warnings, "skipping project-scope hook (workspace not trusted; run `tallyfy trust`)")
			continue
		}
		if r.opts.MatchToken != nil {
			ok, merr := r.opts.MatchToken(h.Matcher)
			if merr != nil {
				warnings = append(warnings, fmt.Sprintf("skipping %s hook with invalid matcher %q: %v", event, h.Matcher, merr))
				continue
			}
			if !ok {
				continue // matcher filtering is normal, not warning-worthy
			}
		}
		switch typ := strings.ToLower(strings.TrimSpace(h.Type)); typ {
		case "", "exec":
			if strings.TrimSpace(h.Command) == "" {
				warnings = append(warnings, fmt.Sprintf("skipping %s exec hook: no command configured", event))
				continue
			}
			stderr, runErr := r.runExec(h.Command, body)
			if runErr != nil {
				if blocking(event) {
					return warnings, &BlockError{HookDesc: h.Command, Stderr: stderr}
				}
				warnings = append(warnings, advisoryWarning(event, h.Command, runErr, stderr))
			}
		case "http":
			if len(r.opts.AllowedHTTPHosts) == 0 {
				warnings = append(warnings, "http hooks disabled: no allowlist configured")
				continue
			}
			host, herr := hookHost(h.URL)
			if herr != nil {
				warnings = append(warnings, fmt.Sprintf("skipping %s http hook %q: %v", event, h.URL, herr))
				continue
			}
			if !hostAllowed(host, r.opts.AllowedHTTPHosts) {
				warnings = append(warnings, fmt.Sprintf("skipping %s http hook %q: host %q not in allowlist", event, h.URL, host))
				continue
			}
			summary, runErr := r.runHTTP(h.URL, body, r.opts.AllowedHTTPHosts)
			if runErr != nil {
				if blocking(event) {
					return warnings, &BlockError{HookDesc: h.URL, Stderr: summary}
				}
				warnings = append(warnings, advisoryWarning(event, h.URL, runErr, ""))
			}
		default:
			warnings = append(warnings, fmt.Sprintf("skipping %s hook: unsupported type %q", event, h.Type))
		}
	}
	return warnings, nil
}

// advisoryWarning renders a non-blocking hook failure, appending captured
// stderr when it adds information beyond the error itself.
func advisoryWarning(event, desc string, err error, stderr string) string {
	msg := fmt.Sprintf("%s hook %q failed: %v", event, desc, err)
	if stderr != "" && stderr != err.Error() {
		msg += ": " + stderr
	}
	return msg
}
