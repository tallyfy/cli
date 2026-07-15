package cli

import (
	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/config"
	"github.com/tallyfy/cli/internal/hooks"
	"github.com/tallyfy/cli/pkg/tallyfy"
)

func init() {
	register(func(root *cobra.Command) {
		cmd := &cobra.Command{
			Use:   "org",
			Short: "List your organizations and choose the active one",
			Long: `List the organizations you belong to and select which one commands
target by default.

The active org is stored in state.json and used whenever --org is not passed
and no "org" setting is configured. Managed policy (forceOrg) overrides it.`,
		}
		cmd.AddCommand(orgListCmd(), orgUseCmd(), orgCurrentCmd())
		root.AddCommand(cmd)
	})
}

func orgListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List organizations you belong to",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := NewContext(cmd, true)
			if err != nil {
				return err
			}
			if err := ctx.Guard("Org", "list", "", hooks.Payload{}); err != nil {
				return err
			}
			all, _ := cmd.Flags().GetBool("all")
			orgs, _, err := ctx.API.MyOrganizations(cmd.Context(), tallyfy.ListOptions{All: all})
			if err != nil {
				return err
			}
			current := ctx.Org
			cols := []string{"ACTIVE", "ID", "NAME", "CREATED"}
			rows := make([][]string, 0, len(orgs))
			items := make([]any, 0, len(orgs))
			for i := range orgs {
				o := orgs[i]
				marker := ""
				if o.ID == current {
					marker = "*"
				}
				rows = append(rows, []string{marker, o.ID, truncate(o.Name, 50), o.CreatedAt})
				items = append(items, o)
			}
			return ctx.RenderList(cols, rows, items)
		},
	}
	cmd.Flags().Bool("all", false, "fetch every page (default: first page only)")
	return cmd
}

func orgUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <id>",
		Short: "Set the active organization",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := NewContext(cmd, true)
			if err != nil {
				return err
			}
			id := args[0]
			if err := ctx.Guard("Org", "use", "", hooks.Payload{Resource: "org", ID: id}); err != nil {
				return err
			}
			// Validate the org is one the user belongs to (catches typos before
			// every later command fails with a confusing 404).
			orgs, _, err := ctx.API.MyOrganizations(cmd.Context(), tallyfy.ListOptions{All: true})
			if err != nil {
				return err
			}
			found := false
			for _, o := range orgs {
				if o.ID == id {
					found = true
					break
				}
			}
			if !found {
				return &UsageError{Msg: "you do not belong to organization " + id + " (run `tallyfy org list`)"}
			}
			state, err := config.LoadState()
			if err != nil {
				return err
			}
			state.CurrentOrg = id
			if err := config.SaveState(state); err != nil {
				return err
			}
			ctx.Infof("active organization set to %s\n", id)
			return nil
		},
	}
}

func orgCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Print the active organization ID",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := NewContext(cmd, false)
			if err != nil {
				return err
			}
			if ctx.Org == "" {
				return &UsageError{Msg: "no active organization (run `tallyfy org use <id>` or pass --org)"}
			}
			ctx.Printf("%s\n", ctx.Org)
			return nil
		},
	}
}
