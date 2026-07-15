package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/hooks"
	"github.com/tallyfy/cli/pkg/tallyfy"
)

func init() {
	register(func(root *cobra.Command) {
		root.AddCommand(newTaskCmd())
	})
}

// taskListColumns is the column set for task lists and the single-task views.
var taskListColumns = []string{"ID", "TITLE", "STATUS", "DEADLINE", "TYPE"}

func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Work with tasks: list, complete, reopen, reassign, comment, and wait",
		Long: `Work with Tallyfy tasks.

Tasks live inside a process (run) or stand alone as one-off tasks. The
` + "`task wait`" + ` subcommand blocks until a named task completes and is the
building block for CI/CD approval gates.`,
	}
	cmd.AddCommand(
		newTaskListCmd(),
		newTaskGetCmd(),
		newTaskCompleteCmd(),
		newTaskReopenCmd(),
		newTaskReassignCmd(),
		newTaskCommentCmd(),
		newTaskWaitCmd(),
	)
	return cmd
}

func newTaskListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks (org-wide, in a process, yours, or a user's)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := NewContext(cmd, true)
			if err != nil {
				return err
			}
			org, err := ctx.RequireOrg()
			if err != nil {
				return err
			}

			process, _ := cmd.Flags().GetString("process")
			mine, _ := cmd.Flags().GetBool("mine")
			user, _ := cmd.Flags().GetString("user")
			all, _ := cmd.Flags().GetBool("all")
			limit, _ := cmd.Flags().GetInt("limit")

			sources := 0
			for _, on := range []bool{process != "", mine, user != ""} {
				if on {
					sources++
				}
			}
			if sources > 1 {
				return &UsageError{Msg: "--process, --mine, and --user are mutually exclusive"}
			}

			opts := tallyfy.ListOptions{All: all, Limit: limit}
			var tasks []tallyfy.Task
			switch {
			case process != "":
				tasks, _, err = ctx.API.ListRunTasks(cmd.Context(), org, process, opts)
			case mine:
				tasks, _, err = ctx.API.ListMyTasks(cmd.Context(), org, opts)
			case user != "":
				opts.Extra = map[string]string{"owners": user}
				tasks, _, err = ctx.API.ListOrgTasks(cmd.Context(), org, opts)
			default:
				tasks, _, err = ctx.API.ListOrgTasks(cmd.Context(), org, opts)
			}
			if err != nil {
				return err
			}
			return ctx.RenderList(taskListColumns, taskRows(tasks), taskItems(tasks))
		},
	}
	cmd.Flags().String("process", "", "list tasks in this process (run) ID")
	cmd.Flags().Bool("mine", false, "list only tasks assigned to you")
	cmd.Flags().String("user", "", "list tasks owned by this user ID")
	cmd.Flags().Bool("all", false, "fetch every page (not just the first)")
	cmd.Flags().Int("limit", 0, "maximum number of tasks to return (0 = no limit)")
	return cmd
}

func newTaskGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <taskID>",
		Short: "Show one task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := NewContext(cmd, true)
			if err != nil {
				return err
			}
			org, err := ctx.RequireOrg()
			if err != nil {
				return err
			}
			taskID := args[0]
			process, _ := cmd.Flags().GetString("process")

			var (
				task *tallyfy.Task
				raw  json.RawMessage
			)
			if process != "" {
				task, raw, err = ctx.API.GetRunTask(cmd.Context(), org, process, taskID)
			} else {
				task, raw, err = ctx.API.GetOrgTask(cmd.Context(), org, taskID)
			}
			if err != nil {
				return err
			}
			return ctx.RenderItem(taskListColumns, taskRow(*task), rawItem(raw))
		},
	}
	cmd.Flags().String("process", "", "process (run) ID; uses the in-run task endpoint")
	return cmd
}

func newTaskCompleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "complete <taskID>",
		Short: "Mark a task complete",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := NewContext(cmd, true)
			if err != nil {
				return err
			}
			org, err := ctx.RequireOrg()
			if err != nil {
				return err
			}
			taskID := args[0]
			process, _ := cmd.Flags().GetString("process")

			payload := hooks.Payload{Resource: "task", ID: taskID}
			if err := ctx.Guard("Task", "complete", hooks.PreComplete, payload); err != nil {
				return err
			}
			if ctx.DryRun {
				if process != "" {
					ctx.DryRunf("POST /organizations/%s/runs/%s/completed-tasks {task_id:%s}", org, process, taskID)
				} else {
					ctx.DryRunf("POST /organizations/%s/completed-tasks {task_id:%s}", org, taskID)
				}
				return nil
			}

			var task *tallyfy.Task
			if process != "" {
				task, err = ctx.API.CompleteTask(cmd.Context(), org, process, taskID)
			} else {
				task, err = ctx.API.CompleteOrgTask(cmd.Context(), org, taskID)
			}
			if err != nil {
				return err
			}
			ctx.FirePost(hooks.PostComplete, payload, "Task", "complete")
			return ctx.RenderItem(taskListColumns, taskRow(*task), task)
		},
	}
	cmd.Flags().String("process", "", "process (run) ID; completes an in-run task")
	return cmd
}

func newTaskReopenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reopen <taskID>",
		Short: "Reopen (un-complete) a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := NewContext(cmd, true)
			if err != nil {
				return err
			}
			org, err := ctx.RequireOrg()
			if err != nil {
				return err
			}
			taskID := args[0]
			process, _ := cmd.Flags().GetString("process")

			if err := ctx.Guard("Task", "reopen", "", hooks.Payload{Resource: "task", ID: taskID}); err != nil {
				return err
			}
			if ctx.DryRun {
				if process != "" {
					ctx.DryRunf("DELETE /organizations/%s/runs/%s/completed-tasks/%s", org, process, taskID)
				} else {
					ctx.DryRunf("DELETE /organizations/%s/completed-tasks/%s", org, taskID)
				}
				return nil
			}

			if process != "" {
				err = ctx.API.ReopenTask(cmd.Context(), org, process, taskID)
			} else {
				err = ctx.API.ReopenOrgTask(cmd.Context(), org, taskID)
			}
			if err != nil {
				return err
			}
			return ctx.RenderItem([]string{"RESULT", "ID"}, []string{"reopened", taskID},
				map[string]string{"result": "reopened", "id": taskID})
		},
	}
	cmd.Flags().String("process", "", "process (run) ID; reopens an in-run task")
	return cmd
}

// taskOwners is the assignment shape sent to the run-task update endpoint.
// Verified against the Swagger stepOwner definition: users are integer IDs,
// guests are email strings, groups are group ID strings.
type taskOwners struct {
	Users  []int    `json:"users"`
	Guests []string `json:"guests"`
	Groups []string `json:"groups"`
}

// taskOwnersBody wraps taskOwners under the "owners" key expected by
// PUT /organizations/{org}/runs/{run}/tasks/{task}.
type taskOwnersBody struct {
	Owners taskOwners `json:"owners"`
}

func newTaskReassignCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reassign <taskID> --to <userIDOrEmail>",
		Short: "Reassign a task to a user (numeric ID) or a guest (email)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := NewContext(cmd, true)
			if err != nil {
				return err
			}
			org, err := ctx.RequireOrg()
			if err != nil {
				return err
			}
			taskID := args[0]
			process, _ := cmd.Flags().GetString("process")
			to, _ := cmd.Flags().GetString("to")

			to = strings.TrimSpace(to)
			if to == "" {
				return &UsageError{Msg: "task reassign requires --to <userIDOrEmail>"}
			}
			// The only run-task-owners write endpoint is the in-run task update,
			// so reassign requires a process context.
			if process == "" {
				return &UsageError{Msg: "task reassign requires --process <runID>"}
			}

			owners := taskOwners{Users: []int{}, Guests: []string{}, Groups: []string{}}
			if strings.Contains(to, "@") {
				owners.Guests = append(owners.Guests, to)
			} else {
				uid, convErr := strconv.Atoi(to)
				if convErr != nil {
					return &UsageError{Msg: fmt.Sprintf("--to must be a numeric user ID or an email address (got %q)", to)}
				}
				owners.Users = append(owners.Users, uid)
			}
			payload, err := json.Marshal(taskOwnersBody{Owners: owners})
			if err != nil {
				return err
			}

			if err := ctx.Guard("Task", "reassign", "", hooks.Payload{Resource: "task", ID: taskID}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("PUT /organizations/%s/runs/%s/tasks/%s %s", org, process, taskID, string(payload))
				return nil
			}

			task, err := ctx.API.UpdateRunTask(cmd.Context(), org, process, taskID, payload)
			if err != nil {
				return err
			}
			return ctx.RenderItem(taskListColumns, taskRow(*task), task)
		},
	}
	cmd.Flags().String("process", "", "process (run) ID (required)")
	cmd.Flags().String("to", "", "user ID (numeric) or guest email to assign the task to")
	return cmd
}

func newTaskCommentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment <taskID> --text <body>",
		Short: "Post a comment on a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := NewContext(cmd, true)
			if err != nil {
				return err
			}
			org, err := ctx.RequireOrg()
			if err != nil {
				return err
			}
			taskID := args[0]
			text, _ := cmd.Flags().GetString("text")
			if strings.TrimSpace(text) == "" {
				return &UsageError{Msg: "task comment requires --text <body>"}
			}

			if err := ctx.Guard("Task", "comment", "", hooks.Payload{Resource: "task", ID: taskID}); err != nil {
				return err
			}
			if ctx.DryRun {
				ctx.DryRunf("POST /organizations/%s/tasks/%s/comment {content:%q}", org, taskID, text)
				return nil
			}

			raw, err := ctx.API.CommentTask(cmd.Context(), org, taskID, text)
			if err != nil {
				return err
			}
			var cm tallyfy.Comment
			_ = json.Unmarshal(raw, &cm)
			return ctx.RenderItem([]string{"ID", "CONTENT", "CREATED"},
				[]string{cm.ID, truncate(cm.Content, 60), cm.CreatedAt}, rawItem(raw))
		},
	}
	cmd.Flags().String("text", "", "comment body (required)")
	return cmd
}

func newTaskWaitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait --process <runID> --task <titleOrID>",
		Short: "Block until a task completes (the CI/CD approval gate)",
		Long: `Poll a process until the named task completes.

Exits 0 the moment the task is complete, or 1 when --timeout elapses first.
Designed for headless pipelines: it never prompts, writes progress to stderr,
and writes the final task to stdout. --task matches an exact task ID or an
exact task title (case-insensitive).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := NewContext(cmd, true)
			if err != nil {
				return err
			}
			org, err := ctx.RequireOrg()
			if err != nil {
				return err
			}

			process, _ := cmd.Flags().GetString("process")
			sel, _ := cmd.Flags().GetString("task")
			timeoutStr, _ := cmd.Flags().GetString("timeout")
			intervalStr, _ := cmd.Flags().GetString("interval")

			if process == "" {
				return &UsageError{Msg: "task wait requires --process <runID>"}
			}
			if strings.TrimSpace(sel) == "" {
				return &UsageError{Msg: "task wait requires --task <titleOrID>"}
			}
			timeout, err := time.ParseDuration(timeoutStr)
			if err != nil {
				return &UsageError{Msg: fmt.Sprintf("invalid --timeout %q: %v", timeoutStr, err)}
			}
			interval, err := time.ParseDuration(intervalStr)
			if err != nil {
				return &UsageError{Msg: fmt.Sprintf("invalid --interval %q: %v", intervalStr, err)}
			}
			if interval <= 0 {
				return &UsageError{Msg: "--interval must be a positive duration"}
			}

			// Permission-gate the wait (fires no lifecycle hook).
			if err := ctx.Guard("Task", "wait", "", hooks.Payload{Resource: "task", ID: sel}); err != nil {
				return err
			}

			goctx := cmd.Context()
			deadline := time.Now().Add(timeout)
			for {
				tasks, _, err := ctx.API.ListRunTasks(goctx, org, process, tallyfy.ListOptions{All: true})
				if err != nil {
					return err
				}
				task, found := findTask(tasks, sel)
				if found && taskIsComplete(task) {
					ctx.Infof("task %q is complete\n", sel)
					return ctx.RenderItem(taskListColumns, taskRow(task), task)
				}

				status := "not found"
				if found {
					status = task.Status
				}
				remaining := time.Until(deadline)
				if remaining <= 0 {
					return fmt.Errorf("timed out after %s waiting for task %q to complete (last status: %s)", timeout, sel, status)
				}
				ctx.Infof("waiting for task %q (status: %s); %s until timeout\n", sel, status, remaining.Round(time.Second))

				sleep := interval
				if remaining < sleep {
					sleep = remaining
				}
				select {
				case <-goctx.Done():
					return goctx.Err()
				case <-time.After(sleep):
				}
			}
		},
	}
	cmd.Flags().String("process", "", "process (run) ID to poll (required)")
	cmd.Flags().String("task", "", "task ID or exact task title to wait for (required)")
	cmd.Flags().String("timeout", "1h", "give up after this duration (Go duration, e.g. 30m, 2h)")
	cmd.Flags().String("interval", "15s", "poll interval (Go duration)")
	return cmd
}

// --- rendering + selection helpers (task-scoped names) ----------------------

func taskRow(t tallyfy.Task) []string {
	return []string{t.ID, t.Title, t.Status, deref(t.Deadline), t.TaskType}
}

func taskRows(ts []tallyfy.Task) [][]string {
	rows := make([][]string, 0, len(ts))
	for _, t := range ts {
		rows = append(rows, taskRow(t))
	}
	return rows
}

func taskItems(ts []tallyfy.Task) []any {
	items := make([]any, 0, len(ts))
	for _, t := range ts {
		items = append(items, t)
	}
	return items
}

// findTask returns the first task whose ID equals sel or whose title matches
// sel case-insensitively.
func findTask(tasks []tallyfy.Task, sel string) (tallyfy.Task, bool) {
	for _, t := range tasks {
		if t.ID == sel || strings.EqualFold(t.Title, sel) {
			return t, true
		}
	}
	return tallyfy.Task{}, false
}

// taskIsComplete reports whether a task has reached a completed state.
func taskIsComplete(t tallyfy.Task) bool {
	if t.CompletedAt != nil && strings.TrimSpace(*t.CompletedAt) != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(t.Status)) {
	case "completed", "complete":
		return true
	}
	return false
}

// rawItem returns raw for lossless -o json output, substituting an empty
// object when the API returned no body (an empty json.RawMessage is not valid
// JSON to encode).
func rawItem(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	return raw
}
