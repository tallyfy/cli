package cli

import (
	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/hooks"
	"github.com/tallyfy/cli/pkg/tallyfy"
)

func init() {
	register(func(root *cobra.Command) {
		cmd := &cobra.Command{
			Use:     "folder",
			Aliases: []string{"folders"},
			Short:   "Manage folders that organize blueprints and processes",
		}
		cmd.AddCommand(
			folderListCmd(),
			folderCreateCmd(),
			folderDeleteCmd(),
		)
		root.AddCommand(cmd)
	})
}

func folderListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List folders",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, org, err := folderContext(cmd)
			if err != nil {
				return err
			}
			if err := ctx.Guard("Folder", "list", "", hooks.Payload{}); err != nil {
				return err
			}
			all, _ := cmd.Flags().GetBool("all")
			limit, _ := cmd.Flags().GetInt("limit")
			folders, _, err := ctx.API.ListFolders(cmd.Context(), org, tallyfy.ListOptions{All: all, Limit: limit})
			if err != nil {
				return err
			}
			cols := []string{"ID", "NAME", "PARENT", "CREATED"}
			rows := make([][]string, 0, len(folders))
			items := make([]any, 0, len(folders))
			for i := range folders {
				f := folders[i]
				rows = append(rows, []string{f.ID, truncate(f.Name, 50), deref(f.ParentID), f.CreatedAt})
				items = append(items, f)
			}
			return ctx.RenderList(cols, rows, items)
		},
	}
	cmd.Flags().Bool("all", false, "fetch every page (default: first page only)")
	cmd.Flags().Int("limit", 0, "maximum folders to return (0 = server default page)")
	return cmd
}

func folderCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create --name <name>",
		Short: "Create a folder",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, org, err := folderContext(cmd)
			if err != nil {
				return err
			}
			fromFile, _ := cmd.Flags().GetString("from-file")
			name, _ := cmd.Flags().GetString("name")
			payload, err := nameOrFileBody(fromFile, "name", name, "folder create requires --name or --from-file")
			if err != nil {
				return err
			}
			if err := ctx.Guard("Folder", "create", "", hooks.Payload{Resource: "folder"}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("POST /organizations/%s/folders %s", org, string(payload))
				return nil
			}
			f, err := ctx.API.CreateFolder(cmd.Context(), org, payload)
			if err != nil {
				return err
			}
			return ctx.RenderItem([]string{"ID", "NAME"}, []string{f.ID, f.Name}, f)
		},
	}
	cmd.Flags().String("name", "", "folder name (required unless --from-file)")
	cmd.Flags().String("from-file", "", "read the full JSON body from a file (\"-\" for stdin)")
	return cmd
}

func folderDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a folder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := folderContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			payload := hooks.Payload{Resource: "folder", ID: id}
			if err := ctx.Guard("Folder", "delete", hooks.PreDelete, payload); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("DELETE /organizations/%s/folders/%s", org, id)
				return nil
			}
			if err := ctx.ConfirmDestructive("delete folder " + id); err != nil {
				return err
			}
			if err := ctx.API.DeleteFolder(cmd.Context(), org, id); err != nil {
				return err
			}
			ctx.FirePost(hooks.PostDelete, payload, "Folder", "delete")
			ctx.Infof("deleted folder %s\n", id)
			return nil
		},
	}
}

func folderContext(cmd *cobra.Command) (*Context, string, error) {
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
