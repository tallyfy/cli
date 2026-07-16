package cli

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/tallyfy/cli/internal/auth"
	"github.com/tallyfy/cli/internal/config"
	"github.com/tallyfy/cli/internal/hooks"
	"github.com/tallyfy/cli/internal/output"
	"github.com/tallyfy/cli/internal/permissions"
	"github.com/tallyfy/cli/internal/version"
	"github.com/tallyfy/cli/pkg/tallyfy"
)

// Context carries everything a command needs for one invocation.
type Context struct {
	Cfg  *config.Resolved
	Cred *auth.Credential
	API  *tallyfy.Client

	Org string // effective org (managed forceOrg > --org > state.json current > settings)

	OutputMode  output.Mode
	Interactive bool // stdin AND stdout are TTYs and --no-input not set
	Yes         bool
	DryRun      bool
	Quiet       bool
	Verbose     bool

	Started time.Time
	cmdPath string // e.g. "blueprint list" (for telemetry)
}

// globalFlags reads the persistent flags defined on the root command.
type globalFlags struct {
	Org      string
	Output   string
	JSON     bool
	Quiet    bool
	Yes      bool
	DryRun   bool
	NoInput  bool
	Settings string
	APIKey   string
	BaseURL  string
	Verbose  bool
	NoColor  bool
}

func readGlobalFlags(cmd *cobra.Command) globalFlags {
	f := globalFlags{}
	fl := cmd.Flags()
	f.Org, _ = fl.GetString("org")
	f.Output, _ = fl.GetString("output")
	f.JSON, _ = fl.GetBool("json")
	f.Quiet, _ = fl.GetBool("quiet")
	f.Yes, _ = fl.GetBool("yes")
	f.DryRun, _ = fl.GetBool("dry-run")
	f.NoInput, _ = fl.GetBool("no-input")
	f.Settings, _ = fl.GetString("settings")
	f.APIKey, _ = fl.GetString("api-key")
	f.BaseURL, _ = fl.GetString("base-url")
	f.Verbose, _ = fl.GetBool("verbose")
	f.NoColor, _ = fl.GetBool("no-color")
	return f
}

// lastContext is used by Execute() for the end-of-run update notice and
// telemetry without re-loading config.
var lastContext *Context

// NewContext loads config and (when needsAuth) resolves a credential and
// builds the API client. Commands call this at the top of RunE.
func NewContext(cmd *cobra.Command, needsAuth bool) (*Context, error) {
	f := readGlobalFlags(cmd)
	out := f.Output
	if f.JSON {
		out = "json"
	}
	cfg, err := config.Load(config.Overrides{
		SettingsArg: f.Settings,
		Org:         f.Org,
		Output:      out,
		BaseURL:     f.BaseURL,
	})
	if err != nil {
		return nil, err
	}
	for _, w := range cfg.Warnings {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
	}

	mode, err := output.ParseMode(cfg.Output)
	if err != nil {
		return nil, &UsageError{Msg: err.Error()}
	}

	interactive := !f.NoInput &&
		term.IsTerminal(int(os.Stdin.Fd())) &&
		term.IsTerminal(int(os.Stdout.Fd()))

	ctx := &Context{
		Cfg:         cfg,
		Org:         effectiveOrg(cfg),
		OutputMode:  mode,
		Interactive: interactive,
		Yes:         f.Yes,
		DryRun:      f.DryRun,
		Quiet:       f.Quiet,
		Verbose:     f.Verbose,
		Started:     time.Now(),
		cmdPath:     strings.TrimPrefix(cmd.CommandPath(), "tallyfy "),
	}

	if needsAuth {
		cred, err := auth.NewResolver().Resolve(cfg, f.APIKey)
		if err != nil {
			return nil, err
		}
		ctx.Cred = cred
		opts := tallyfy.Options{
			BaseURL:   cfg.BaseURL,
			Token:     cred.Token,
			UserAgent: fmt.Sprintf("tallyfy-cli/%s (%s/%s)", version.Version, runtime.GOOS, runtime.GOARCH),
		}
		if f.Verbose {
			opts.Verbose = cmd.ErrOrStderr()
		}
		ctx.API = tallyfy.New(opts)
	}

	lastContext = ctx
	return ctx, nil
}

