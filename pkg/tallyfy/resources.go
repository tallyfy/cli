package tallyfy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Every path below was verified against the live Swagger spec
// (temporary/tallyfy-cli-build/swagger/all-paths.txt) and, where Swagger was
// silent or wrong, against the api-v2 Laravel routes — divergences are noted
// on the method. Create/Update payloads are json.RawMessage passthrough: the
// caller builds the body, the API validates it.

// --- internal helpers -------------------------------------------------------

// orgPath joins "organizations/{org}/..." with each segment path-escaped
// (guest emails, arbitrary ids).
func orgPath(org string, segments ...string) string {
	var b strings.Builder
	b.WriteString("organizations/")
	b.WriteString(url.PathEscape(org))
	for _, s := range segments {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(s))
	}
	return b.String()
}

// withQuery builds ?with=a,b when includes are requested.
func withQuery(with []string) url.Values {
	if len(with) == 0 {
		return nil
	}
	q := url.Values{}
	q.Set("with", strings.Join(with, ","))
	return q
}

// itemEnvelope is the single-item response shape {"data": {...}}.
type itemEnvelope struct {
	Data json.RawMessage `json:"data"`
}

// doItem performs a request whose response is an item envelope, returning
// both the typed model and the raw "data" JSON.
func doItem[T any](ctx context.Context, c *Client, method, path string, query url.Values, body any) (*T, json.RawMessage, error) {
	var env itemEnvelope
	if _, err := c.Do(ctx, method, path, query, body, &env); err != nil {
		return nil, nil, err
	}
	out := new(T)
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return nil, nil, fmt.Errorf("decode %s %s data: %w", method, path, err)
		}
	}
	return out, env.Data, nil
}

