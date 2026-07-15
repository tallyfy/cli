package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/hooks"
	"github.com/tallyfy/cli/pkg/tallyfy"
)

// blueprintImportStripFields are the server-managed fields removed from an
// exported blueprint before it is re-created via `blueprint import`, so a
// snapshot round-trips cleanly into any organization (workflows-as-code).
var blueprintImportStripFields = []string{
	"id", "owner_id", "created_by", "alias", "steps_count",
	"industry_tags", "topic_tags", "folder_id", "kickoff_title",
	"kickoff_description", "started_processes", "created_at",
	"last_updated", "archived_at",
}

func init() {
	register(func(root *cobra.Command) {
		cmd := &cobra.Command{
			Use:     "blueprint",
			Aliases: []string{"checklist"},
			Short:   "Manage blueprints (workflow templates; API name: checklist)",
			Long: `Manage blueprints - reusable workflow templates.

Tallyfy's UI calls these "blueprints"; the API calls them "checklists". This
command accepts either name. Use export/import to keep blueprints in version
control and promote them across organizations.`,
		}
		cmd.AddCommand(
			blueprintListCmd(),
			blueprintGetCmd(),
			blueprintCreateCmd(),
			blueprintUpdateCmd(),
			blueprintDeleteCmd(),
			blueprintCloneCmd(),
			blueprintPublishCmd(),
			blueprintExportCmd(),
			blueprintImportCmd(),
			blueprintStepsCmd(),
			blueprintAutomationCmd(),
		)
		root.AddCommand(cmd)
	})
}

func blueprintListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List blueprints",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, org, err := blueprintContext(cmd)
			if err != nil {
				return err
			}
			if err := ctx.Guard("Blueprint", "list", "", hooks.Payload{}); err != nil {
				return err
			}

			all, _ := cmd.Flags().GetBool("all")
			limit, _ := cmd.Flags().GetInt("limit")
			folder, _ := cmd.Flags().GetString("folder")
			status, _ := cmd.Flags().GetString("status")

			opts := tallyfy.ListOptions{All: all || limit > 0, Limit: limit}
			if limit > 0 {
				opts.PerPage = limit
			}
			extra := map[string]string{}
			if folder != "" {
				extra["folder_id"] = folder
			}
			if status != "" {
				extra["status"] = status
			}
			if len(extra) > 0 {
				opts.Extra = extra
			}

			bps, _, err := ctx.API.ListBlueprints(cmd.Context(), org, opts)
			if err != nil {
				return err
			}
			cols := []string{"ID", "TITLE", "STATUS", "STEPS", "UPDATED"}
			rows := make([][]string, 0, len(bps))
			items := make([]any, 0, len(bps))
			for i := range bps {
				b := bps[i]
				rows = append(rows, []string{b.ID, truncate(b.Title, 50), b.Status, b.StepsCount.String(), b.LastUpdated})
				items = append(items, b)
			}
			return ctx.RenderList(cols, rows, items)
		},
	}
	cmd.Flags().Bool("all", false, "fetch every page (default: first page only)")
	cmd.Flags().Int("limit", 0, "maximum blueprints to return (0 = server default page)")
	cmd.Flags().String("folder", "", "filter by folder ID")
	cmd.Flags().String("status", "", "filter by status")
	return cmd
}

func blueprintGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one blueprint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := blueprintContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			if err := ctx.Guard("Blueprint", "get", "", hooks.Payload{Resource: "blueprint", ID: id}); err != nil {
				return err
			}
			withArg, _ := cmd.Flags().GetString("with")
			b, raw, err := ctx.API.GetBlueprint(cmd.Context(), org, id, l6SplitCSV(withArg))
			if err != nil {
				return err
			}
			cols := []string{"ID", "TITLE", "STATUS", "SUMMARY", "STEPS", "CREATED"}
			row := []string{b.ID, b.Title, b.Status, b.Summary, b.StepsCount.String(), b.CreatedAt}
			var item any = b
			if len(raw) > 0 {
				item = raw
			}
			return ctx.RenderItem(cols, row, item)
		},
	}
	cmd.Flags().String("with", "steps", "comma-separated relationships to include (e.g. steps,automated_actions)")
	return cmd
}

func blueprintCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a blueprint",
		Long:  "Create a blueprint from --title/--summary, or from a JSON body via --from-file (\"-\" reads stdin).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, org, err := blueprintContext(cmd)
			if err != nil {
				return err
			}
			fromFile, _ := cmd.Flags().GetString("from-file")
			title, _ := cmd.Flags().GetString("title")
			summary, _ := cmd.Flags().GetString("summary")

			payload, err := blueprintWriteBody(fromFile, title, summary, "--title is required (or use --from-file)")
			if err != nil {
				return err
			}
			if err := ctx.Guard("Blueprint", "create", "", hooks.Payload{Resource: "blueprint"}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("POST /organizations/%s/checklists %s", org, string(payload))
				return nil
			}
			b, raw, err := ctx.API.CreateBlueprint(cmd.Context(), org, payload)
			if err != nil {
				return err
			}
			return blueprintRenderResult(ctx, b, raw)
		},
	}
	cmd.Flags().String("title", "", "blueprint title")
	cmd.Flags().String("summary", "", "blueprint summary")
	cmd.Flags().String("from-file", "", "read the full JSON body from a file (\"-\" for stdin)")
	return cmd
}

func blueprintUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a blueprint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := blueprintContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			fromFile, _ := cmd.Flags().GetString("from-file")
			title, _ := cmd.Flags().GetString("title")
			summary, _ := cmd.Flags().GetString("summary")

			payload, err := blueprintWriteBody(fromFile, title, summary, "provide --title, --summary, or --from-file")
			if err != nil {
				return err
			}
			if err := ctx.Guard("Blueprint", "update", "", hooks.Payload{Resource: "blueprint", ID: id}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("PUT /organizations/%s/checklists/%s %s", org, id, string(payload))
				return nil
			}
			b, err := ctx.API.UpdateBlueprint(cmd.Context(), org, id, payload)
			if err != nil {
				return err
			}
			return blueprintRenderResult(ctx, b, b.Raw)
		},
	}
	cmd.Flags().String("title", "", "new title")
	cmd.Flags().String("summary", "", "new summary")
	cmd.Flags().String("from-file", "", "read the full JSON body from a file (\"-\" for stdin)")
	return cmd
}

func blueprintDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete (archive) a blueprint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := blueprintContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			payload := hooks.Payload{Resource: "blueprint", ID: id}
			if err := ctx.Guard("Blueprint", "delete", hooks.PreDelete, payload); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("DELETE /organizations/%s/checklists/%s", org, id)
				return nil
			}
			if err := ctx.ConfirmDestructive(fmt.Sprintf("delete blueprint %s", id)); err != nil {
				return err
			}
			if err := ctx.API.DeleteBlueprint(cmd.Context(), org, id); err != nil {
				return err
			}
			ctx.FirePost(hooks.PostDelete, payload, "Blueprint", "delete")
			ctx.Infof("deleted blueprint %s\n", id)
			return nil
		},
	}
	return cmd
}

func blueprintCloneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clone <id>",
		Short: "Clone a blueprint within the organization",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := blueprintContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			title, _ := cmd.Flags().GetString("title")
			if err := ctx.Guard("Blueprint", "clone", "", hooks.Payload{Resource: "blueprint", ID: id}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("POST /organizations/%s/checklists/%s/clone title=%q", org, id, title)
				return nil
			}
			b, err := ctx.API.CloneBlueprint(cmd.Context(), org, id, title)
			if err != nil {
				return err
			}
			return blueprintRenderResult(ctx, b, b.Raw)
		},
	}
	cmd.Flags().String("title", "", "title for the cloned blueprint (optional)")
	return cmd
}

func blueprintPublishCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "publish <id>",
		Short: "Publish a blueprint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := blueprintContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			if err := ctx.Guard("Blueprint", "publish", "", hooks.Payload{Resource: "blueprint", ID: id}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("PUT /organizations/%s/checklists/%s/publish", org, id)
				return nil
			}
			if err := ctx.API.PublishBlueprint(cmd.Context(), org, id); err != nil {
				return err
			}
			ctx.Infof("published blueprint %s\n", id)
			return nil
		},
	}
}

func blueprintExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export <id>",
		Short: "Export a blueprint to JSON (git-committable)",
		Long:  "Fetch a blueprint with its steps and automated actions and write the raw JSON to stdout, or to --out. This is the form consumed by `blueprint import`.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := blueprintContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			if err := ctx.Guard("Blueprint", "export", "", hooks.Payload{Resource: "blueprint", ID: id}); err != nil {
				return err
			}
			_, raw, err := ctx.API.GetBlueprint(cmd.Context(), org, id, []string{"steps", "automated_actions"})
			if err != nil {
				return err
			}
			out, _ := cmd.Flags().GetString("out")
			return l6WritePrettyJSON(raw, out)
		},
	}
	cmd.Flags().String("out", "", "write to this file instead of stdout")
	return cmd
}

func blueprintImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <file>",
		Short: "Import a blueprint from an exported JSON file",
		Long: `Create a blueprint from a JSON file produced by "blueprint export" ("-" reads stdin).

Server-managed fields (id, owner_id, created_by, alias, steps_count,
industry_tags, topic_tags, folder_id, kickoff_title, kickoff_description,
started_processes, created_at, last_updated, archived_at) are stripped before
POST so the snapshot re-creates cleanly. Combine with --org to promote a
blueprint into another organization (workflows-as-code).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := blueprintContext(cmd)
			if err != nil {
				return err
			}
			data, err := readInput(args[0])
			if err != nil {
				return &UsageError{Msg: err.Error()}
			}
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(data, &obj); err != nil {
				return &UsageError{Msg: "import file must be a JSON object: " + err.Error()}
			}
			stripped := make([]string, 0, len(blueprintImportStripFields))
			for _, f := range blueprintImportStripFields {
				if _, ok := obj[f]; ok {
					delete(obj, f)
					stripped = append(stripped, f)
				}
			}
			payload, err := json.Marshal(obj)
			if err != nil {
				return err
			}
			evtPayload := hooks.Payload{Resource: "blueprint"}
			if err := ctx.Guard("Blueprint", "import", hooks.PreImport, evtPayload); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("POST /organizations/%s/checklists (import from %s)", org, args[0])
				if len(stripped) == 0 {
					ctx.Printf("stripped system fields: (none present)\n")
				} else {
					ctx.Printf("stripped system fields: %s\n", strings.Join(stripped, ", "))
				}
				return nil
			}
			b, raw, err := ctx.API.CreateBlueprint(cmd.Context(), org, payload)
			if err != nil {
				return err
			}
			evtPayload.ID = b.ID
			ctx.FirePost(hooks.PostImport, evtPayload, "Blueprint", "import")
			return blueprintRenderResult(ctx, b, raw)
		},
	}
}

func blueprintStepsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "steps <id>",
		Short: "List a blueprint's steps",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := blueprintContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			if err := ctx.Guard("Blueprint", "steps", "", hooks.Payload{Resource: "blueprint", ID: id}); err != nil {
				return err
			}
			raw, err := ctx.API.GetBlueprintSteps(cmd.Context(), org, id)
			if err != nil {
				return err
			}
			var steps []json.RawMessage
			if err := json.Unmarshal(raw, &steps); err != nil {
				return fmt.Errorf("unexpected steps payload (not a JSON array): %w", err)
			}
			cols := []string{"POSITION", "ID", "TITLE", "TYPE"}
			rows := make([][]string, 0, len(steps))
			items := make([]any, 0, len(steps))
			for i := range steps {
				var s struct {
					Position json.Number `json:"position"`
					ID       string      `json:"id"`
					Title    string      `json:"title"`
					StepType string      `json:"step_type"`
				}
				_ = json.Unmarshal(steps[i], &s)
				rows = append(rows, []string{s.Position.String(), s.ID, truncate(s.Title, 50), s.StepType})
				items = append(items, steps[i])
			}
			return ctx.RenderList(cols, rows, items)
		},
	}
}

func blueprintAutomationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "automation",
		Aliases: []string{"automations", "rule"},
		Short:   "Manage a blueprint's automated actions (rules)",
	}
	cmd.AddCommand(
		blueprintAutomationListCmd(),
		blueprintAutomationCreateCmd(),
		blueprintAutomationUpdateCmd(),
		blueprintAutomationDeleteCmd(),
	)
	return cmd
}

func blueprintAutomationListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <blueprint-id>",
		Short: "List automated actions on a blueprint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := blueprintContext(cmd)
			if err != nil {
				return err
			}
			bpID := args[0]
			if err := ctx.Guard("Blueprint", "automation", "", hooks.Payload{Resource: "blueprint", ID: bpID}); err != nil {
				return err
			}
			raw, err := ctx.API.ListAutomations(cmd.Context(), org, bpID)
			if err != nil {
				return err
			}
			var actions []json.RawMessage
			if err := json.Unmarshal(raw, &actions); err != nil {
				return fmt.Errorf("unexpected automations payload (not a JSON array): %w", err)
			}
			cols := []string{"ID", "ALIAS"}
			rows := make([][]string, 0, len(actions))
			items := make([]any, 0, len(actions))
			for i := range actions {
				var a struct {
					ID    string `json:"id"`
					Alias string `json:"automated_alias"`
				}
				_ = json.Unmarshal(actions[i], &a)
				rows = append(rows, []string{a.ID, a.Alias})
				items = append(items, actions[i])
			}
			return ctx.RenderList(cols, rows, items)
		},
	}
}

func blueprintAutomationCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <blueprint-id>",
		Short: "Create an automated action from a JSON body (--from-file)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := blueprintContext(cmd)
			if err != nil {
				return err
			}
			bpID := args[0]
			payload, err := blueprintRequireFile(cmd)
			if err != nil {
				return err
			}
			if err := ctx.Guard("Blueprint", "automation", "", hooks.Payload{Resource: "blueprint", ID: bpID}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("POST /organizations/%s/checklists/%s/automated-actions %s", org, bpID, string(payload))
				return nil
			}
			raw, err := ctx.API.CreateAutomation(cmd.Context(), org, bpID, payload)
			if err != nil {
				return err
			}
			return l6WritePrettyJSON(raw, "")
		},
	}
	cmd.Flags().String("from-file", "", "read the JSON body from a file (\"-\" for stdin)")
	return cmd
}

func blueprintAutomationUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <blueprint-id> <automation-id>",
		Short: "Update an automated action from a JSON body (--from-file)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := blueprintContext(cmd)
			if err != nil {
				return err
			}
			bpID, autoID := args[0], args[1]
			payload, err := blueprintRequireFile(cmd)
			if err != nil {
				return err
			}
			if err := ctx.Guard("Blueprint", "automation", "", hooks.Payload{Resource: "blueprint", ID: bpID}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("PUT /organizations/%s/checklists/%s/automated-actions/%s %s", org, bpID, autoID, string(payload))
				return nil
			}
			raw, err := ctx.API.UpdateAutomation(cmd.Context(), org, bpID, autoID, payload)
			if err != nil {
				return err
			}
			return l6WritePrettyJSON(raw, "")
		},
	}
	cmd.Flags().String("from-file", "", "read the JSON body from a file (\"-\" for stdin)")
	return cmd
}

func blueprintAutomationDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <blueprint-id> <automation-id>",
		Short: "Delete an automated action",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := blueprintContext(cmd)
			if err != nil {
				return err
			}
			bpID, autoID := args[0], args[1]
			if err := ctx.Guard("Blueprint", "automation", "", hooks.Payload{Resource: "blueprint", ID: bpID}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("DELETE /organizations/%s/checklists/%s/automated-actions/%s", org, bpID, autoID)
				return nil
			}
			if err := ctx.API.DeleteAutomation(cmd.Context(), org, bpID, autoID); err != nil {
				return err
			}
			ctx.Infof("deleted automated action %s\n", autoID)
			return nil
		},
	}
}

// --- shared helpers ---------------------------------------------------------

// blueprintContext builds an authenticated context and resolves the org for a
// blueprint subcommand (every blueprint verb is org-scoped and hits the API).
func blueprintContext(cmd *cobra.Command) (*Context, string, error) {
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

// blueprintWriteBody builds a create/update payload: the raw file body when
// --from-file is set, otherwise a {title,summary} object. missingMsg is the
// usage error when neither a file nor any field is supplied.
func blueprintWriteBody(fromFile, title, summary, missingMsg string) (json.RawMessage, error) {
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
	if title != "" {
		body["title"] = title
	}
	if summary != "" {
		body["summary"] = summary
	}
	if len(body) == 0 {
		return nil, &UsageError{Msg: missingMsg}
	}
	return json.Marshal(body)
}

// blueprintRequireFile reads a mandatory --from-file JSON body.
func blueprintRequireFile(cmd *cobra.Command) (json.RawMessage, error) {
	fromFile, _ := cmd.Flags().GetString("from-file")
	if fromFile == "" {
		return nil, &UsageError{Msg: "--from-file is required"}
	}
	data, err := readInput(fromFile)
	if err != nil {
		return nil, &UsageError{Msg: err.Error()}
	}
	if !json.Valid(data) {
		return nil, &UsageError{Msg: "--from-file is not valid JSON"}
	}
	return data, nil
}

// blueprintRenderResult renders a single blueprint after a mutation, dumping
// the raw payload in json mode when available.
func blueprintRenderResult(ctx *Context, b *tallyfy.Blueprint, raw json.RawMessage) error {
	cols := []string{"ID", "TITLE", "STATUS"}
	row := []string{b.ID, b.Title, b.Status}
	var item any = b
	if len(raw) > 0 {
		item = raw
	}
	return ctx.RenderItem(cols, row, item)
}

// l6SplitCSV splits a comma-separated flag value into trimmed, non-empty parts.
func l6SplitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// l6WritePrettyJSON pretty-prints raw JSON to outPath (0600), or to Stdout when
// outPath is empty. Invalid JSON is written through unchanged.
func l6WritePrettyJSON(raw json.RawMessage, outPath string) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		buf.Reset()
		buf.Write(raw)
	}
	buf.WriteByte('\n')
	if outPath == "" {
		_, err := Stdout.Write(buf.Bytes())
		return err
	}
	return os.WriteFile(outPath, buf.Bytes(), 0o600) //nolint:gosec // user-chosen export path is intentional
}