// effectiveOrg applies managed forceOrg above everything else.
func effectiveOrg(cfg *config.Resolved) string {
	if cfg.ForceOrg != "" {
		return cfg.ForceOrg
	}
	return cfg.Org
}

// RequireOrg errors with a helpful message when no org context exists.
func (c *Context) RequireOrg() (string, error) {
	if c.Org == "" {
		return "", fmt.Errorf("no organization selected: pass --org <id>, run `tallyfy org use <id>`, or set \"org\" in settings")
	}
	return c.Org, nil
}

// Guard enforces the permission engine and fires the Pre* hook for a
// mutating command. Call before any API mutation. meta becomes hook args.
func (c *Context) Guard(resource, verb string, hookEvent string, payload hooks.Payload) error {
	tok := permissions.Token{Resource: resource, Verb: verb}
	res := permissions.Evaluate(permissions.Input{
		Token:                 tok,
		Org:                   c.Org,
		Allow:                 toRules(c.Cfg.Allow),
		AskRules:              toRules(c.Cfg.Ask),
		Deny:                  toRules(c.Cfg.Deny),
		DefaultMode:           c.Cfg.PermissionDefaultMode,
		AllowManagedRulesOnly: c.Cfg.AllowManagedRulesOnly,
	})
	switch res.Decision {
	case permissions.Deny:
		// Deny always blocks, even a dry-run preview.
		return &PermissionDeniedError{Token: tok, Result: res}
	case permissions.Ask:
		// A dry-run executes nothing, so it previews without confirmation.
		if !c.DryRun && !c.confirm(fmt.Sprintf("Proceed with %s?", tok.String())) {
			res.Reason = "declined (ask rule; use --yes in non-interactive runs)"
			return &PermissionDeniedError{Token: tok, Result: res}
		}
	}
	// Pre* hooks have side effects (exec/HTTP), so they never fire during a
	// dry run - the preview reports intended API calls only.
	if hookEvent != "" && !c.DryRun {
		payload.Event = hookEvent
		payload.Org = c.Org
		payload.Timestamp = time.Now().UTC()
		warns, err := hooks.NewRunner(c.hookOptions(tok)).Fire(hookEvent, c.Cfg.Hooks[hookEvent], payload)
		for _, w := range warns {
			fmt.Fprintln(os.Stderr, "warning:", w)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// FirePost fires an advisory Post* hook (never fails the command).
func (c *Context) FirePost(event string, payload hooks.Payload, tokenResource, tokenVerb string) {
	if len(c.Cfg.Hooks[event]) == 0 {
		return
	}
	payload.Event = event
	payload.Org = c.Org
	payload.Timestamp = time.Now().UTC()
	tok := permissions.Token{Resource: tokenResource, Verb: tokenVerb}
	warns, _ := hooks.NewRunner(c.hookOptions(tok)).Fire(event, c.Cfg.Hooks[event], payload)
	for _, w := range warns {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
}

func (c *Context) hookOptions(tok permissions.Token) hooks.Options {
	return hooks.Options{
		Env:              c.Cfg.Env,
		WorkspaceTrusted: config.WorkspaceTrusted(c.Cfg.ProjectDir),
		MatchToken: func(matcher string) (bool, error) {
			return permissions.MatchesMatcher(matcher, tok, c.Org)
		},
	}
}

// confirm prompts interactively; non-interactive resolves via --yes.
func (c *Context) confirm(prompt string) bool {
	if c.Yes {
		return true
	}
	if !c.Interactive {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s [y/N]: ", prompt)
	var answer string
	_, _ = fmt.Fscanln(os.Stdin, &answer)
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

// ConfirmDestructive gates destructive verbs: requires --yes when
// non-interactive, or an interactive yes.
func (c *Context) ConfirmDestructive(what string) error {
	if c.confirm(fmt.Sprintf("Really %s? This cannot be undone via the CLI", what)) {
		return nil
	}
	return &UsageError{Msg: fmt.Sprintf("refusing to %s without confirmation (pass --yes in non-interactive runs)", what)}
}

func toRules(in []config.Rule) []permissions.Rule {
	out := make([]permissions.Rule, 0, len(in))
	for _, r := range in {
		p, err := permissions.Parse(r.Raw)
		if err != nil {
			continue // invalid rules are reported by doctor; skip at runtime
		}
		p.Scope = r.Scope
		out = append(out, p)
	}
	return out
}