// doData performs a request and returns the raw "data" member untyped.
func doData(ctx context.Context, c *Client, method, path string, query url.Values, body any) (json.RawMessage, error) {
	var env itemEnvelope
	if _, err := c.Do(ctx, method, path, query, body, &env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

// --- identity ---------------------------------------------------------------

// Me returns the authenticated user. GET /me
func (c *Client) Me(ctx context.Context) (*Me, error) {
	me, _, err := doItem[Me](ctx, c, http.MethodGet, "me", nil, nil)
	return me, err
}

// MyOrganizations lists organizations the user belongs to.
// GET /me/organizations
func (c *Client) MyOrganizations(ctx context.Context, o ListOptions) ([]Organization, *Pagination, error) {
	return CollectAll[Organization](ctx, c, "me/organizations", o)
}

// --- blueprints (API name: checklists) ---------------------------------------

// ListBlueprints lists workflow templates. GET /organizations/{org}/checklists
func (c *Client) ListBlueprints(ctx context.Context, org string, o ListOptions) ([]Blueprint, *Pagination, error) {
	return CollectAll[Blueprint](ctx, c, orgPath(org, "checklists"), o)
}

// GetBlueprint fetches one template, returning both the typed model and the
// raw "data" JSON (for export). GET /organizations/{org}/checklists/{id}
func (c *Client) GetBlueprint(ctx context.Context, org, id string, with []string) (*Blueprint, json.RawMessage, error) {
	bp, raw, err := doItem[Blueprint](ctx, c, http.MethodGet, orgPath(org, "checklists", id), withQuery(with), nil)
	if err != nil {
		return nil, nil, err
	}
	bp.Raw = raw
	return bp, raw, nil
}

// CreateBlueprint creates a template from a caller-built payload.
// POST /organizations/{org}/checklists
func (c *Client) CreateBlueprint(ctx context.Context, org string, payload json.RawMessage) (*Blueprint, json.RawMessage, error) {
	bp, raw, err := doItem[Blueprint](ctx, c, http.MethodPost, orgPath(org, "checklists"), nil, payload)
	if err != nil {
		return nil, nil, err
	}
	bp.Raw = raw
	return bp, raw, nil
}

// UpdateBlueprint updates a template. PUT /organizations/{org}/checklists/{id}
func (c *Client) UpdateBlueprint(ctx context.Context, org, id string, payload json.RawMessage) (*Blueprint, error) {
	bp, raw, err := doItem[Blueprint](ctx, c, http.MethodPut, orgPath(org, "checklists", id), nil, payload)
	if err != nil {
		return nil, err
	}
	bp.Raw = raw
	return bp, nil
}

// DeleteBlueprint archives a template. DELETE /organizations/{org}/checklists/{id}
func (c *Client) DeleteBlueprint(ctx context.Context, org, id string) error {
	_, err := c.Do(ctx, http.MethodDelete, orgPath(org, "checklists", id), nil, nil, nil)
	return err
}

// CloneBlueprint duplicates a template within org, optionally retitled.
// POST /organizations/{org}/checklists/{id}/clone
//
// Swagger declares formData fields tenant (required) + title (optional); the
// api-v2 controller reads both from any input source and defaults tenant to
// the path org. Sent as JSON: {"tenant": org, "title": title?}.
func (c *Client) CloneBlueprint(ctx context.Context, org, id string, title string) (*Blueprint, error) {
	body := struct {
		Tenant string `json:"tenant"`
		Title  string `json:"title,omitempty"`
	}{Tenant: org, Title: title}
	bp, raw, err := doItem[Blueprint](ctx, c, http.MethodPost, orgPath(org, "checklists", id, "clone"), nil, body)
	if err != nil {
		return nil, err
	}
	bp.Raw = raw
	return bp, nil
}

// PublishBlueprint publishes a template (no request body per Swagger).
// PUT /organizations/{org}/checklists/{id}/publish
func (c *Client) PublishBlueprint(ctx context.Context, org, id string) error {
	_, err := c.Do(ctx, http.MethodPut, orgPath(org, "checklists", id, "publish"), nil, nil, nil)
	return err
}

// GetBlueprintSteps returns a template's steps as raw JSON.
// GET /organizations/{org}/checklists/{id}/steps
func (c *Client) GetBlueprintSteps(ctx context.Context, org, id string) (json.RawMessage, error) {
	return doData(ctx, c, http.MethodGet, orgPath(org, "checklists", id, "steps"), nil, nil)
}

// GetKickoffFields returns a template's kick-off form fields.
//
// There is no dedicated route for them: ChecklistTransformer always inlines
// "prerun" (it is a plain transform key, not a ?with= include), so this
// fetches the checklist and extracts that array.
// GET /organizations/{org}/checklists/{id}
func (c *Client) GetKickoffFields(ctx context.Context, org, blueprintID string) ([]KickoffField, error) {
	var env struct {
		Data struct {
			Prerun []KickoffField `json:"prerun"`
		} `json:"data"`
	}
	if _, err := c.Do(ctx, http.MethodGet, orgPath(org, "checklists", blueprintID), nil, nil, &env); err != nil {
		return nil, err
	}
	return env.Data.Prerun, nil
}

// --- automations (automated-actions) -----------------------------------------

// ListAutomations returns a template's automated actions as raw JSON.
//
// The API has no GET route for automated-actions (Swagger + api-v2 routes:
// store/update/destroy only); the checklist transformer always inlines
// "automated_actions", so this fetches the checklist and extracts that field.
// GET /organizations/{org}/checklists/{checklist_id}
func (c *Client) ListAutomations(ctx context.Context, org, checklistID string) (json.RawMessage, error) {
	var env struct {
		Data struct {
			AutomatedActions json.RawMessage `json:"automated_actions"`
		} `json:"data"`
	}
	if _, err := c.Do(ctx, http.MethodGet, orgPath(org, "checklists", checklistID), nil, nil, &env); err != nil {
		return nil, err
	}
	if len(env.Data.AutomatedActions) == 0 {
		return json.RawMessage("[]"), nil
	}
	return env.Data.AutomatedActions, nil
}

// CreateAutomation adds an automated action to a template.
// POST /organizations/{org}/checklists/{checklist_id}/automated-actions
func (c *Client) CreateAutomation(ctx context.Context, org, checklistID string, payload json.RawMessage) (json.RawMessage, error) {
	return doData(ctx, c, http.MethodPost, orgPath(org, "checklists", checklistID, "automated-actions"), nil, payload)
}

// UpdateAutomation updates an automated action.
// PUT /organizations/{org}/checklists/{checklist_id}/automated-actions/{automated_id}
func (c *Client) UpdateAutomation(ctx context.Context, org, checklistID, automationID string, payload json.RawMessage) (json.RawMessage, error) {
	return doData(ctx, c, http.MethodPut, orgPath(org, "checklists", checklistID, "automated-actions", automationID), nil, payload)
}

// DeleteAutomation removes an automated action.
// DELETE /organizations/{org}/checklists/{checklist_id}/automated-actions/{automated_id}
func (c *Client) DeleteAutomation(ctx context.Context, org, checklistID, automationID string) error {
	_, err := c.Do(ctx, http.MethodDelete, orgPath(org, "checklists", checklistID, "automated-actions", automationID), nil, nil, nil)
	return err
}

// --- processes (API name: runs) ----------------------------------------------

// ListProcesses lists launched processes. GET /organizations/{org}/runs
func (c *Client) ListProcesses(ctx context.Context, org string, o ListOptions) ([]Process, *Pagination, error) {
	return CollectAll[Process](ctx, c, orgPath(org, "runs"), o)
}

// GetProcess fetches one process plus its raw "data" JSON.
// GET /organizations/{org}/runs/{id}
func (c *Client) GetProcess(ctx context.Context, org, id string, with []string) (*Process, json.RawMessage, error) {
	return doItem[Process](ctx, c, http.MethodGet, orgPath(org, "runs", id), withQuery(with), nil)
}

// LaunchProcess starts a process from a caller-built payload (checklist_id,
// name, tasks/preruns...). POST /organizations/{org}/runs
func (c *Client) LaunchProcess(ctx context.Context, org string, payload json.RawMessage) (*Process, error) {
	p, _, err := doItem[Process](ctx, c, http.MethodPost, orgPath(org, "runs"), nil, payload)
	return p, err
}

// UpdateProcess updates a process. PUT /organizations/{org}/runs/{id}
func (c *Client) UpdateProcess(ctx context.Context, org, id string, payload json.RawMessage) (*Process, error) {
	p, _, err := doItem[Process](ctx, c, http.MethodPut, orgPath(org, "runs", id), nil, payload)
	return p, err
}

// ArchiveProcess archives a process. DELETE /organizations/{org}/runs/{id}
func (c *Client) ArchiveProcess(ctx context.Context, org, id string) error {
	_, err := c.Do(ctx, http.MethodDelete, orgPath(org, "runs", id), nil, nil, nil)
	return err
}

// ReactivateProcess restores an archived process.
// PUT /organizations/{org}/runs/{id}/activate (verified PUT in Swagger)
func (c *Client) ReactivateProcess(ctx context.Context, org, id string) error {
	_, err := c.Do(ctx, http.MethodPut, orgPath(org, "runs", id, "activate"), nil, nil, nil)
	return err
}

// --- tasks --------------------------------------------------------------------

// ListRunTasks lists a process's tasks. GET /organizations/{org}/runs/{run}/tasks
func (c *Client) ListRunTasks(ctx context.Context, org, runID string, o ListOptions) ([]Task, *Pagination, error) {
	return CollectAll[Task](ctx, c, orgPath(org, "runs", runID, "tasks"), o)
}

// ListOrgTasks lists tasks across the org. GET /organizations/{org}/tasks
func (c *Client) ListOrgTasks(ctx context.Context, org string, o ListOptions) ([]Task, *Pagination, error) {
	return CollectAll[Task](ctx, c, orgPath(org, "tasks"), o)
}

// ListMyTasks lists the caller's tasks. GET /organizations/{org}/me/tasks
//
// Absent from the Swagger spec but present in api-v2 routes/api.php
// (UserTasksController@index) — verified against the API source.
func (c *Client) ListMyTasks(ctx context.Context, org string, o ListOptions) ([]Task, *Pagination, error) {
	return CollectAll[Task](ctx, c, orgPath(org, "me", "tasks"), o)
}

// GetRunTask fetches one process task plus raw JSON.
// GET /organizations/{org}/runs/{run}/tasks/{id}
func (c *Client) GetRunTask(ctx context.Context, org, runID, taskID string) (*Task, json.RawMessage, error) {
	return doItem[Task](ctx, c, http.MethodGet, orgPath(org, "runs", runID, "tasks", taskID), nil, nil)
}

// GetOrgTask fetches one task (incl. one-off tasks) plus raw JSON.
// GET /organizations/{org}/tasks/{id}
func (c *Client) GetOrgTask(ctx context.Context, org, taskID string) (*Task, json.RawMessage, error) {
	return doItem[Task](ctx, c, http.MethodGet, orgPath(org, "tasks", taskID), nil, nil)
}

// UpdateRunTask updates a process task (deadline, assignees, form values...).
// PUT /organizations/{org}/runs/{run}/tasks/{id}
func (c *Client) UpdateRunTask(ctx context.Context, org, runID, taskID string, payload json.RawMessage) (*Task, error) {
	t, _, err := doItem[Task](ctx, c, http.MethodPut, orgPath(org, "runs", runID, "tasks", taskID), nil, payload)
	return t, err
}

// completeBody is the completed-tasks request payload (Swagger: task_id
// string; optional is_approved / override_user are not exposed here).
type completeBody struct {
	TaskID string `json:"task_id"`
}

// CompleteTask marks a process task complete.
// POST /organizations/{org}/runs/{run}/completed-tasks with {"task_id": id}
func (c *Client) CompleteTask(ctx context.Context, org, runID, taskID string) (*Task, error) {
	t, _, err := doItem[Task](ctx, c, http.MethodPost, orgPath(org, "runs", runID, "completed-tasks"), nil, completeBody{TaskID: taskID})
	return t, err
}

// ReopenTask un-completes a process task.
// DELETE /organizations/{org}/runs/{run}/completed-tasks/{task}
func (c *Client) ReopenTask(ctx context.Context, org, runID, taskID string) error {
	_, err := c.Do(ctx, http.MethodDelete, orgPath(org, "runs", runID, "completed-tasks", taskID), nil, nil, nil)
	return err
}

// CompleteOrgTask marks a standalone (one-off) task complete.
// POST /organizations/{org}/completed-tasks with {"task_id": id}
func (c *Client) CompleteOrgTask(ctx context.Context, org, taskID string) (*Task, error) {
	t, _, err := doItem[Task](ctx, c, http.MethodPost, orgPath(org, "completed-tasks"), nil, completeBody{TaskID: taskID})
	return t, err
}

// ReopenOrgTask un-completes a standalone task.
// DELETE /organizations/{org}/completed-tasks/{id}
func (c *Client) ReopenOrgTask(ctx context.Context, org, taskID string) error {
	_, err := c.Do(ctx, http.MethodDelete, orgPath(org, "completed-tasks", taskID), nil, nil, nil)
	return err
}

// CommentTask posts a comment on a task. Body field verified via Swagger
// threadInput: {"content": "..."} (optional state/label not exposed here).
// POST /organizations/{org}/tasks/{task}/comment
func (c *Client) CommentTask(ctx context.Context, org, taskID, content string) (json.RawMessage, error) {
	body := struct {
		Content string `json:"content"`
	}{Content: content}
	return doData(ctx, c, http.MethodPost, orgPath(org, "tasks", taskID, "comment"), nil, body)
}

// --- users --------------------------------------------------------------------

// ListUsers lists org members. GET /organizations/{org}/users
func (c *Client) ListUsers(ctx context.Context, org string, o ListOptions) ([]User, *Pagination, error) {
	return CollectAll[User](ctx, c, orgPath(org, "users"), o)
}

// InviteUser invites a member. Swagger inviteInput documents email,
// first_name, last_name; the payload passes through untouched so callers may
// send role etc. POST /organizations/{org}/users/invite
func (c *Client) InviteUser(ctx context.Context, org string, payload json.RawMessage) (json.RawMessage, error) {
	return doData(ctx, c, http.MethodPost, orgPath(org, "users", "invite"), nil, payload)
}

// SetUserRole changes a member's role. Body field verified in api-v2
// (UsersController@updateRole reads request('role')).
// PUT /organizations/{org}/users/{id}/role with {"role": role}
func (c *Client) SetUserRole(ctx context.Context, org, userID, role string) error {
	body := struct {
		Role string `json:"role"`
	}{Role: role}
	_, err := c.Do(ctx, http.MethodPut, orgPath(org, "users", userID, "role"), nil, body, nil)
	return err
}

// DisableUser deactivates a member.
// DELETE /organizations/{org}/users/{id}/disable — Swagger + api-v2 routes
// use DELETE here (not PUT/POST).
func (c *Client) DisableUser(ctx context.Context, org, userID string) error {
	_, err := c.Do(ctx, http.MethodDelete, orgPath(org, "users", userID, "disable"), nil, nil, nil)
	return err
}

// EnableUser reactivates a member. PUT /organizations/{org}/users/{id}/enable
func (c *Client) EnableUser(ctx context.Context, org, userID string) error {
	_, err := c.Do(ctx, http.MethodPut, orgPath(org, "users", userID, "enable"), nil, nil, nil)
	return err
}

// --- guests -------------------------------------------------------------------

// ListGuests lists external guests. GET /organizations/{org}/guests
func (c *Client) ListGuests(ctx context.Context, org string, o ListOptions) ([]Guest, *Pagination, error) {
	return CollectAll[Guest](ctx, c, orgPath(org, "guests"), o)
}

// GetGuest fetches a guest by email. GET /organizations/{org}/guests/{email}
func (c *Client) GetGuest(ctx context.Context, org, email string) (*Guest, error) {
	g, _, err := doItem[Guest](ctx, c, http.MethodGet, orgPath(org, "guests", email), nil, nil)
	return g, err
}

// CreateGuest creates a guest. POST /organizations/{org}/guests
func (c *Client) CreateGuest(ctx context.Context, org string, payload json.RawMessage) (*Guest, error) {
	g, _, err := doItem[Guest](ctx, c, http.MethodPost, orgPath(org, "guests"), nil, payload)
	return g, err
}

// UpdateGuest updates a guest by email. PUT /organizations/{org}/guests/{email}
func (c *Client) UpdateGuest(ctx context.Context, org, email string, payload json.RawMessage) (*Guest, error) {
	g, _, err := doItem[Guest](ctx, c, http.MethodPut, orgPath(org, "guests", email), nil, payload)
	return g, err
}

// --- groups -------------------------------------------------------------------

// ListGroups lists assignment groups. GET /organizations/{org}/groups
func (c *Client) ListGroups(ctx context.Context, org string, o ListOptions) ([]Group, *Pagination, error) {
	return CollectAll[Group](ctx, c, orgPath(org, "groups"), o)
}

// CreateGroup creates a group. POST /organizations/{org}/groups
func (c *Client) CreateGroup(ctx context.Context, org string, payload json.RawMessage) (*Group, error) {
	g, _, err := doItem[Group](ctx, c, http.MethodPost, orgPath(org, "groups"), nil, payload)
	return g, err
}

// UpdateGroup updates a group. PUT /organizations/{org}/groups/{id}
func (c *Client) UpdateGroup(ctx context.Context, org, id string, payload json.RawMessage) (*Group, error) {
	g, _, err := doItem[Group](ctx, c, http.MethodPut, orgPath(org, "groups", id), nil, payload)
	return g, err
}

// DeleteGroup removes a group. DELETE /organizations/{org}/groups/{id}
func (c *Client) DeleteGroup(ctx context.Context, org, id string) error {
	_, err := c.Do(ctx, http.MethodDelete, orgPath(org, "groups", id), nil, nil, nil)
	return err
}

// --- folders ------------------------------------------------------------------

// ListFolders lists folders. GET /organizations/{org}/folders
func (c *Client) ListFolders(ctx context.Context, org string, o ListOptions) ([]Folder, *Pagination, error) {
	return CollectAll[Folder](ctx, c, orgPath(org, "folders"), o)
}

// CreateFolder creates a folder. POST /organizations/{org}/folders
func (c *Client) CreateFolder(ctx context.Context, org string, payload json.RawMessage) (*Folder, error) {
	f, _, err := doItem[Folder](ctx, c, http.MethodPost, orgPath(org, "folders"), nil, payload)
	return f, err
}

// DeleteFolder removes a folder. DELETE /organizations/{org}/folders/{id}
func (c *Client) DeleteFolder(ctx context.Context, org, id string) error {
	_, err := c.Do(ctx, http.MethodDelete, orgPath(org, "folders", id), nil, nil, nil)
	return err
}

// --- tags ---------------------------------------------------------------------

// ListTags lists tags. GET /organizations/{org}/tags
func (c *Client) ListTags(ctx context.Context, org string, o ListOptions) ([]Tag, *Pagination, error) {
	return CollectAll[Tag](ctx, c, orgPath(org, "tags"), o)
}

// CreateTag creates a tag. POST /organizations/{org}/tags
func (c *Client) CreateTag(ctx context.Context, org string, payload json.RawMessage) (*Tag, error) {
	t, _, err := doItem[Tag](ctx, c, http.MethodPost, orgPath(org, "tags"), nil, payload)
	return t, err
}

// UpdateTag updates a tag. PUT /organizations/{org}/tags/{id}
func (c *Client) UpdateTag(ctx context.Context, org, id string, payload json.RawMessage) (*Tag, error) {
	t, _, err := doItem[Tag](ctx, c, http.MethodPut, orgPath(org, "tags", id), nil, payload)
	return t, err
}

// DeleteTag removes a tag. DELETE /organizations/{org}/tags/{id}
func (c *Client) DeleteTag(ctx context.Context, org, id string) error {
	_, err := c.Do(ctx, http.MethodDelete, orgPath(org, "tags", id), nil, nil, nil)
	return err
}
