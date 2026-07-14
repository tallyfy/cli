package tallyfy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// lastCall records the most recent request the fake API received.
type lastCall struct {
	method string
	path   string
	query  url.Values
	body   string
}

// newRecorder returns a client whose server records the last call and always
// answers 200 with respBody.
func newRecorder(t *testing.T, respBody string) (*Client, *lastCall) {
	t.Helper()
	rec := &lastCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.query = r.URL.Query()
		rec.body = string(b)
		io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return New(Options{BaseURL: srv.URL, Token: testToken, UserAgent: testUA, MaxRetries: -1}), rec
}

const onePageMeta = `,"meta":{"pagination":{"total":1,"count":1,"per_page":10,"current_page":1,"total_pages":1,"links":[]}}`

func listResp(items string) string { return `{"data":` + items + onePageMeta + `}` }
func itemResp(item string) string  { return `{"data":` + item + `}` }

func TestResourceMethods(t *testing.T) {
	ctx := context.Background()
	payload := json.RawMessage(`{"title":"X"}`)

	cases := []struct {
		name       string
		resp       string
		invoke     func(c *Client) error
		wantMethod string
		wantPath   string
		wantQuery  map[string]string // subset; nil = skip
		wantBody   string            // exact; "" = must be empty
	}{
		{
			name: "Me", resp: itemResp(`{"id":123,"email":"amit@x.com","username":"amit","first_name":"Amit"}`),
			invoke: func(c *Client) error {
				me, err := c.Me(ctx)
				if err != nil {
					return err
				}
				if me.Email != "amit@x.com" || me.ID.String() != "123" {
					return fmt.Errorf("decoded me = %+v", me)
				}
				return nil
			},
			wantMethod: "GET", wantPath: "/me",
		},
		{
			name: "MyOrganizations", resp: listResp(`[{"id":"org1","name":"Acme"}]`),
			invoke: func(c *Client) error {
				orgs, pg, err := c.MyOrganizations(ctx, ListOptions{})
				if err != nil {
					return err
				}
				if len(orgs) != 1 || orgs[0].ID != "org1" || pg == nil {
					return fmt.Errorf("orgs = %+v pg = %+v", orgs, pg)
				}
				return nil
			},
			wantMethod: "GET", wantPath: "/me/organizations",
		},
		{
			name: "ListBlueprints", resp: listResp(`[{"id":"b1","title":"T"}]`),
			invoke: func(c *Client) error {
				bps, _, err := c.ListBlueprints(ctx, "org1", ListOptions{})
				if err == nil && (len(bps) != 1 || bps[0].ID != "b1") {
					return fmt.Errorf("bps = %+v", bps)
				}
				return err
			},
			wantMethod: "GET", wantPath: "/organizations/org1/checklists",
		},
		{
			name: "GetBlueprint with includes", resp: itemResp(`{"id":"b1","title":"T","steps":[{"id":"s1"}]}`),
			invoke: func(c *Client) error {
				bp, raw, err := c.GetBlueprint(ctx, "org1", "b1", []string{"steps", "tags"})
				if err != nil {
					return err
				}
				if bp.ID != "b1" || len(raw) == 0 || string(bp.Raw) != string(raw) {
					return fmt.Errorf("bp = %+v raw = %s", bp, raw)
				}
				return nil
			},
			wantMethod: "GET", wantPath: "/organizations/org1/checklists/b1",
			wantQuery: map[string]string{"with": "steps,tags"},
		},
		{
			name: "CreateBlueprint", resp: itemResp(`{"id":"b9","title":"X"}`),
			invoke: func(c *Client) error {
				bp, raw, err := c.CreateBlueprint(ctx, "org1", payload)
				if err != nil {
					return err
				}
				if bp.ID != "b9" || len(raw) == 0 {
					return fmt.Errorf("bp = %+v", bp)
				}
				return nil
			},
			wantMethod: "POST", wantPath: "/organizations/org1/checklists", wantBody: `{"title":"X"}`,
		},
		{
			name: "UpdateBlueprint", resp: itemResp(`{"id":"b1","title":"X"}`),
			invoke: func(c *Client) error {
				_, err := c.UpdateBlueprint(ctx, "org1", "b1", payload)
				return err
			},
			wantMethod: "PUT", wantPath: "/organizations/org1/checklists/b1", wantBody: `{"title":"X"}`,
		},
		{
			name: "DeleteBlueprint", resp: itemResp(`{}`),
			invoke: func(c *Client) error {
				return c.DeleteBlueprint(ctx, "org1", "b1")
			},
			wantMethod: "DELETE", wantPath: "/organizations/org1/checklists/b1",
		},
		{
			name: "CloneBlueprint with title", resp: itemResp(`{"id":"b2","title":"Copy"}`),
			invoke: func(c *Client) error {
				bp, err := c.CloneBlueprint(ctx, "org1", "b1", "Copy")
				if err != nil {
					return err
				}
				if bp.ID != "b2" {
					return fmt.Errorf("bp = %+v", bp)
				}
				return nil
			},
			wantMethod: "POST", wantPath: "/organizations/org1/checklists/b1/clone",
			wantBody: `{"tenant":"org1","title":"Copy"}`,
		},
		{
			name: "CloneBlueprint no title", resp: itemResp(`{"id":"b2"}`),
			invoke: func(c *Client) error {
				_, err := c.CloneBlueprint(ctx, "org1", "b1", "")
				return err
			},
			wantMethod: "POST", wantPath: "/organizations/org1/checklists/b1/clone",
			wantBody: `{"tenant":"org1"}`,
		},
		{
			name: "PublishBlueprint", resp: itemResp(`{}`),
			invoke: func(c *Client) error {
				return c.PublishBlueprint(ctx, "org1", "b1")
			},
			wantMethod: "PUT", wantPath: "/organizations/org1/checklists/b1/publish", wantBody: "",
		},
		{
			name: "GetBlueprintSteps", resp: itemResp(`[{"id":"s1"},{"id":"s2"}]`),
			invoke: func(c *Client) error {
				steps, err := c.GetBlueprintSteps(ctx, "org1", "b1")
				if err != nil {
					return err
				}
				if string(steps) != `[{"id":"s1"},{"id":"s2"}]` {
					return fmt.Errorf("steps = %s", steps)
				}
				return nil
			},
			wantMethod: "GET", wantPath: "/organizations/org1/checklists/b1/steps",
		},
		{
			name: "ListAutomations extracts inline field",
			resp: itemResp(`{"id":"b1","automated_actions":[{"id":"aa1"}]}`),
			invoke: func(c *Client) error {
				aa, err := c.ListAutomations(ctx, "org1", "b1")
				if err != nil {
					return err
				}
				if string(aa) != `[{"id":"aa1"}]` {
					return fmt.Errorf("automations = %s", aa)
				}
				return nil
			},
			wantMethod: "GET", wantPath: "/organizations/org1/checklists/b1",
		},
		{
			name: "ListAutomations missing field yields empty array",
			resp: itemResp(`{"id":"b1"}`),
			invoke: func(c *Client) error {
				aa, err := c.ListAutomations(ctx, "org1", "b1")
				if err != nil {
					return err
				}
				if string(aa) != `[]` {
					return fmt.Errorf("automations = %s", aa)
				}
				return nil
			},
			wantMethod: "GET", wantPath: "/organizations/org1/checklists/b1",
		},
		{
			name: "CreateAutomation", resp: itemResp(`{"id":"aa1"}`),
			invoke: func(c *Client) error {
				_, err := c.CreateAutomation(ctx, "org1", "b1", payload)
				return err
			},
			wantMethod: "POST", wantPath: "/organizations/org1/checklists/b1/automated-actions",
			wantBody: `{"title":"X"}`,
		},
		{
			name: "UpdateAutomation", resp: itemResp(`{"id":"aa1"}`),
			invoke: func(c *Client) error {
				_, err := c.UpdateAutomation(ctx, "org1", "b1", "aa1", payload)
				return err
			},
			wantMethod: "PUT", wantPath: "/organizations/org1/checklists/b1/automated-actions/aa1",
			wantBody: `{"title":"X"}`,
		},
		{
			name: "DeleteAutomation", resp: itemResp(`{}`),
			invoke: func(c *Client) error {
				return c.DeleteAutomation(ctx, "org1", "b1", "aa1")
			},
			wantMethod: "DELETE", wantPath: "/organizations/org1/checklists/b1/automated-actions/aa1",
		},
		{
			name: "ListProcesses", resp: listResp(`[{"id":"r1","name":"Run 1"}]`),
			invoke: func(c *Client) error {
				ps, _, err := c.ListProcesses(ctx, "org1", ListOptions{})
				if err == nil && (len(ps) != 1 || ps[0].ID != "r1") {
					return fmt.Errorf("ps = %+v", ps)
				}
				return err
			},
			wantMethod: "GET", wantPath: "/organizations/org1/runs",
		},
		{
			name: "GetProcess with includes", resp: itemResp(`{"id":"r1","name":"Run 1","checklist_id":"b1"}`),
			invoke: func(c *Client) error {
				p, raw, err := c.GetProcess(ctx, "org1", "r1", []string{"tasks"})
				if err != nil {
					return err
				}
				if p.ID != "r1" || p.ChecklistID != "b1" || len(raw) == 0 {
					return fmt.Errorf("p = %+v", p)
				}
				return nil
			},
			wantMethod: "GET", wantPath: "/organizations/org1/runs/r1",
			wantQuery: map[string]string{"with": "tasks"},
		},
		{
			name: "LaunchProcess", resp: itemResp(`{"id":"r9","name":"New"}`),
			invoke: func(c *Client) error {
				p, err := c.LaunchProcess(ctx, "org1", json.RawMessage(`{"checklist_id":"b1","name":"New"}`))
				if err != nil {
					return err
				}
				if p.ID != "r9" {
					return fmt.Errorf("p = %+v", p)
				}
				return nil
			},
			wantMethod: "POST", wantPath: "/organizations/org1/runs",
			wantBody: `{"checklist_id":"b1","name":"New"}`,
		},
		{
			name: "UpdateProcess", resp: itemResp(`{"id":"r1"}`),
			invoke: func(c *Client) error {
				_, err := c.UpdateProcess(ctx, "org1", "r1", payload)
				return err
			},
			wantMethod: "PUT", wantPath: "/organizations/org1/runs/r1", wantBody: `{"title":"X"}`,
		},
		{
			name: "ArchiveProcess", resp: itemResp(`{}`),
			invoke: func(c *Client) error {
				return c.ArchiveProcess(ctx, "org1", "r1")
			},
			wantMethod: "DELETE", wantPath: "/organizations/org1/runs/r1",
		},
		{
			name: "ReactivateProcess", resp: itemResp(`{}`),
			invoke: func(c *Client) error {
				return c.ReactivateProcess(ctx, "org1", "r1")
			},
			wantMethod: "PUT", wantPath: "/organizations/org1/runs/r1/activate", wantBody: "",
		},
		{
			name: "ListRunTasks", resp: listResp(`[{"id":"t1","title":"Task"}]`),
			invoke: func(c *Client) error {
				ts, _, err := c.ListRunTasks(ctx, "org1", "r1", ListOptions{})
				if err == nil && (len(ts) != 1 || ts[0].ID != "t1") {
					return fmt.Errorf("ts = %+v", ts)
				}
				return err
			},
			wantMethod: "GET", wantPath: "/organizations/org1/runs/r1/tasks",
		},
		{
			name: "ListOrgTasks", resp: listResp(`[{"id":"t1"}]`),
			invoke: func(c *Client) error {
				_, _, err := c.ListOrgTasks(ctx, "org1", ListOptions{})
				return err
			},
			wantMethod: "GET", wantPath: "/organizations/org1/tasks",
		},
		{
			name: "ListMyTasks", resp: listResp(`[{"id":"t1"}]`),
			invoke: func(c *Client) error {
				_, _, err := c.ListMyTasks(ctx, "org1", ListOptions{})
				return err
			},
			wantMethod: "GET", wantPath: "/organizations/org1/me/tasks",
		},
		{
			name: "GetRunTask", resp: itemResp(`{"id":"t1","title":"Task","run_id":"r1"}`),
			invoke: func(c *Client) error {
				tk, raw, err := c.GetRunTask(ctx, "org1", "r1", "t1")
				if err != nil {
					return err
				}
				if tk.ID != "t1" || tk.RunID == nil || *tk.RunID != "r1" || len(raw) == 0 {
					return fmt.Errorf("task = %+v", tk)
				}
				return nil
			},
			wantMethod: "GET", wantPath: "/organizations/org1/runs/r1/tasks/t1",
		},
		{
			name: "GetOrgTask", resp: itemResp(`{"id":"t1","task_type":"member"}`),
			invoke: func(c *Client) error {
				tk, _, err := c.GetOrgTask(ctx, "org1", "t1")
				if err != nil {
					return err
				}
				if tk.TaskType != "member" {
					return fmt.Errorf("task = %+v", tk)
				}
				return nil
			},
			wantMethod: "GET", wantPath: "/organizations/org1/tasks/t1",
		},
		{
			name: "UpdateRunTask", resp: itemResp(`{"id":"t1"}`),
			invoke: func(c *Client) error {
				_, err := c.UpdateRunTask(ctx, "org1", "r1", "t1", payload)
				return err
			},
			wantMethod: "PUT", wantPath: "/organizations/org1/runs/r1/tasks/t1", wantBody: `{"title":"X"}`,
		},
		{
			name: "CompleteTask", resp: itemResp(`{"id":"t1","status":"completed"}`),
			invoke: func(c *Client) error {
				tk, err := c.CompleteTask(ctx, "org1", "r1", "t1")
				if err != nil {
					return err
				}
				if tk.Status != "completed" {
					return fmt.Errorf("task = %+v", tk)
				}
				return nil
			},
			wantMethod: "POST", wantPath: "/organizations/org1/runs/r1/completed-tasks",
			wantBody: `{"task_id":"t1"}`,
		},
		{
			name: "ReopenTask", resp: itemResp(`{}`),
			invoke: func(c *Client) error {
				return c.ReopenTask(ctx, "org1", "r1", "t1")
			},
			wantMethod: "DELETE", wantPath: "/organizations/org1/runs/r1/completed-tasks/t1",
		},
		{
			name: "CompleteOrgTask", resp: itemResp(`{"id":"t1"}`),
			invoke: func(c *Client) error {
				_, err := c.CompleteOrgTask(ctx, "org1", "t1")
				return err
			},
			wantMethod: "POST", wantPath: "/organizations/org1/completed-tasks",
			wantBody: `{"task_id":"t1"}`,
		},
		{
			name: "ReopenOrgTask", resp: itemResp(`{}`),
			invoke: func(c *Client) error {
				return c.ReopenOrgTask(ctx, "org1", "t1")
			},
			wantMethod: "DELETE", wantPath: "/organizations/org1/completed-tasks/t1",
		},
		{
			name: "CommentTask", resp: itemResp(`{"id":"cm1","content":"hello"}`),
			invoke: func(c *Client) error {
				raw, err := c.CommentTask(ctx, "org1", "t1", "hello")
				if err != nil {
					return err
				}
				if len(raw) == 0 {
					return fmt.Errorf("raw empty")
				}
				return nil
			},
			wantMethod: "POST", wantPath: "/organizations/org1/tasks/t1/comment",
			wantBody: `{"content":"hello"}`,
		},
		{
			name: "ListUsers", resp: listResp(`[{"id":42,"email":"u@x.com","role":"admin"}]`),
			invoke: func(c *Client) error {
				us, _, err := c.ListUsers(ctx, "org1", ListOptions{})
				if err != nil {
					return err
				}
				if len(us) != 1 || us[0].ID.String() != "42" || us[0].Role != "admin" {
					return fmt.Errorf("users = %+v", us)
				}
				return nil
			},
			wantMethod: "GET", wantPath: "/organizations/org1/users",
		},
		{
			name: "InviteUser", resp: itemResp(`{"id":43}`),
			invoke: func(c *Client) error {
				_, err := c.InviteUser(ctx, "org1", json.RawMessage(`{"email":"new@x.com","first_name":"N","last_name":"U"}`))
				return err
			},
			wantMethod: "POST", wantPath: "/organizations/org1/users/invite",
			wantBody: `{"email":"new@x.com","first_name":"N","last_name":"U"}`,
		},
		{
			name: "SetUserRole", resp: itemResp(`{}`),
			invoke: func(c *Client) error {
				return c.SetUserRole(ctx, "org1", "42", "admin")
			},
			wantMethod: "PUT", wantPath: "/organizations/org1/users/42/role",
			wantBody: `{"role":"admin"}`,
		},
		{
			name: "DisableUser uses DELETE", resp: itemResp(`{}`),
			invoke: func(c *Client) error {
				return c.DisableUser(ctx, "org1", "42")
			},
			wantMethod: "DELETE", wantPath: "/organizations/org1/users/42/disable",
		},
		{
			name: "EnableUser uses PUT", resp: itemResp(`{}`),
			invoke: func(c *Client) error {
				return c.EnableUser(ctx, "org1", "42")
			},
			wantMethod: "PUT", wantPath: "/organizations/org1/users/42/enable",
		},
		{
			name: "ListGuests", resp: listResp(`[{"email":"g@x.com"}]`),
			invoke: func(c *Client) error {
				gs, _, err := c.ListGuests(ctx, "org1", ListOptions{})
				if err == nil && (len(gs) != 1 || gs[0].Email != "g@x.com") {
					return fmt.Errorf("guests = %+v", gs)
				}
				return err
			},
			wantMethod: "GET", wantPath: "/organizations/org1/guests",
		},
		{
			name: "GetGuest by email", resp: itemResp(`{"email":"jane@ex.com","first_name":"Jane"}`),
			invoke: func(c *Client) error {
				g, err := c.GetGuest(ctx, "org1", "jane@ex.com")
				if err != nil {
					return err
				}
				if g.FirstName != "Jane" {
					return fmt.Errorf("guest = %+v", g)
				}
				return nil
			},
			wantMethod: "GET", wantPath: "/organizations/org1/guests/jane@ex.com",
		},
		{
			name: "CreateGuest", resp: itemResp(`{"email":"g@x.com"}`),
			invoke: func(c *Client) error {
				_, err := c.CreateGuest(ctx, "org1", json.RawMessage(`{"email":"g@x.com"}`))
				return err
			},
			wantMethod: "POST", wantPath: "/organizations/org1/guests", wantBody: `{"email":"g@x.com"}`,
		},
		{
			name: "UpdateGuest", resp: itemResp(`{"email":"jane@ex.com"}`),
			invoke: func(c *Client) error {
				_, err := c.UpdateGuest(ctx, "org1", "jane@ex.com", json.RawMessage(`{"first_name":"J"}`))
				return err
			},
			wantMethod: "PUT", wantPath: "/organizations/org1/guests/jane@ex.com",
			wantBody: `{"first_name":"J"}`,
		},
		{
			name: "ListGroups", resp: listResp(`[{"id":"g1","name":"Ops"}]`),
			invoke: func(c *Client) error {
				gs, _, err := c.ListGroups(ctx, "org1", ListOptions{})
				if err == nil && (len(gs) != 1 || gs[0].Name != "Ops") {
					return fmt.Errorf("groups = %+v", gs)
				}
				return err
			},
			wantMethod: "GET", wantPath: "/organizations/org1/groups",
		},
		{
			name: "CreateGroup", resp: itemResp(`{"id":"g1"}`),
			invoke: func(c *Client) error {
				_, err := c.CreateGroup(ctx, "org1", payload)
				return err
			},
			wantMethod: "POST", wantPath: "/organizations/org1/groups", wantBody: `{"title":"X"}`,
		},
		{
			name: "UpdateGroup", resp: itemResp(`{"id":"g1"}`),
			invoke: func(c *Client) error {
				_, err := c.UpdateGroup(ctx, "org1", "g1", payload)
				return err
			},
			wantMethod: "PUT", wantPath: "/organizations/org1/groups/g1", wantBody: `{"title":"X"}`,
		},
		{
			name: "DeleteGroup", resp: itemResp(`{}`),
			invoke: func(c *Client) error {
				return c.DeleteGroup(ctx, "org1", "g1")
			},
			wantMethod: "DELETE", wantPath: "/organizations/org1/groups/g1",
		},
		{
			name: "ListFolders", resp: listResp(`[{"id":"f1","name":"HR"}]`),
			invoke: func(c *Client) error {
				fs, _, err := c.ListFolders(ctx, "org1", ListOptions{})
				if err == nil && (len(fs) != 1 || fs[0].Name != "HR") {
					return fmt.Errorf("folders = %+v", fs)
				}
				return err
			},
			wantMethod: "GET", wantPath: "/organizations/org1/folders",
		},
		{
			name: "CreateFolder", resp: itemResp(`{"id":"f1"}`),
			invoke: func(c *Client) error {
				_, err := c.CreateFolder(ctx, "org1", json.RawMessage(`{"name":"HR"}`))
				return err
			},
			wantMethod: "POST", wantPath: "/organizations/org1/folders", wantBody: `{"name":"HR"}`,
		},
		{
			name: "DeleteFolder", resp: itemResp(`{}`),
			invoke: func(c *Client) error {
				return c.DeleteFolder(ctx, "org1", "f1")
			},
			wantMethod: "DELETE", wantPath: "/organizations/org1/folders/f1",
		},
		{
			name: "ListTags", resp: listResp(`[{"id":"tag1","title":"urgent"}]`),
			invoke: func(c *Client) error {
				tags, _, err := c.ListTags(ctx, "org1", ListOptions{})
				if err == nil && (len(tags) != 1 || tags[0].Title != "urgent") {
					return fmt.Errorf("tags = %+v", tags)
				}
				return err
			},
			wantMethod: "GET", wantPath: "/organizations/org1/tags",
		},
		{
			name: "CreateTag", resp: itemResp(`{"id":"tag1"}`),
			invoke: func(c *Client) error {
				_, err := c.CreateTag(ctx, "org1", json.RawMessage(`{"title":"urgent"}`))
				return err
			},
			wantMethod: "POST", wantPath: "/organizations/org1/tags", wantBody: `{"title":"urgent"}`,
		},
		{
			name: "UpdateTag", resp: itemResp(`{"id":"tag1"}`),
			invoke: func(c *Client) error {
				_, err := c.UpdateTag(ctx, "org1", "tag1", payload)
				return err
			},
			wantMethod: "PUT", wantPath: "/organizations/org1/tags/tag1", wantBody: `{"title":"X"}`,
		},
		{
			name: "DeleteTag", resp: itemResp(`{}`),
			invoke: func(c *Client) error {
				return c.DeleteTag(ctx, "org1", "tag1")
			},
			wantMethod: "DELETE", wantPath: "/organizations/org1/tags/tag1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newRecorder(t, tc.resp)
			if err := tc.invoke(c); err != nil {
				t.Fatalf("invoke: %v", err)
			}
			if rec.method != tc.wantMethod {
				t.Errorf("method = %s, want %s", rec.method, tc.wantMethod)
			}
			if rec.path != tc.wantPath {
				t.Errorf("path = %s, want %s", rec.path, tc.wantPath)
			}
			for k, v := range tc.wantQuery {
				if got := rec.query.Get(k); got != v {
					t.Errorf("query %s = %q, want %q", k, got, v)
				}
			}
			if rec.body != tc.wantBody {
				t.Errorf("body = %q, want %q", rec.body, tc.wantBody)
			}
		})
	}
}

// TestListMethodsPassListOptions spot-checks that a typed list method routes
// its ListOptions through the paginator (page/per_page/with/extra).
func TestListMethodsPassListOptions(t *testing.T) {
	c, rec := newRecorder(t, listResp(`[]`))
	_, _, err := c.ListProcesses(context.Background(), "org1", ListOptions{
		Page: 3, PerPage: 25, With: []string{"tasks"}, Extra: map[string]string{"status": "active"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"page": "3", "per_page": "25", "with": "tasks", "status": "active"} {
		if got := rec.query.Get(k); got != v {
			t.Errorf("query %s = %q, want %q", k, got, v)
		}
	}
}
