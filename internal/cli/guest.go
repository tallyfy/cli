package cli

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/hooks"
	"github.com/tallyfy/cli/pkg/tallyfy"
)

func init() {
	register(func(root *cobra.Command) {
		cmd := &cobra.Command{
			Use:     "guest",
			Aliases: []string{"guests"},
			Short:   "Manage guests (external task participants)",
			Long: `Manage guests - people who complete tasks by email without a full account.

Guests are identified by email address, not a numeric ID.`,
		}
		cmd.AddCommand(
			guestListCmd(),
			guestGetCmd(),
			guestCreateCmd(),
			guestUpdateCmd(),
		)
		root.AddCommand(cmd)
	})
}

func guestListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List guests",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, org, err := guestContext(cmd)
			if err != nil {
				return err
			}
			if err := ctx.Guard("Guest", "list", "", hooks.Payload{}); err != nil {
				return err
			}
			all, _ := cmd.Flags().GetBool("all")
			limit, _ := cmd.Flags().GetInt("limit")
			guests, _, err := ctx.API.ListGuests(cmd.Context(), org, tallyfy.ListOptions{All: all, Limit: limit})
			if err != nil {
				return err
			}
			return ctx.RenderList(guestColumns, guestRows(guests), guestItems(guests))
		},
	}
	cmd.Flags().Bool("all", false, "fetch every page (default: first page only)")
	cmd.Flags().Int("limit", 0, "maximum guests to return (0 = server default page)")
	return cmd
}

func guestGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <email>",
		Short: "Show one guest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := guestContext(cmd)
			if err != nil {
				return err
			}
			email := args[0]
			if err := ctx.Guard("Guest", "get", "", hooks.Payload{Resource: "guest", ID: email}); err != nil {
				return err
			}
			g, err := ctx.API.GetGuest(cmd.Context(), org, email)
			if err != nil {
				return err
			}
			return ctx.RenderItem(guestColumns, guestRow(*g), g)
		},
	}
}

func guestCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create --email <email>",
		Short: "Create a guest",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, org, err := guestContext(cmd)
			if err != nil {
				return err
			}
			fromFile, _ := cmd.Flags().GetString("from-file")
			email, _ := cmd.Flags().GetString("email")
			first, _ := cmd.Flags().GetString("first-name")
			last, _ := cmd.Flags().GetString("last-name")

			payload, err := guestBody(fromFile, email, first, last, "guest create requires --email or --from-file")
			if err != nil {
				return err
			}
			if err := ctx.Guard("Guest", "create", "", hooks.Payload{Resource: "guest", ID: email}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("POST /organizations/%s/guests %s", org, string(payload))
				return nil
			}
			g, err := ctx.API.CreateGuest(cmd.Context(), org, payload)
			if err != nil {
				return err
			}
			return ctx.RenderItem(guestColumns, guestRow(*g), g)
		},
	}
	cmd.Flags().String("email", "", "guest email (required unless --from-file)")
	cmd.Flags().String("first-name", "", "first name")
	cmd.Flags().String("last-name", "", "last name")
	cmd.Flags().String("from-file", "", "read the full JSON body from a file (\"-\" for stdin)")
	return cmd
}

func guestUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <email>",
		Short: "Update a guest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := guestContext(cmd)
			if err != nil {
				return err
			}
			email := args[0]
			fromFile, _ := cmd.Flags().GetString("from-file")
			first, _ := cmd.Flags().GetString("first-name")
			last, _ := cmd.Flags().GetString("last-name")

			payload, err := guestBody(fromFile, "", first, last, "provide --first-name, --last-name, or --from-file")
			if err != nil {
				return err
			}
			if err := ctx.Guard("Guest", "update", "", hooks.Payload{Resource: "guest", ID: email}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("PUT /organizations/%s/guests/%s %s", org, email, string(payload))
				return nil
			}
			g, err := ctx.API.UpdateGuest(cmd.Context(), org, email, payload)
			if err != nil {
				return err
			}
			return ctx.RenderItem(guestColumns, guestRow(*g), g)
		},
	}
	cmd.Flags().String("first-name", "", "new first name")
	cmd.Flags().String("last-name", "", "new last name")
	cmd.Flags().String("from-file", "", "read the full JSON body from a file (\"-\" for stdin)")
	return cmd
}

// --- guest rendering + helpers ----------------------------------------------

var guestColumns = []string{"EMAIL", "NAME", "CREATED"}

func guestRow(g tallyfy.Guest) []string {
	name := strings.TrimSpace(g.FirstName + " " + g.LastName)
	return []string{g.Email, name, g.CreatedAt}
}

func guestRows(gs []tallyfy.Guest) [][]string {
	rows := make([][]string, 0, len(gs))
	for _, g := range gs {
		rows = append(rows, guestRow(g))
	}
	return rows
}

func guestItems(gs []tallyfy.Guest) []any {
	items := make([]any, 0, len(gs))
	for _, g := range gs {
		items = append(items, g)
	}
	return items
}

func guestContext(cmd *cobra.Command) (*Context, string, error) {
	ctx, err := NewContext(cmd, true)
	if err != nil {
		return nil, "", err
	}
	org, err := ctx.RequireOrg()
	if err != nil {
		return nil, "", err
	}
	return ctx, org, nil
}

func guestBody(fromFile, email, first, last, missingMsg string) (json.RawMessage, error) {
	if fromFile != "" {
		data, err := readInput(fromFile)
		if err != nil {
			return nil, &UsageError{Msg: err.Error()}
		}
		if !json.Valid(data) {
			return nil, &UsageError{Msg: "--from-file is not valid JSON"}
		}
		return data, nil
	}
	body := map[string]string{}
	if email != "" {
		body["email"] = email
	}
	if first != "" {
		body["first_name"] = first
	}
	if last != "" {
		body["last_name"] = last
	}
	if len(body) == 0 {
		return nil, &UsageError{Msg: missingMsg}
	}
	return json.Marshal(body)
}
