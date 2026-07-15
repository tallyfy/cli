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
			Use:     "tag",
			Aliases: []string{"tags"},
			Short:   "Manage tags applied to blueprints and processes",
		}
		cmd.AddCommand(
			tagListCmd(),
			tagCreateCmd(),
			tagUpdateCmd(),
			tagDeleteCmd(),
		)
		root.AddCommand(cmd)
	})
}

func tagListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tags",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, org, err := tagContext(cmd)
			if err != nil {
				return err
			}
			if err := ctx.Guard("Tag", "list", "", hooks.Payload{}); err != nil {
				return err
			}
			all, _ := cmd.Flags().GetBool("all")
			limit, _ := cmd.Flags().GetInt("limit")
			tags, _, err := ctx.API.ListTags(cmd.Context(), org, tallyfy.ListOptions{All: all, Limit: limit})
			if err != nil {
				return err
			}
			cols := []string{"ID", "TITLE", "COLOR"}
			rows := make([][]string, 0, len(tags))
			items := make([]any, 0, len(tags))
			for i := range tags {
				t := tags[i]
				rows = append(rows, []string{t.ID, truncate(t.Title, 50), t.Color})
				items = append(items, t)
			}
			return ctx.RenderList(cols, rows, items)
		},
	}
	cmd.Flags().Bool("all", false, "fetch every page (default: first page only)")
	cmd.Flags().Int("limit", 0, "maximum tags to return (0 = server default page)")
	return cmd
}

func tagCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create --title <title>",
		Short: "Create a tag",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, org, err := tagContext(cmd)
			if err != nil {
				return err
			}
			payload, err := tagBody(cmd, "tag create requires --title or --from-file")
			if err != nil {
				return err
			}
			if err := ctx.Guard("Tag", "create", "", hooks.Payload{Resource: "tag"}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("POST /organizations/%s/tags %s", org, string(payload))
				return nil
			}
			t, err := ctx.API.CreateTag(cmd.Context(), org, payload)
			if err != nil {
				return err
			}
			return ctx.RenderItem([]string{"ID", "TITLE", "COLOR"}, []string{t.ID, t.Title, t.Color}, t)
		},
	}
	cmd.Flags().String("title", "", "tag title (required unless --from-file)")
	cmd.Flags().String("color", "", "tag color (e.g. #01803d)")
	cmd.Flags().String("from-file", "", "read the full JSON body from a file (\"-\" for stdin)")
	return cmd
}

func tagUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a tag",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := tagContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			payload, err := tagBody(cmd, "provide --title, --color, or --from-file")
			if err != nil {
				return err
			}
			if err := ctx.Guard("Tag", "update", "", hooks.Payload{Resource: "tag", ID: id}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("PUT /organizations/%s/tags/%s %s", org, id, string(payload))
				return nil
			}
			t, err := ctx.API.UpdateTag(cmd.Context(), org, id, payload)
			if err != nil {
				return err
			}
			return ctx.RenderItem([]string{"ID", "TITLE", "COLOR"}, []string{t.ID, t.Title, t.Color}, t)
		},
	}
	cmd.Flags().String("title", "", "new tag title")
	cmd.Flags().String("color", "", "new tag color")
	cmd.Flags().String("from-file", "", "read the full JSON body from a file (\"-\" for stdin)")
	return cmd
}

func tagDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a tag",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := tagContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			payload := hooks.Payload{Resource: "tag", ID: id}
			if err := ctx.Guard("Tag", "delete", hooks.PreDelete, payload); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("DELETE /organizations/%s/tags/%s", org, id)
				return nil
			}
			if err := ctx.ConfirmDestructive("delete tag " + id); err != nil {
				return err
			}
			if err := ctx.API.DeleteTag(cmd.Context(), org, id); err != nil {
				return err
			}
			ctx.FirePost(hooks.PostDelete, payload, "Tag", "delete")
			ctx.Infof("deleted tag %s\n", id)
			return nil
		},
	}
}

func tagContext(cmd *cobra.Command) (*Context, string, error) {
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

// tagBody builds a tag payload from --from-file, or from --title/--color.
func tagBody(cmd *cobra.Command, missingMsg string) (json.RawMessage, error) {
	fromFile, _ := cmd.Flags().GetString("from-file")
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
	title, _ := cmd.Flags().GetString("title")
	color, _ := cmd.Flags().GetString("color")
	body := map[string]string{}
	if title != "" {
		body["title"] = title
	}
	if color != "" {
		body["color"] = color
	}
	if len(body) == 0 {
		return nil, &UsageError{Msg: missingMsg}
	}
	return json.Marshal(body)
}
