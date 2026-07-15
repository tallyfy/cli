package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/hooks"
	"github.com/tallyfy/cli/pkg/tallyfy"
)

func init() {
	register(func(root *cobra.Command) {
		cmd := &cobra.Command{
			Use:     "group",
			Aliases: []string{"groups"},
			Short:   "Manage groups of members and guests",
		}
		cmd.AddCommand(
			groupListCmd(),
			groupCreateCmd(),
			groupUpdateCmd(),
			groupDeleteCmd(),
		)
		root.AddCommand(cmd)
	})
}

func groupListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List groups",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, org, err := groupContext(cmd)
			if err != nil {
				return err
			}
			if err := ctx.Guard("Group", "list", "", hooks.Payload{}); err != nil {
				return err
			}
			all, _ := cmd.Flags().GetBool("all")
			limit, _ := cmd.Flags().GetInt("limit")
			groups, _, err := ctx.API.ListGroups(cmd.Context(), org, tallyfy.ListOptions{All: all, Limit: limit})
			if err != nil {
				return err
			}
			cols := []string{"ID", "NAME", "CREATED"}
			rows := make([][]string, 0, len(groups))
			items := make([]any, 0, len(groups))
			for i := range groups {
				g := groups[i]
				rows = append(rows, []string{g.ID, truncate(g.Name, 50), g.CreatedAt})
				items = append(items, g)
			}
			return ctx.RenderList(cols, rows, items)
		},
	}
	cmd.Flags().Bool("all", false, "fetch every page (default: first page only)")
	cmd.Flags().Int("limit", 0, "maximum groups to return (0 = server default page)")
	return cmd
}

func groupCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create --name <name>",
		Short: "Create a group",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, org, err := groupContext(cmd)
			if err != nil {
				return err
			}
			fromFile, _ := cmd.Flags().GetString("from-file")
			name, _ := cmd.Flags().GetString("name")
			payload, err := nameOrFileBody(fromFile, "name", name, "group create requires --name or --from-file")
			if err != nil {
				return err
			}
			if err := ctx.Guard("Group", "create", "", hooks.Payload{Resource: "group"}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("POST /organizations/%s/groups %s", org, string(payload))
				return nil
			}
			g, err := ctx.API.CreateGroup(cmd.Context(), org, payload)
			if err != nil {
				return err
			}
			return ctx.RenderItem([]string{"ID", "NAME"}, []string{g.ID, g.Name}, g)
		},
	}
	cmd.Flags().String("name", "", "group name (required unless --from-file)")
	cmd.Flags().String("from-file", "", "read the full JSON body from a file (\"-\" for stdin)")
	return cmd
}

func groupUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := groupContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			fromFile, _ := cmd.Flags().GetString("from-file")
			name, _ := cmd.Flags().GetString("name")
			payload, err := nameOrFileBody(fromFile, "name", name, "provide --name or --from-file")
			if err != nil {
				return err
			}
			if err := ctx.Guard("Group", "update", "", hooks.Payload{Resource: "group", ID: id}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("PUT /organizations/%s/groups/%s %s", org, id, string(payload))
				return nil
			}
			g, err := ctx.API.UpdateGroup(cmd.Context(), org, id, payload)
			if err != nil {
				return err
			}
			return ctx.RenderItem([]string{"ID", "NAME"}, []string{g.ID, g.Name}, g)
		},
	}
	cmd.Flags().String("name", "", "new group name")
	cmd.Flags().String("from-file", "", "read the full JSON body from a file (\"-\" for stdin)")
	return cmd
}

func groupDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := groupContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			payload := hooks.Payload{Resource: "group", ID: id}
			if err := ctx.Guard("Group", "delete", hooks.PreDelete, payload); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("DELETE /organizations/%s/groups/%s", org, id)
				return nil
			}
			if err := ctx.ConfirmDestructive("delete group " + id); err != nil {
				return err
			}
			if err := ctx.API.DeleteGroup(cmd.Context(), org, id); err != nil {
				return err
			}
			ctx.FirePost(hooks.PostDelete, payload, "Group", "delete")
			ctx.Infof("deleted group %s\n", id)
			return nil
		},
	}
}

func groupContext(cmd *cobra.Command) (*Context, string, error) {
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

// nameOrFileBody builds a {field: value} JSON body, or reads a raw JSON file.
func nameOrFileBody(fromFile, field, value, missingMsg string) (json.RawMessage, error) {
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
	if value == "" {
		return nil, &UsageError{Msg: missingMsg}
	}
	return json.Marshal(map[string]string{field: value})
}
