package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/auth"
	"github.com/tallyfy/cli/internal/config"
	"github.com/tallyfy/cli/internal/permissions"
)

func init() {
	register(func(root *cobra.Command) {
		root.AddCommand(&cobra.Command{
			Use:   "doctor",
			Short: "Diagnose configuration, authentication, and connectivity",
			Long: `Run a series of read-only checks and report what is healthy and what is
not: configuration, credential source, API reachability, active org,
permission-rule validity, and the credential backend.

Run this first whenever a command misbehaves.`,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				ctx, err := NewContext(cmd, false)
				if err != nil {
					return err
				}
				apiKey, _ := cmd.Flags().GetString("api-key")
				checks := runDoctorChecks(cmd, ctx, apiKey)

				cols := []string{"CHECK", "STATUS", "DETAIL"}
				rows := make([][]string, 0, len(checks))
				items := make([]any, 0, len(checks))
				for _, c := range checks {
					rows = append(rows, []string{c.name, c.status, c.detail})
					items = append(items, map[string]string{"check": c.name, "status": c.status, "detail": c.detail})
				}
				return ctx.RenderList(cols, rows, items)
			},
		})
	})
}

type doctorCheck struct {
	name   string
	status string // ok | warn | fail
	detail string
}

func runDoctorChecks(cmd *cobra.Command, ctx *Context, apiKey string) []doctorCheck {
	var checks []doctorCheck

	// 1. Configuration.
	cfgStatus, cfgDetail := "ok", "loaded"
	if n := len(ctx.Cfg.Warnings); n > 0 {
		cfgStatus = "warn"
		cfgDetail = fmt.Sprintf("loaded with %d warning(s): %s", n, ctx.Cfg.Warnings[0])
	}
	checks = append(checks, doctorCheck{"configuration", cfgStatus, cfgDetail})

	// 2. Base URL.
	checks = append(checks, doctorCheck{"base URL", "ok", ctx.Cfg.BaseURL})

	// 3. Credential + 4. API identity.
	cred, resolveErr := auth.NewResolver().Resolve(ctx.Cfg, apiKey)
	store := auth.NewStore()
	if resolveErr != nil || cred == nil {
		checks = append(checks, doctorCheck{"credential", "fail", "none found (run `tallyfy login` or set TALLYFY_API_TOKEN)"})
		checks = append(checks, doctorCheck{"API identity", "fail", "skipped (no credential)"})
	} else {
		checks = append(checks, doctorCheck{"credential", "ok", "resolved from " + string(cred.Source)})
		if me, verr := auth.ValidateToken(ctx.Cfg.BaseURL, cred.Token); verr == nil {
			checks = append(checks, doctorCheck{"API identity", "ok", fmt.Sprintf("%s (%s)", me.Email, me.ID.String())})
		} else {
			checks = append(checks, doctorCheck{"API identity", "fail", "token did not validate: " + verr.Error()})
		}
	}

	// 5. Active organization.
	if ctx.Org == "" {
		checks = append(checks, doctorCheck{"active org", "warn", "none set (pass --org or run `tallyfy org use <id>`)"})
	} else {
		checks = append(checks, doctorCheck{"active org", "ok", ctx.Org})
	}

	// 6. Permission rules parse.
	invalid := countInvalidRules(ctx.Cfg)
	if invalid > 0 {
		checks = append(checks, doctorCheck{"permission rules", "warn", fmt.Sprintf("%d rule(s) failed to parse and are ignored", invalid)})
	} else {
		checks = append(checks, doctorCheck{"permission rules", "ok", fmt.Sprintf("default mode %q, all rules valid", ctx.Cfg.PermissionDefaultMode)})
	}

	// 7. Credential backend.
	checks = append(checks, doctorCheck{"credential backend", "ok", store.Backend()})

	// 8. Managed policy.
	if ctx.Cfg.ForceOrg != "" || ctx.Cfg.AllowManagedRulesOnly {
		checks = append(checks, doctorCheck{"managed policy", "ok", "active (managed settings present)"})
	} else {
		checks = append(checks, doctorCheck{"managed policy", "ok", "none"})
	}

	return checks
}

func countInvalidRules(cfg *config.Resolved) int {
	invalid := 0
	for _, set := range [][]config.Rule{cfg.Allow, cfg.Ask, cfg.Deny} {
		for _, r := range set {
			if _, err := permissions.Parse(r.Raw); err != nil {
				invalid++
			}
		}
	}
	return invalid
}
