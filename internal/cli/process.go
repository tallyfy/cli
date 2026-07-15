package cli

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/hooks"
	"github.com/tallyfy/cli/pkg/tallyfy"
)

func init() {
	register(func(root *cobra.Command) {
		cmd := &cobra.Command{
			Use:     "process",
			Aliases: []string{"runs"},
			Short:   "Manage processes (running instances of blueprints; API name: run)",
			Long: `Manage processes - live instances launched from a blueprint.

Tallyfy's UI calls these "processes"; the API calls them "runs". Launch one
process or many (from a CSV), inspect status, and archive when finished.`,
		}
		cmd.AddCommand(
			processListCmd(),
			processGetCmd(),
			processLaunchCmd(),
			processUpdateCmd(),
			processArchiveCmd(),
			processReactivateCmd(),
			processExportCmd(),
		)
		root.AddCommand(cmd)
	})
}

func processListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List processes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, org, err := processContext(cmd)
			if err != nil {
				return err
			}
			if err := ctx.Guard("Process", "list", "", hooks.Payload{}); err != nil {
				return err
			}
			all, _ := cmd.Flags().GetBool("all")
			limit, _ := cmd.Flags().GetInt("limit")
			status, _ := cmd.Flags().GetString("status")

			opts := tallyfy.ListOptions{All: all || limit > 0, Limit: limit}
			if limit > 0 {
				opts.PerPage = limit
			}
			if status != "" {
				opts.Extra = map[string]string{"status": status}
			}

			procs, _, err := ctx.API.ListProcesses(cmd.Context(), org, opts)
			if err != nil {
				return err
			}
			cols := []string{"ID", "NAME", "STATUS", "CREATED", "DUE"}
			rows := make([][]string, 0, len(procs))
			items := make([]any, 0, len(procs))
			for i := range procs {
				p := procs[i]
				rows = append(rows, []string{p.ID, truncate(p.Name, 50), p.Status, p.CreatedAt, deref(p.DueDate)})
				items = append(items, p)
			}
			return ctx.RenderList(cols, rows, items)
		},
	}
	cmd.Flags().Bool("all", false, "fetch every page (default: first page only)")
	cmd.Flags().Int("limit", 0, "maximum processes to return (0 = server default page)")
	cmd.Flags().String("status", "", "filter by status")
	return cmd
}

func processGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one process",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := processContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			if err := ctx.Guard("Process", "get", "", hooks.Payload{Resource: "process", ID: id}); err != nil {
				return err
			}
			withArg, _ := cmd.Flags().GetString("with")
			p, raw, err := ctx.API.GetProcess(cmd.Context(), org, id, l6SplitCSV(withArg))
			if err != nil {
				return err
			}
			cols := []string{"ID", "NAME", "STATUS", "CHECKLIST", "CREATED", "DUE"}
			row := []string{p.ID, p.Name, p.Status, p.ChecklistID, p.CreatedAt, deref(p.DueDate)}
			var item any = p
			if len(raw) > 0 {
				item = raw
			}
			return ctx.RenderItem(cols, row, item)
		},
	}
	cmd.Flags().String("with", "", "comma-separated relationships to include (e.g. tasks,checklist)")
	return cmd
}

func processLaunchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "launch",
		Short: "Launch one process, or many from a CSV",
		Long: `Launch a process from a blueprint.

Single:
  tallyfy process launch --blueprint <id> --name "Q3 onboarding" \
    --field start_date=2026-07-01 --field manager=jo@example.com

Bulk (one process per CSV row):
  tallyfy process launch --blueprint <id> --from-csv new-hires.csv

The CSV's header row names the kickoff fields; a "name" column (if present)
sets each process name and every other column is a kickoff field. Use
--dry-run to preview without launching.

Idempotency: launch has no dedupe key in v1 - re-running a CSV launches the
processes again. Guard against duplicates upstream (e.g. track launched rows).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, org, err := processContext(cmd)
			if err != nil {
				return err
			}
			blueprintID, _ := cmd.Flags().GetString("blueprint")
			if blueprintID == "" {
				return &UsageError{Msg: "--blueprint is required"}
			}
			if csvPath, _ := cmd.Flags().GetString("from-csv"); csvPath != "" {
				return processLaunchBulk(cmd, ctx, org, blueprintID, csvPath)
			}
			name, _ := cmd.Flags().GetString("name")
			fieldFlags, _ := cmd.Flags().GetStringArray("field")
			fields, err := parseKeyValues(fieldFlags)
			if err != nil {
				return err
			}
			return processLaunchSingle(cmd, ctx, org, blueprintID, name, fields)
		},
	}
	cmd.Flags().String("blueprint", "", "blueprint (checklist) ID to launch (required)")
	cmd.Flags().String("name", "", "process name")
	cmd.Flags().StringArray("field", nil, "kickoff field as key=value (repeatable)")
	cmd.Flags().String("from-csv", "", "launch one process per row of this CSV file")
	return cmd
}

func processUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a process",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := processContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			fromFile, _ := cmd.Flags().GetString("from-file")
			name, _ := cmd.Flags().GetString("name")
			payload, err := processUpdateBody(fromFile, name)
			if err != nil {
				return err
			}
			if err := ctx.Guard("Process", "update", "", hooks.Payload{Resource: "process", ID: id}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("PUT /organizations/%s/runs/%s %s", org, id, string(payload))
				return nil
			}
			p, err := ctx.API.UpdateProcess(cmd.Context(), org, id, payload)
			if err != nil {
				return err
			}
			return processRenderResult(ctx, p)
		},
	}
	cmd.Flags().String("name", "", "new process name")
	cmd.Flags().String("from-file", "", "read the full JSON body from a file (\"-\" for stdin)")
	return cmd
}

func processArchiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "archive <id>",
		Short: "Archive a process",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := processContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			payload := hooks.Payload{Resource: "process", ID: id}
			if err := ctx.Guard("Process", "archive", hooks.PreArchive, payload); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("DELETE /organizations/%s/runs/%s", org, id)
				return nil
			}
			if err := ctx.ConfirmDestructive(fmt.Sprintf("archive process %s", id)); err != nil {
				return err
			}
			if err := ctx.API.ArchiveProcess(cmd.Context(), org, id); err != nil {
				return err
			}
			ctx.FirePost(hooks.PostArchive, payload, "Process", "archive")
			ctx.Infof("archived process %s\n", id)
			return nil
		},
	}
}

func processReactivateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reactivate <id>",
		Short: "Reactivate an archived process",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := processContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			if err := ctx.Guard("Process", "reactivate", "", hooks.Payload{Resource: "process", ID: id}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("PUT /organizations/%s/runs/%s/activate", org, id)
				return nil
			}
			if err := ctx.API.ReactivateProcess(cmd.Context(), org, id); err != nil {
				return err
			}
			ctx.Infof("reactivated process %s\n", id)
			return nil
		},
	}
}

func processExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export <id>",
		Short: "Export a process to JSON",
		Long:  "Export a process (POST /runs/{id}/export) and write the raw JSON to stdout, or to --out.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, org, err := processContext(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			if err := ctx.Guard("Process", "export", "", hooks.Payload{Resource: "process", ID: id}); err != nil {
				return err
			}
			// resources.go exposes no process-export method; the endpoint is a
			// POST that returns JSON, so call it through the raw passthrough.
			path := "organizations/" + url.PathEscape(org) + "/runs/" + url.PathEscape(id) + "/export"
			_, body, err := ctx.API.Raw(cmd.Context(), http.MethodPost, path, nil, nil)
			if err != nil {
				return err
			}
			out, _ := cmd.Flags().GetString("out")
			return l6WritePrettyJSON(body, out)
		},
	}
	cmd.Flags().String("out", "", "write to this file instead of stdout")
	return cmd
}

// --- launch helpers ---------------------------------------------------------

func processLaunchSingle(cmd *cobra.Command, ctx *Context, org, blueprintID, name string, fields map[string]string) error {
	payload, err := processLaunchPayload(blueprintID, name, fields)
	if err != nil {
		return err
	}
	if err := ctx.Guard("Process", "launch", hooks.PreLaunch, hooks.Payload{Resource: "process", ID: blueprintID}); err != nil {
		return err
	}
	if ctx.DryRun {
		ctx.DryRunf("POST /organizations/%s/runs %s", org, string(payload))
		return nil
	}
	p, err := ctx.API.LaunchProcess(cmd.Context(), org, payload)
	if err != nil {
		return err
	}
	ctx.FirePost(hooks.PostLaunch, hooks.Payload{Resource: "process", ID: p.ID}, "Process", "launch")
	return processRenderResult(ctx, p)
}

func processLaunchBulk(cmd *cobra.Command, ctx *Context, org, blueprintID, csvPath string) error {
	data, err := readInput(csvPath)
	if err != nil {
		return &UsageError{Msg: err.Error()}
	}
	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		return &UsageError{Msg: "parse CSV: " + err.Error()}
	}
	if len(records) < 2 {
		return &UsageError{Msg: "CSV needs a header row and at least one data row"}
	}
	header := records[0]
	nameIdx := -1
	for i, h := range header {
		if strings.EqualFold(strings.TrimSpace(h), "name") {
			nameIdx = i
		}
	}
	dataRows := records[1:]

	if err := ctx.Guard("Process", "launch", hooks.PreLaunch, hooks.Payload{Resource: "process", ID: blueprintID}); err != nil {
		return err
	}

	if ctx.DryRun {
		for _, rec := range dataRows {
			name, fields := csvRowFields(header, rec, nameIdx)
			ctx.DryRunf("POST /organizations/%s/runs checklist_id=%s name=%q fields=%v", org, blueprintID, name, fields)
		}
		return nil
	}

	cols := []string{"ROW", "STATUS", "PROCESS/ERROR"}
	rows := make([][]string, 0, len(dataRows))
	items := make([]any, 0, len(dataRows))
	succeeded, failed := 0, 0
	for i, rec := range dataRows {
		rowNum := i + 2 // 1-based, accounting for the header row
		name, fields := csvRowFields(header, rec, nameIdx)
		payload, perr := processLaunchPayload(blueprintID, name, fields)
		if perr == nil {
			var p *tallyfy.Process
			p, perr = ctx.API.LaunchProcess(cmd.Context(), org, payload)
			if perr == nil {
				succeeded++
				rows = append(rows, []string{strconv.Itoa(rowNum), "ok", p.ID})
				items = append(items, map[string]any{"row": rowNum, "ok": true, "process": p.ID})
				continue
			}
		}
		failed++
		rows = append(rows, []string{strconv.Itoa(rowNum), "failed", perr.Error()})
		items = append(items, map[string]any{"row": rowNum, "ok": false, "error": perr.Error()})
		ctx.Infof("row %d failed: %s\n", rowNum, perr.Error())
	}
	if err := ctx.RenderList(cols, rows, items); err != nil {
		return err
	}
	ctx.Infof("launched %d, failed %d (of %d)\n", succeeded, failed, len(dataRows))
	if succeeded > 0 {
		ctx.FirePost(hooks.PostLaunch, hooks.Payload{Resource: "process", ID: blueprintID}, "Process", "launch")
	}
	if failed > 0 {
		return &BulkPartialError{Succeeded: succeeded, Failed: failed, Total: len(dataRows)}
	}
	return nil
}

// processLaunchPayload builds a createRunInput body: checklist_id (required),
// optional name, and kickoff fields under "prerun" as an id->value object.
func processLaunchPayload(blueprintID, name string, fields map[string]string) (json.RawMessage, error) {
	body := map[string]any{"checklist_id": blueprintID}
	if name != "" {
		body["name"] = name
	}
	if len(fields) > 0 {
		prerun := make(map[string]string, len(fields))
		for k, v := range fields {
			prerun[k] = v
		}
		body["prerun"] = prerun
	}
	return json.Marshal(body)
}

// csvRowFields splits one CSV record into the process name (from the "name"
// column, if any) and the remaining columns as kickoff fields.
func csvRowFields(header, rec []string, nameIdx int) (string, map[string]string) {
	name := ""
	fields := make(map[string]string)
	for i, h := range header {
		val := ""
		if i < len(rec) {
			val = rec[i]
		}
		if i == nameIdx {
			name = val
			continue
		}
		if key := strings.TrimSpace(h); key != "" {
			fields[key] = val
		}
	}
	return name, fields
}

// parseKeyValues parses repeated --field key=value flags into a map.
func parseKeyValues(pairs []string) (map[string]string, error) {
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, &UsageError{Msg: fmt.Sprintf("invalid --field %q (want key=value)", p)}
		}
		m[k] = v
	}
	return m, nil
}

// --- shared helpers ---------------------------------------------------------

// processContext builds an authenticated context and resolves the org for a
// process subcommand (every process verb is org-scoped and hits the API).
func processContext(cmd *cobra.Command) (*Context, string, error) {
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

// processUpdateBody builds an update payload from a file body or a --name.
func processUpdateBody(fromFile, name string) (json.RawMessage, error) {
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
	if name == "" {
		return nil, &UsageError{Msg: "provide --name or --from-file"}
	}
	return json.Marshal(map[string]string{"name": name})
}

// processRenderResult renders a single process after a mutation.
func processRenderResult(ctx *Context, p *tallyfy.Process) error {
	cols := []string{"ID", "NAME", "STATUS"}
	row := []string{p.ID, p.Name, p.Status}
	return ctx.RenderItem(cols, row, p)
}
