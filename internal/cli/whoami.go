package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/hooks"
)

func init() {
	register(func(root *cobra.Command) {
		root.AddCommand(&cobra.Command{
			Use:   "whoami",
			Short: "Show the authenticated user and active organization",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				ctx, err := NewContext(cmd, true)
				if err != nil {
					return err
				}
				if err := ctx.Guard("Me", "get", "", hooks.Payload{}); err != nil {
					return err
				}
				me, err := ctx.API.Me(cmd.Context())
				if err != nil {
					return err
				}
				name := strings.TrimSpace(me.FirstName + " " + me.LastName)
				org := ctx.Org
				if org == "" {
					org = "(none - run `tallyfy org use <id>`)"
				}
				cols := []string{"ID", "EMAIL", "NAME", "ORG"}
				row := []string{me.ID.String(), me.Email, name, org}
				return ctx.RenderItem(cols, row, map[string]any{
					"id":    me.ID,
					"email": me.Email,
					"name":  name,
					"org":   ctx.Org,
				})
			},
		})
	})
}
