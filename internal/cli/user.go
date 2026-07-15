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
			Use:     "user",
			Aliases: []string{"member", "members", "users"},
			Short:   "Manage organization members",
			Long: `Manage the members of an organization.

Members are full Tallyfy users (as opposed to guests, who complete tasks by
email without an account). Invite members, change roles, and disable or
re-enable accounts.`,
		}
		cmd.AddCommand(
			userListCmd(),
			userInviteCmd(),
			userRoleCmd(),
			userDisableCmd(),
			userEnableCmd(),
		)
		root.AddCommand(cmd)
	})
}

func userListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List members",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, org, err := userContext(cmd)
			if err != nil {
				return err
			}
			if err := ctx.Guard("User", "list", "", hooks.Payload{}); err != nil {
				return err
			}
			all, _ := cmd.Flags().GetBool("all")
			limit, _ := cmd.Flags().GetInt("limit")
			users, _, err := ctx.API.ListUsers(cmd.Context(), org, tallyfy.ListOptions{All: all, Limit: limit})
			if err != nil {
				return err
			}
			cols := []string{"ID", "EMAIL", "NAME", "ROLE", "STATUS"}
			rows := make([][]string, 0, len(users))
			items := make([]any, 0, len(users))
			for i := range users {
				u := users[i]
				name := strings.TrimSpace(u.FirstName + " " + u.LastName)
				rows = append(rows, []string{u.ID.String(), u.Email, name, u.Role, u.Status})
				items = append(items, u)
			}
			return ctx.RenderList(cols, rows, items)
		},
	}
	cmd.Flags().Bool("all", false, "fetch every page (default: first page only)")
	cmd.Flags().Int("limit", 0, "maximum members to return (0 = server default page)")
	return cmd
}

func userInviteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invite",
		Short: "Invite a member by email",
		Long: `Invite a member to the organization.

  tallyfy user invite --email jo@example.com --first-name Jo --last-name Lee --role standard

Or send a full JSON body:

  tallyfy user invite --from-file invite.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, org, err := userContext(cmd)
			if err != nil {
				return err
			}
			fromFile, _ := cmd.Flags().GetString("from-file")
			email, _ := cmd.Flags().GetString("email")
			first, _ := cmd.Flags().GetString("first-name")
			last, _ := cmd.Flags().GetString("last-name")
			role, _ := cmd.Flags().GetString("role")

			payload, err := userInviteBody(fromFile, email, first, last, role)
			if err != nil {
				return err
			}
			if err := ctx.Guard("User", "invite", "", hooks.Payload{Resource: "user"}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("POST /organizations/%s/users/invite %s", org, string(payload))
				return nil
			}
			raw, err := ctx.API.InviteUser(cmd.Context(), org, payload)
			if err != nil {
				return err
			}
			ctx.Infof("invited %s\n", email)
			return ctx.RenderItem([]string{"RESULT", "EMAIL"}, []string{"invited", email}, rawItem(raw))
		},
	}
	cmd.Flags().String("email", "", "email address to invite")
	cmd.Flags().String("first-name", "", "first name")
	cmd.Flags().String("last-name", "", "last name")
	cmd.Flags().String("role", "", "role (e.g. admin, standard, light)")
	cmd.Flags().String("from-file", "", "read the full JSON invite body from a file (\"-\" for stdin)")
	return cmd
}

func userRoleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "role <userID> --role <role>",
		Short: "Change a member's role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := userContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			role, _ := cmd.Flags().GetString("role")
			if strings.TrimSpace(role) == "" {
				return &UsageError{Msg: "user role requires --role <role>"}
			}
			if err := ctx.Guard("User", "role", "", hooks.Payload{Resource: "user", ID: id}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("PUT /organizations/%s/users/%s/role {role:%q}", org, id, role)
				return nil
			}
			if err := ctx.API.SetUserRole(cmd.Context(), org, id, role); err != nil {
				return err
			}
			ctx.Infof("set role of user %s to %s\n", id, role)
			return ctx.RenderItem([]string{"ID", "ROLE"}, []string{id, role},
				map[string]string{"id": id, "role": role})
		},
	}
	cmd.Flags().String("role", "", "new role (required)")
	return cmd
}

func userDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable <userID>",
		Short: "Disable (deactivate) a member",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := userContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			if err := ctx.Guard("User", "disable", "", hooks.Payload{Resource: "user", ID: id}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("DELETE /organizations/%s/users/%s/disable", org, id)
				return nil
			}
			if err := ctx.ConfirmDestructive("disable user " + id); err != nil {
				return err
			}
			if err := ctx.API.DisableUser(cmd.Context(), org, id); err != nil {
				return err
			}
			ctx.Infof("disabled user %s\n", id)
			return nil
		},
	}
}

func userEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable <userID>",
		Short: "Re-enable a disabled member",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := userContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			if err := ctx.Guard("User", "enable", "", hooks.Payload{Resource: "user", ID: id}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("PUT /organizations/%s/users/%s/enable", org, id)
				return nil
			}
			if err := ctx.API.EnableUser(cmd.Context(), org, id); err != nil {
				return err
			}
			ctx.Infof("enabled user %s\n", id)
			return nil
		},
	}
}

// userContext builds an authenticated, org-scoped context for a user command.
func userContext(cmd *cobra.Command) (*Context, string, error) {
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

// userInviteBody builds an invite payload from a JSON file or from flags.
func userInviteBody(fromFile, email, first, last, role string) (json.RawMessage, error) {
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
	if strings.TrimSpace(email) == "" {
		return nil, &UsageError{Msg: "user invite requires --email or --from-file"}
	}
	body := map[string]string{"email": email}
	if first != "" {
		body["first_name"] = first
	}
	if last != "" {
		body["last_name"] = last
	}
	if role != "" {
		body["role"] = role
	}
	return json.Marshal(body)
}
