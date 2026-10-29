package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// testdata/blueprint-export-milestone.json is GET .../checklists/{id}
// ?with=steps,automated_actions,stages recorded from the staging API on
// 2026-10-24 (tallyfy/work-queue#4261), the "data" object only, which is what
// `blueprint export` writes. The template has 3 steps, one milestone holding
// steps 1 and 2, a text field on step 1, a dropdown on step 2, and step 2's
// deadline counting from step 1. The org id, host and user id are replaced.
const importSource = "b851740ee140cbd4b8bf08797994670d"

// importAPI is a small in-memory stand-in for the api-v2 routes an export
// then import round trip touches. Each rule below mirrors api-v2 code:
//
//   - POST checklists keeps template fields only and ignores "steps" and
//     "stages" (Checklist::build, Arr::only on $fillableFields), and drops a
//     kick-off field that carries an id (measured on staging);
//   - POST checklists/{id}/steps appends the step last, whatever position the
//     body says (StepBuilder::buildBasicStep), needs a title and the template's
//     id as checklist_id (CreateStepRequest), skips a capture whose id is not
//     already on the step (CaptureRepository::sync; measured on staging: a
//     new step sent an existing field's id got no field), and refuses a deadline
//     whose anchor step does not exist (StepDeadlineDependencies);
//     and answers 422 to a present null for a typed field (measured);
//   - POST stages needs title, subject_id, subject_type and position
//     (CreateStageRequest) and answers the id as a string (StageTransformer);
//   - PUT .../link-stage needs an integer stage_id of the same template
//     (AddStageStepRequest); PUT .../steps/{id} needs a title (UpdateStepRequest).
type importAPI struct {
	source map[string]json.RawMessage

	mu            sync.Mutex
	templates     map[string]*fakeTemplate
	stepOwner     map[string]string // step id -> template id, every template
	nextTemplate  int
	nextStep      int
	nextStage     int
	nextCapture   int
	posts         int
	linkCalls     int
	failStepTitle string

	// The target org's member user ids and group ids, as the users and groups
	// routes list them and as api-v2's org_user and org_group rules check them.
	members       map[string]bool
	groups        map[string]bool
	memberLookups int

	// The target org's tags (id -> title), whether it has single sign-on on,
	// and its company domains. Every member's email is member<id>@example.com.
	tags        map[string]string
	samlEnabled bool
	domains     []string
	lookups     map[string]int    // GETs per lookup route: users, groups, tags, org, domains
	lookupQuery map[string]string // the last query string each lookup route got
	failLookup  string            // a lookup route that answers 500
}

type fakeTemplate struct {
	id     string
	fields map[string]any
	steps  []*fakeStep
	stages []*fakeStage
}

type fakeStep struct {
	id       string
	body     map[string]any
	captures []map[string]any
	deadline map[string]any
	stageID  *int
}

type fakeStage struct {
	id       int
	title    string
	position int
}

func newImportAPI(t *testing.T) (*importAPI, *httptest.Server) {
	t.Helper()
	api := &importAPI{templates: map[string]*fakeTemplate{}, stepOwner: map[string]string{}, members: map[string]bool{}, groups: map[string]bool{},
		tags: map[string]string{}, lookups: map[string]int{}, lookupQuery: map[string]string{}}
	if err := json.Unmarshal(readFixture(t, "blueprint-export-milestone.json"), &api.source); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(api.handle))
	t.Cleanup(srv.Close)
	return api, srv
}

func (a *importAPI) handle(w http.ResponseWriter, r *http.Request) {
	if !authorized(w, r) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	const prefix = "/organizations/org_test/"
	if r.Method == http.MethodGet && r.URL.Path == strings.TrimSuffix(prefix, "/") {
		if a.lookup(w, r, "org") {
			b, _ := json.Marshal(map[string]any{"data": map[string]any{"id": "org_test", "name": "Test org", "saml_enabled": a.samlEnabled}})
			writeJSON(w, 200, string(b))
		}
		return
	}
	if !strings.HasPrefix(r.URL.Path, prefix) {
		writeJSON(w, 404, `{"error":true,"message":"no mock route"}`)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	var body map[string]any
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		a.posts++
		if err := json.Unmarshal([]byte(readBody(r)), &body); err != nil {
			writeJSON(w, 400, `{"error":true,"message":"body is not a JSON object"}`)
			return
		}
	}
	with := strings.Split(r.URL.Query().Get("with"), ",")

	switch {
	case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "checklists":
		a.getTemplate(w, parts[1], with)
	case r.Method == http.MethodGet && len(parts) == 1 && (parts[0] == "users" || parts[0] == "groups"):
		if a.lookup(w, r, parts[0]) {
			a.listMembers(w, parts[0])
		}
	case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "tags":
		if a.lookup(w, r, "tags") {
			a.listTags(w, r.URL.Query().Get("all") == "true")
		}
	case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "domains":
		if a.lookup(w, r, "domains") {
			// api-v2 answers this route as a bare collection, with no
			// pagination meta (OrganizationsDomainsController::index).
			items := []any{}
			for i, d := range a.domains {
				items = append(items, map[string]any{"id": i + 1, "name": d, "organization_id": "org_test"})
			}
			b, _ := json.Marshal(map[string]any{"data": items})
			writeJSON(w, 200, string(b))
		}
	case r.Method == http.MethodPost && len(parts) == 1 && parts[0] == "checklists":
		a.createTemplate(w, body)
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "checklists" && parts[2] == "steps":
		a.createStep(w, parts[1], body)
	case r.Method == http.MethodPost && len(parts) == 1 && parts[0] == "stages":
		a.createStage(w, body)
	case r.Method == http.MethodPut && len(parts) == 5 && parts[0] == "checklists" && parts[2] == "steps" && parts[4] == "link-stage":
		a.linkStage(w, parts[1], parts[3], body)
	case r.Method == http.MethodPut && len(parts) == 4 && parts[0] == "checklists" && parts[2] == "steps":
		a.updateStep(w, parts[1], parts[3], body)
	default:
		writeJSON(w, 404, `{"error":true,"message":"no mock route: `+r.Method+" "+r.URL.Path+`"}`)
	}
}

func has(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func (a *importAPI) getTemplate(w http.ResponseWriter, id string, with []string) {
	if id == importSource {
		out := map[string]json.RawMessage{}
		for k, v := range a.source {
			out[k] = v
		}
		// Includes appear only when asked for, as with Fractal.
		if !has(with, "steps") {
			delete(out, "steps")
		}
		if !has(with, "stages") {
			delete(out, "stages")
		}
		b, _ := json.Marshal(map[string]any{"data": out})
		writeJSON(w, 200, string(b))
		return
	}
	tpl, ok := a.templates[id]
	if !ok {
		writeJSON(w, 404, `{"error":true,"message":"template not found"}`)
		return
	}
	out := map[string]any{}
	for k, v := range tpl.fields {
		out[k] = v
	}
	out["id"], out["status"], out["steps_count"] = tpl.id, "started", len(tpl.steps)
	if has(with, "steps") {
		steps := make([]any, 0, len(tpl.steps))
		for i, s := range tpl.steps {
			steps = append(steps, a.renderStep(tpl, s, i))
		}
		out["steps"] = map[string]any{"data": steps}
	}
	if has(with, "stages") {
		stages := make([]any, 0, len(tpl.stages))
		for _, g := range tpl.stages {
			stages = append(stages, map[string]any{"id": strconv.Itoa(g.id), "title": g.title, "position": g.position})
		}
		out["stages"] = map[string]any{"data": stages}
	}
	b, _ := json.Marshal(map[string]any{"data": out})
	writeJSON(w, 200, string(b))
}

func (a *importAPI) renderStep(tpl *fakeTemplate, s *fakeStep, i int) map[string]any {
	out := map[string]any{"id": s.id, "checklist_id": tpl.id, "position": i + 1, "step_type": "task", "summary": nil}
	for _, k := range []string{"title", "step_type", "summary"} {
		if v, ok := s.body[k]; ok {
			out[k] = v
		}
	}
	out["deadline"] = s.deadline
	for _, k := range []string{"assignees", "groups"} {
		if v, ok := s.body[k]; ok {
			out[k] = v
		} else {
			out[k] = []any{}
		}
	}
	caps := make([]any, 0, len(s.captures))
	for _, c := range s.captures {
		caps = append(caps, c)
	}
	out["captures"] = caps
	if s.stageID != nil {
		out["stage_id"] = *s.stageID
	} else {
		out["stage_id"] = nil
	}
	return out
}

func (a *importAPI) listMembers(w http.ResponseWriter, kind string) {
	a.memberLookups++
	items := []any{}
	if kind == "users" {
		for id := range a.members {
			n, _ := strconv.Atoi(id)
			items = append(items, map[string]any{"id": n, "email": "member" + id + "@example.com"})
		}
	} else {
		for id := range a.groups {
			items = append(items, map[string]any{"id": id, "name": "group " + id})
		}
	}
	b, _ := json.Marshal(map[string]any{"data": items, "meta": map[string]any{"pagination": map[string]any{
		"total": len(items), "count": len(items), "per_page": 100, "current_page": 1, "total_pages": 1, "links": map[string]any{}}}})
	writeJSON(w, 200, string(b))
}

// lookup counts a GET on a lookup route and answers 500 when the test asked
// that route to fail. It reports whether the caller should answer.
func (a *importAPI) lookup(w http.ResponseWriter, r *http.Request, route string) bool {
	a.lookups[route]++
	a.lookupQuery[route] = r.URL.RawQuery
	if a.failLookup == route {
		writeJSON(w, 500, `{"error":true,"message":"lookup failed on purpose"}`)
		return false
	}
	return true
}

// listTags mirrors api-v2's tags index: without all=true it leaves out the
// tags made for processes, whose titles start "from:", "task:" or "step:"
// (TagsController::index, Tag::scopeWithoutAutoProcessTags).
func (a *importAPI) listTags(w http.ResponseWriter, all bool) {
	items := []any{}
	for id, title := range a.tags {
		if !all && (strings.HasPrefix(title, "from:") || strings.HasPrefix(title, "task:") || strings.HasPrefix(title, "step:")) {
			continue
		}
		items = append(items, map[string]any{"id": id, "title": title})
	}
	b, _ := json.Marshal(map[string]any{"data": items, "meta": map[string]any{"pagination": map[string]any{
		"total": len(items), "count": len(items), "per_page": 100, "current_page": 1, "total_pages": 1, "links": map[string]any{}}}})
	writeJSON(w, 200, string(b))
}

// guestRefusal mirrors api-v2's ValidGuestEmail for the two checks that
// depend on the org: the email is lowercased, then refused when single
// sign-on is on and its domain is a company domain, or when it is a member's.
func (a *importAPI) guestRefusal(email string) string {
	e := strings.ToLower(email)
	if a.samlEnabled {
		if _, domain, ok := strings.Cut(e, "@"); ok && has(a.domains, domain) {
			return `"` + e + `" uses the company domain ` + domain + `, which signs in with single sign-on.`
		}
	}
	for id := range a.members {
		if e == "member"+id+"@example.com" {
			return `The email address "` + e + `" is already a member of this organization.`
		}
	}
	return ""
}

func fail422(w http.ResponseWriter, msg string) {
	b, _ := json.Marshal(map[string]any{"error": true, "message": msg})
	writeJSON(w, 422, string(b))
}

func (a *importAPI) createTemplate(w http.ResponseWriter, body map[string]any) {
	if _, ok := body["type"]; !ok {
		fail422(w, "The type field is required.")
		return
	}
	// CreateChecklistRequest: the template's own users and groups must be in
	// the org (org_user, org_group), and its default tag must be one of the
	// org's tags.
	if msg := a.refusedAssignee(body, "users"); msg != "" {
		fail422(w, msg)
		return
	}
	if msg := a.refusedAssignee(body, "groups"); msg != "" {
		fail422(w, msg)
		return
	}
	if tag, _ := body["default_tag"].(string); tag != "" {
		if _, ok := a.tags[tag]; !ok {
			fail422(w, "The selected default tag is invalid.")
			return
		}
	}
	a.nextTemplate++
	tpl := &fakeTemplate{id: fmt.Sprintf("bp_imp_%d", a.nextTemplate), fields: map[string]any{}}
	for k, v := range body {
		if k == "steps" || k == "stages" {
			continue // not fillable on a template: dropped without a word
		}
		tpl.fields[k] = v
	}
	// Measured on staging 2026-10-24: a kick-off field sent with an id is
	// dropped (201 all the same); one with no id is created on this template.
	kept := []any{}
	if pre, ok := body["prerun"].([]any); ok {
		for _, p := range pre {
			pm, _ := p.(map[string]any)
			if _, hasID := pm["id"]; hasID {
				continue
			}
			a.nextCapture++
			pm["id"], pm["checklist_id"] = fmt.Sprintf("ko_%d", a.nextCapture), tpl.id
			kept = append(kept, pm)
		}
	}
	tpl.fields["prerun"] = kept
	a.templates[tpl.id] = tpl
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"id": tpl.id, "title": body["title"], "status": "started", "steps_count": 0}})
	writeJSON(w, 201, string(b))
}

// refusedAssignee checks a list of user ids ("users", "assignees") or group
// ids ("groups") the way org_user and org_group do, and returns the 422
// message, or "" when every one is in the org.
func (a *importAPI) refusedAssignee(body map[string]any, key string) string {
	members, msg := a.members, "Some users are not valid members of this organization"
	if key == "groups" {
		members, msg = a.groups, "Some values are not valid groups of this organization"
	}
	ids, _ := body[key].([]any)
	for _, id := range ids {
		if !members[fmt.Sprint(id)] {
			return msg
		}
	}
	return ""
}

func (a *importAPI) validDeadline(d any) (map[string]any, bool) {
	m, ok := d.(map[string]any)
	if !ok {
		return nil, false
	}
	anchor, _ := m["step"].(string)
	if anchor == "start_run" {
		return m, true
	}
	_, exists := a.stepOwner[anchor]
	return m, exists
}

func (a *importAPI) createStep(w http.ResponseWriter, tplID string, body map[string]any) {
	tpl, ok := a.templates[tplID]
	if !ok {
		writeJSON(w, 404, `{"error":true,"message":"template not found"}`)
		return
	}
	title, _ := body["title"].(string)
	if title == "" {
		fail422(w, "The title field is required.")
		return
	}
	if body["checklist_id"] != tplID {
		fail422(w, "The selected checklist id is invalid.")
		return
	}
	if title == a.failStepTitle {
		fail422(w, "rejected by the test")
		return
	}
	// api-v2's org_user and org_group rules: every assignee must be in the
	// target org (measured on staging 2026-10-27: a source-only member got
	// 422 "Some users are not valid members of this organization").
	for _, key := range []string{"assignees", "groups"} {
		if msg := a.refusedAssignee(body, key); msg != "" {
			fail422(w, msg)
			return
		}
	}
	guests, _ := body["guests"].([]any)
	for _, g := range guests {
		if msg := a.guestRefusal(fmt.Sprint(g)); msg != "" {
			fail422(w, msg)
			return
		}
	}
	// Measured on staging 2026-10-24: a present null answers 422 for these
	// typed fields, while the nullable send_chromeless answers 201.
	for _, k := range []string{"start_date", "top_secret", "max_assignable"} {
		if v, present := body[k]; present && v == nil {
			fail422(w, "The "+k+" field is invalid.")
			return
		}
	}
	s := &fakeStep{body: body, deadline: map[string]any{"step": "start_run", "unit": "day", "value": float64(1), "option": "from"}}
	if d, present := body["deadline"]; present {
		m, ok := a.validDeadline(d)
		if !ok {
			fail422(w, "The step that this deadline depends on does not exist.")
			return
		}
		s.deadline = m
	}
	if caps, ok := body["captures"].([]any); ok {
		for _, c := range caps {
			cm, _ := c.(map[string]any)
			if _, hasID := cm["id"]; hasID {
				continue // an id that is not already on this step is skipped
			}
			a.nextCapture++
			cm["id"] = fmt.Sprintf("cap_%d", a.nextCapture)
			cm["position"] = float64(len(s.captures) + 1)
			s.captures = append(s.captures, cm)
		}
	}
	a.nextStep++
	s.id = fmt.Sprintf("st_%d", a.nextStep)
	tpl.steps = append(tpl.steps, s)
	a.stepOwner[s.id] = tpl.id
	b, _ := json.Marshal(map[string]any{"data": a.renderStep(tpl, s, len(tpl.steps)-1)})
	writeJSON(w, 201, string(b))
}

func (a *importAPI) createStage(w http.ResponseWriter, body map[string]any) {
	title, _ := body["title"].(string)
	subject, _ := body["subject_id"].(string)
	pos, posOK := body["position"].(float64)
	tpl, ok := a.templates[subject]
	if title == "" || !ok || body["subject_type"] != "Checklist" || !posOK {
		fail422(w, "title, subject_id, subject_type and position are required")
		return
	}
	a.nextStage++
	g := &fakeStage{id: 7000 + a.nextStage, title: title, position: int(pos)}
	tpl.stages = append(tpl.stages, g)
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"id": strconv.Itoa(g.id), "title": g.title, "position": g.position}})
	writeJSON(w, 201, string(b))
}

func (a *importAPI) findStep(tplID, stepID string) (*fakeTemplate, *fakeStep) {
	tpl, ok := a.templates[tplID]
	if !ok {
		return nil, nil
	}
	for _, s := range tpl.steps {
		if s.id == stepID {
			return tpl, s
		}
	}
	return tpl, nil
}

func (a *importAPI) linkStage(w http.ResponseWriter, tplID, stepID string, body map[string]any) {
	a.linkCalls++
	tpl, s := a.findStep(tplID, stepID)
	if s == nil {
		writeJSON(w, 404, `{"error":true,"message":"step not found"}`)
		return
	}
	id, ok := body["stage_id"].(float64)
	if !ok {
		fail422(w, "The stage id must be an integer.")
		return
	}
	for _, g := range tpl.stages {
		if g.id == int(id) {
			v := g.id
			s.stageID = &v
			b, _ := json.Marshal(map[string]any{"data": a.renderStep(tpl, s, 0)})
			writeJSON(w, 200, string(b))
			return
		}
	}
	fail422(w, "The selected stage id is invalid.")
}

func (a *importAPI) updateStep(w http.ResponseWriter, tplID, stepID string, body map[string]any) {
	tpl, s := a.findStep(tplID, stepID)
	if s == nil {
		writeJSON(w, 404, `{"error":true,"message":"step not found"}`)
		return
	}
	if t, _ := body["title"].(string); t == "" {
		fail422(w, "The title field is required.")
		return
	}
	if d, present := body["deadline"]; present {
		m, ok := a.validDeadline(d)
		if !ok {
			fail422(w, "The step that this deadline depends on does not exist.")
			return
		}
		s.deadline = m
	}
	b, _ := json.Marshal(map[string]any{"data": a.renderStep(tpl, s, 0)})
	writeJSON(w, 200, string(b))
}

// --- comparing exports ----------------------------------------------------

type normCapture struct {
	Label     string
	FieldType string
	Required  bool
	Options   string
}

type normStep struct {
	Title     string
	StepType  string
	Deadline  string // value unit option, then the anchor as a 1-based step index in the same file
	Milestone string
	Captures  []normCapture
}

// normalizeExport reduces an exported blueprint to what a round trip must
// keep, with every id replaced by what it points at inside the same file.
func normalizeExport(t *testing.T, data []byte) []normStep {
	t.Helper()
	var bp struct {
		Steps struct {
			Data []struct {
				ID       string         `json:"id"`
				Title    string         `json:"title"`
				StepType string         `json:"step_type"`
				Position int            `json:"position"`
				Deadline map[string]any `json:"deadline"`
				StageID  any            `json:"stage_id"`
				Captures []struct {
					Label     string `json:"label"`
					FieldType string `json:"field_type"`
					Required  bool   `json:"required"`
					Options   any    `json:"options"`
				} `json:"captures"`
			} `json:"data"`
		} `json:"steps"`
		Stages struct {
			Data []struct {
				ID    any    `json:"id"`
				Title string `json:"title"`
			} `json:"data"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(data, &bp); err != nil {
		t.Fatalf("export is not a blueprint object: %v\n%s", err, data)
	}
	index := map[string]int{}
	for i, s := range bp.Steps.Data {
		index[s.ID] = i + 1
		if s.Position != i+1 {
			t.Errorf("export lists %q at position %d in slot %d", s.Title, s.Position, i+1)
		}
	}
	milestone := map[string]string{}
	for _, g := range bp.Stages.Data {
		milestone[fmt.Sprint(g.ID)] = g.Title
	}
	out := make([]normStep, 0, len(bp.Steps.Data))
	for _, s := range bp.Steps.Data {
		anchor := fmt.Sprint(s.Deadline["step"])
		if anchor != "start_run" {
			if n, ok := index[anchor]; ok {
				anchor = fmt.Sprintf("step %d", n)
			} else {
				anchor = "outside this blueprint: " + anchor
			}
		}
		n := normStep{
			Title:    s.Title,
			StepType: s.StepType,
			Deadline: fmt.Sprintf("%v %v %v %s", s.Deadline["value"], s.Deadline["unit"], s.Deadline["option"], anchor),
		}
		if s.StageID != nil {
			key := fmt.Sprint(s.StageID)
			if title, ok := milestone[key]; ok {
				n.Milestone = title
			} else {
				n.Milestone = "missing milestone " + key
			}
		}
		for _, c := range s.Captures {
			opts, _ := json.Marshal(c.Options)
			n.Captures = append(n.Captures, normCapture{c.Label, c.FieldType, c.Required, string(opts)})
		}
		out = append(out, n)
	}
	return out
}

func importedID(t *testing.T, stdout string) string {
	t.Helper()
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(stdout), &obj); err != nil || obj.ID == "" {
		t.Fatalf("import did not print a blueprint with an id: %v\n%s", err, stdout)
	}
	return obj.ID
}

// TestBlueprintImportRoundTrip is tallyfy/work-queue#4261: export a template,
// import the file, export the new template, and get the same steps in the
// same order, with their form fields, milestone and step-relative deadline.
func TestBlueprintImportRoundTrip(t *testing.T) {
	api, srv := newImportAPI(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "first.json")
	second := filepath.Join(dir, "second.json")

	if res := run(t, srv.URL, "", "blueprint", "export", importSource, "--out", first); res.code != 0 {
		t.Fatalf("export exit = %d; stderr=%s", res.code, res.stderr)
	}
	imp := run(t, srv.URL, "", "--yes", "blueprint", "import", first, "-o", "json")
	if imp.code != 0 {
		t.Fatalf("import exit = %d, want 0; stderr=%s", imp.code, imp.stderr)
	}
	newID := importedID(t, imp.stdout)
	if newID == importSource {
		t.Fatalf("import reported the source template id")
	}
	if res := run(t, srv.URL, "", "blueprint", "export", newID, "--out", second); res.code != 0 {
		t.Fatalf("second export exit = %d; stderr=%s", res.code, res.stderr)
	}

	a, _ := os.ReadFile(first)
	b, _ := os.ReadFile(second)
	want, got := normalizeExport(t, a), normalizeExport(t, b)
	if len(want) != 3 {
		t.Fatalf("source export has %d steps, want the fixture's 3", len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the steps\n got: %+v\nwant: %+v", got, want)
	}
	if want[0].Milestone == "" || want[1].Deadline != "2 days from step 1" {
		t.Errorf("fixture lost what this test is for: %+v", want)
	}
	if !strings.Contains(imp.stderr, "with 3 step(s) and 1 milestone(s)") {
		t.Errorf("import summary missing from stderr: %s", imp.stderr)
	}
	var printed struct {
		StepsCount int `json:"steps_count"`
	}
	_ = json.Unmarshal([]byte(imp.stdout), &printed)
	if printed.StepsCount != 3 {
		t.Errorf("import printed steps_count %d, want 3 (the blueprint as it stands after the import)", printed.StepsCount)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.linkCalls != 2 {
		t.Errorf("milestone links = %d, want 2 (steps 1 and 2)", api.linkCalls)
	}
}

// An export made before milestones were exported still imports every step,
// and says which milestone it could not re-create.
func TestBlueprintImportWithoutMilestones(t *testing.T) {
	api, srv := newImportAPI(t)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(readFixture(t, "blueprint-export-milestone.json"), &obj); err != nil {
		t.Fatal(err)
	}
	delete(obj, "stages")
	path := filepath.Join(t.TempDir(), "old.json")
	b, _ := json.Marshal(obj)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	res := run(t, srv.URL, "", "--yes", "blueprint", "import", path, "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	if !strings.Contains(res.stderr, "milestone 6243 is not in the file") {
		t.Errorf("stderr should name the milestone it could not re-create:\n%s", res.stderr)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	tpl := api.templates[importedID(t, res.stdout)]
	if tpl == nil || len(tpl.steps) != 3 {
		t.Fatalf("imported template = %+v, want 3 steps", tpl)
	}
	if api.linkCalls != 0 {
		t.Errorf("link-stage called %d times with no milestones to link to", api.linkCalls)
	}
}

// A deadline that counts from a later step is set once that step exists,
// and points at the new step, never the source template's.
func TestBlueprintImportForwardDeadline(t *testing.T) {
	_, srv := newImportAPI(t)
	var obj map[string]any
	if err := json.Unmarshal(readFixture(t, "blueprint-export-milestone.json"), &obj); err != nil {
		t.Fatal(err)
	}
	steps := obj["steps"].(map[string]any)["data"].([]any)
	first, third := steps[0].(map[string]any), steps[2].(map[string]any)
	first["deadline"] = map[string]any{"step": third["id"], "unit": "days", "value": float64(3), "option": "prior_to"}
	dir := t.TempDir()
	in := filepath.Join(dir, "forward.json")
	b, _ := json.Marshal(obj)
	if err := os.WriteFile(in, b, 0o600); err != nil {
		t.Fatal(err)
	}
	res := run(t, srv.URL, "", "--yes", "blueprint", "import", in, "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	out := filepath.Join(dir, "out.json")
	if r := run(t, srv.URL, "", "blueprint", "export", importedID(t, res.stdout), "--out", out); r.code != 0 {
		t.Fatalf("export exit = %d; stderr=%s", r.code, r.stderr)
	}
	exported, _ := os.ReadFile(out)
	got := normalizeExport(t, exported)
	want := normalizeExport(t, b)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the steps\n got: %+v\nwant: %+v", got, want)
	}
	if len(got) != 3 || got[0].Deadline != "3 days prior_to step 3" {
		t.Errorf("step 1 deadline = %+v, want it to count from the new step 3", got)
	}
}

// A step the API refuses stops the import, keeps the API's exit code, and
// names the half-built blueprint so it can be deleted.
func TestBlueprintImportStopsAndNamesIncompleteBlueprint(t *testing.T) {
	api, srv := newImportAPI(t)
	api.mu.Lock()
	api.failStepTitle = "wq4260 import step 2"
	api.mu.Unlock()
	res := run(t, srv.URL, "", "--yes", "blueprint", "import", filepath.Join("testdata", "blueprint-export-milestone.json"))
	if res.code != 7 {
		t.Fatalf("exit = %d, want 7 (validation); stderr=%s", res.code, res.stderr)
	}
	for _, want := range []string{"blueprint bp_imp_1 was created", `step 2 of 3 ("wq4260 import step 2")`, "tallyfy blueprint delete bp_imp_1"} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, res.stderr)
		}
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if n := len(api.templates["bp_imp_1"].steps); n != 1 {
		t.Errorf("steps created = %d, want 1 before the refusal", n)
	}
}

// --dry-run shows the whole plan and sends nothing that writes.
func TestBlueprintImportDryRun(t *testing.T) {
	api, srv := newImportAPI(t)
	res := run(t, srv.URL, "", "blueprint", "import", filepath.Join("testdata", "blueprint-export-milestone.json"), "--dry-run")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "then 1 milestone(s)") || !strings.Contains(res.stdout, "and 3 step(s) in order") {
		t.Errorf("dry run should list the milestones and steps it would create:\n%s", res.stdout)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.posts != 0 {
		t.Errorf("dry run sent %d write request(s), want 0", api.posts)
	}
}

// An exported null means "not set". Sent as is, api-v2 refuses it for typed
// step fields, so the import leaves those keys out.
func TestBlueprintImportDropsNullFields(t *testing.T) {
	api, srv := newImportAPI(t)
	var obj map[string]any
	if err := json.Unmarshal(readFixture(t, "blueprint-export-milestone.json"), &obj); err != nil {
		t.Fatal(err)
	}
	steps := obj["steps"].(map[string]any)["data"].([]any)
	steps[2].(map[string]any)["start_date"] = nil
	path := filepath.Join(t.TempDir(), "nulls.json")
	b, _ := json.Marshal(obj)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	res := run(t, srv.URL, "", "--yes", "blueprint", "import", path, "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if tpl := api.templates[importedID(t, res.stdout)]; tpl == nil || len(tpl.steps) != 3 {
		t.Fatalf("imported template = %+v, want 3 steps", tpl)
	}
}

// Kick-off fields come through too. testdata/blueprint-export-kickoff.json is
// a staging export (2026-10-24) of a 1 step template with one kick-off field.
func TestBlueprintImportKeepsKickoffFields(t *testing.T) {
	api, srv := newImportAPI(t)
	res := run(t, srv.URL, "", "--yes", "blueprint", "import", filepath.Join("testdata", "blueprint-export-kickoff.json"), "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	tpl := api.templates[importedID(t, res.stdout)]
	if tpl == nil {
		t.Fatal("no template was created")
	}
	pre, _ := tpl.fields["prerun"].([]any)
	if len(pre) != 1 {
		t.Fatalf("kick-off fields on the new template = %v, want the 1 from the export", pre)
	}
	f, _ := pre[0].(map[string]any)
	if f["label"] != "wq4260 kickoff text" || f["field_type"] != "text" || f["checklist_id"] != tpl.id {
		t.Errorf("kick-off field = %v, want the exported text field on the new template", f)
	}
	if len(tpl.steps) != 1 {
		t.Errorf("steps = %d, want 1", len(tpl.steps))
	}
}

// Automations are not re-created yet, so the import must say so rather than
// drop them without a word (measured on staging: 1 in the source, 0 after).
func TestBlueprintImportWarnsAboutAutomations(t *testing.T) {
	_, srv := newImportAPI(t)
	var obj map[string]any
	if err := json.Unmarshal(readFixture(t, "blueprint-export-milestone.json"), &obj); err != nil {
		t.Fatal(err)
	}
	obj["automated_actions"] = []any{map[string]any{"id": "c558bc44559ecdac0171d3832868b028", "automated_alias": "wq4260 show step 2"}}
	path := filepath.Join(t.TempDir(), "automations.json")
	b, _ := json.Marshal(obj)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--yes", "blueprint", "import", path}, {"blueprint", "import", path, "--dry-run"}} {
		res := run(t, srv.URL, "", args...)
		if res.code != 0 {
			t.Fatalf("%v: exit = %d, want 0; stderr=%s", args, res.code, res.stderr)
		}
		if !strings.Contains(res.stderr, "the file has 1 automation(s), and import does not re-create automations yet") {
			t.Errorf("%v: stderr should warn that the automation was not imported:\n%s", args, res.stderr)
		}
	}
	plain := run(t, srv.URL, "", "--yes", "blueprint", "import", filepath.Join("testdata", "blueprint-export-milestone.json"))
	if strings.Contains(plain.stderr, "automation(s)") {
		t.Errorf("a file with no automations should not warn:\n%s", plain.stderr)
	}
}

// An export can list steps out of position order: on api-v2 production the
// ?with=steps include that `blueprint export` uses has no fixed order
// (api-v2#11323). The import must still create the steps by position, and
// step 2's deadline must still point at the new step 1. Adapted from the
// reviewer's probe on tallyfy/cli#20.
func TestBlueprintImportOrdersStepsByPosition(t *testing.T) {
	_, srv := newImportAPI(t)
	orig := readFixture(t, "blueprint-export-milestone.json")
	var obj map[string]any
	if err := json.Unmarshal(orig, &obj); err != nil {
		t.Fatal(err)
	}
	steps := obj["steps"].(map[string]any)["data"].([]any)
	for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
		steps[i], steps[j] = steps[j], steps[i]
	}
	if first := steps[0].(map[string]any)["position"]; first != float64(3) {
		t.Fatalf("the reversed file starts with position %v, want 3 (the test input is not out of order)", first)
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "reversed.json")
	b, _ := json.Marshal(obj)
	if err := os.WriteFile(in, b, 0o600); err != nil {
		t.Fatal(err)
	}
	res := run(t, srv.URL, "", "--yes", "blueprint", "import", in, "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	out := filepath.Join(dir, "out.json")
	if r := run(t, srv.URL, "", "blueprint", "export", importedID(t, res.stdout), "--out", out); r.code != 0 {
		t.Fatalf("export exit = %d; stderr=%s", r.code, r.stderr)
	}
	exported, _ := os.ReadFile(out)
	got, want := normalizeExport(t, exported), normalizeExport(t, orig)
	if len(want) != 3 {
		t.Fatalf("fixture has %d steps, want 3", len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a file listing steps out of order did not import in position order\n got: %+v\nwant: %+v", got, want)
	}
}

// --- step assignees across orgs (tallyfy/work-queue#4280) -----------------

// testdata/blueprint-export-assignees.json is a staging export (2026-10-27)
// from a test org of a 4 step template: step 1 assigned to a user who is in
// both test orgs (123 here), step 2 to a user only in the source org (456),
// step 3 to a source org group, and step 4 to all three. The org ids, user
// ids and group id are replaced.
func assigneesFixture(t *testing.T, sourceOrg string) string {
	t.Helper()
	return assigneesFixtureWith(t, sourceOrg, nil)
}

// assigneesFixtureWith is assigneesFixture with edit applied to the parsed
// file first. edit gets the blueprint object and its steps in file order.
func assigneesFixtureWith(t *testing.T, sourceOrg string, edit func(bp map[string]any, steps []map[string]any)) string {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(readFixture(t, "blueprint-export-assignees.json"), &obj); err != nil {
		t.Fatal(err)
	}
	if obj["exported_from_org"] != "org_source" {
		t.Fatalf("fixture exported_from_org = %v, want org_source", obj["exported_from_org"])
	}
	if edit != nil {
		// The recorded export has these, all empty; the edits fill them in.
		for _, k := range []string{"users", "groups", "default_tag"} {
			if _, ok := obj[k]; !ok {
				t.Fatalf("fixture has no %q key", k)
			}
		}
		raw := obj["steps"].(map[string]any)["data"].([]any)
		steps := make([]map[string]any, len(raw))
		for i, s := range raw {
			steps[i] = s.(map[string]any)
			if _, ok := steps[i]["guests"]; !ok {
				t.Fatalf("fixture step %d has no guests key", i+1)
			}
		}
		edit(obj, steps)
	}
	if sourceOrg == "" {
		delete(obj, "exported_from_org")
	} else {
		obj["exported_from_org"] = sourceOrg
	}
	path := filepath.Join(t.TempDir(), "assignees.json")
	b, _ := json.Marshal(obj)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// stepAssignees reads the users and groups each created step ended up with.
func stepAssignees(t *testing.T, api *importAPI, id string) []string {
	t.Helper()
	tpl := api.templates[id]
	if tpl == nil {
		t.Fatalf("no template %s", id)
	}
	out := make([]string, 0, len(tpl.steps))
	for _, s := range tpl.steps {
		out = append(out, fmt.Sprintf("users=%v groups=%v", s.body["assignees"], s.body["groups"]))
	}
	return out
}

func setMembers(api *importAPI, users []string, groups []string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	for _, u := range users {
		api.members[u] = true
	}
	for _, g := range groups {
		api.groups[g] = true
	}
}

// Same org: every assignee is kept, and the import does not look up members.
func TestBlueprintImportSameOrgKeepsAssignees(t *testing.T) {
	api, srv := newImportAPI(t)
	setMembers(api, []string{"123", "456"}, []string{"grp_source_1"})
	res := run(t, srv.URL, "", "--yes", "blueprint", "import", assigneesFixture(t, "org_test"), "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	want := []string{"users=[123] groups=[]", "users=[456] groups=[]", "users=[] groups=[grp_source_1]", "users=[123 456] groups=[grp_source_1]"}
	if got := stepAssignees(t, api, importedID(t, res.stdout)); !reflect.DeepEqual(got, want) {
		t.Errorf("assignees = %q\nwant every one kept %q", got, want)
	}
	if api.memberLookups != 0 {
		t.Errorf("member lookups = %d, want 0 for a same-org import", api.memberLookups)
	}
	if strings.Contains(res.stderr, "leaves off") {
		t.Errorf("a same-org import should not warn about assignees:\n%s", res.stderr)
	}
}

// Another org with none of the assignees: the import completes, every
// assignee is left off, and one warning names each.
func TestBlueprintImportOtherOrgDropsNonMembers(t *testing.T) {
	api, srv := newImportAPI(t)
	setMembers(api, []string{"789"}, nil)
	res := run(t, srv.URL, "", "--yes", "blueprint", "import", assigneesFixture(t, "org_source"), "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0 (the import completes); stderr=%s", res.code, res.stderr)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	want := []string{"users=[] groups=[]", "users=[] groups=[]", "users=[] groups=[]", "users=[] groups=[]"}
	if got := stepAssignees(t, api, importedID(t, res.stdout)); !reflect.DeepEqual(got, want) {
		t.Errorf("assignees = %q, want all left off %q", got, want)
	}
	if n := strings.Count(res.stderr, "leaves off"); n != 1 {
		t.Errorf("warnings about assignees = %d, want exactly 1:\n%s", n, res.stderr)
	}
	for _, s := range []string{"exported from org org_source", "leaves off 6 assignee(s) that are not in org org_test",
		`user 123 on step 1 ("wq4260 assign step 1 (member of both orgs)")`, `user 456 on step 2`, `group grp_source_1 on step 3`,
		`user 123 on step 4`, `user 456 on step 4`, `group grp_source_1 on step 4`} {
		if !strings.Contains(res.stderr, s) {
			t.Errorf("warning missing %q:\n%s", s, res.stderr)
		}
	}
}

// Another org with some of the assignees: members are kept, the rest are
// left off and named.
func TestBlueprintImportOtherOrgKeepsMembers(t *testing.T) {
	api, srv := newImportAPI(t)
	setMembers(api, []string{"123"}, nil)
	res := run(t, srv.URL, "", "--yes", "blueprint", "import", assigneesFixture(t, "org_source"), "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	want := []string{"users=[123] groups=[]", "users=[] groups=[]", "users=[] groups=[]", "users=[123] groups=[]"}
	if got := stepAssignees(t, api, importedID(t, res.stdout)); !reflect.DeepEqual(got, want) {
		t.Errorf("assignees = %q\nwant only user 123 kept %q", got, want)
	}
	if !strings.Contains(res.stderr, "leaves off 4 assignee(s)") || strings.Contains(res.stderr, "user 123") {
		t.Errorf("warning should name the 4 left off and not user 123:\n%s", res.stderr)
	}
}

// A file from before exports recorded their org: the import cannot tell, so
// it checks, and keeps assignees that are members.
func TestBlueprintImportUnknownOrgChecksAssignees(t *testing.T) {
	api, srv := newImportAPI(t)
	setMembers(api, []string{"123", "456"}, []string{"grp_source_1"})
	res := run(t, srv.URL, "", "--yes", "blueprint", "import", assigneesFixture(t, ""), "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.memberLookups != 2 {
		t.Errorf("member lookups = %d, want 2 (users and groups)", api.memberLookups)
	}
	want := []string{"users=[123] groups=[]", "users=[456] groups=[]", "users=[] groups=[grp_source_1]", "users=[123 456] groups=[grp_source_1]"}
	if got := stepAssignees(t, api, importedID(t, res.stdout)); !reflect.DeepEqual(got, want) {
		t.Errorf("assignees = %q\nwant every member kept %q", got, want)
	}
}

// Export records the org it ran against as the first key, and leaves the
// API's object as it was after that.
func TestBlueprintExportRecordsOrg(t *testing.T) {
	_, srv := newImportAPI(t)
	out := filepath.Join(t.TempDir(), "export.json")
	if res := run(t, srv.URL, "", "blueprint", "export", importSource, "--out", out); res.code != 0 {
		t.Fatalf("export exit = %d; stderr=%s", res.code, res.stderr)
	}
	b, _ := os.ReadFile(out)
	if !strings.HasPrefix(string(b), "{\n  \"exported_from_org\": \"org_test\",\n") {
		t.Errorf("export does not start with the org key:\n%.200s", b)
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("export is not JSON: %v", err)
	}
	if obj["id"] != importSource {
		t.Errorf("export lost the blueprint's own fields: id = %v", obj["id"])
	}
}

// --- what else another org refuses (tallyfy/work-queue#4280) -------------

// configure changes the fake's target org under its lock.
func configure(api *importAPI, f func(a *importAPI)) {
	api.mu.Lock()
	defer api.mu.Unlock()
	f(api)
}

// totalLookups counts every GET on a lookup route. The caller holds api.mu.
func totalLookups(api *importAPI) int {
	n := 0
	for _, c := range api.lookups {
		n += c
	}
	return n
}

// templateField reads a field as the template create got it, as JSON.
func templateField(t *testing.T, api *importAPI, id, key string) string {
	t.Helper()
	tpl := api.templates[id]
	if tpl == nil {
		t.Fatalf("no template %s", id)
	}
	v, ok := tpl.fields[key]
	if !ok {
		return "(absent)"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// stepGuests reads the guests each created step ended up with.
func stepGuests(t *testing.T, api *importAPI, id string) []string {
	t.Helper()
	tpl := api.templates[id]
	if tpl == nil {
		t.Fatalf("no template %s", id)
	}
	out := make([]string, 0, len(tpl.steps))
	for _, s := range tpl.steps {
		out = append(out, fmt.Sprint(s.body["guests"]))
	}
	return out
}

// clearAssignees empties every step's users and groups, so a test looks at
// one kind of check at a time.
func clearAssignees(steps []map[string]any) {
	for _, s := range steps {
		s["assignees"], s["groups"] = []any{}, []any{}
	}
}

// The blueprint's own users and groups are checked like step assignees
// (CreateChecklistRequest: org_user, org_group): members are kept, and the
// rest are left off and named in the same one warning.
func TestBlueprintImportOtherOrgTemplateAssignees(t *testing.T) {
	api, srv := newImportAPI(t)
	setMembers(api, []string{"123"}, nil)
	path := assigneesFixtureWith(t, "org_source", func(bp map[string]any, _ []map[string]any) {
		bp["users"] = []any{123, 456}
		bp["groups"] = []any{"grp_source_1"}
	})
	res := run(t, srv.URL, "", "--yes", "blueprint", "import", path, "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0 (the import completes); stderr=%s", res.code, res.stderr)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	id := importedID(t, res.stdout)
	if got := templateField(t, api, id, "users"); got != "[123]" {
		t.Errorf("blueprint users = %s, want [123]", got)
	}
	if got := templateField(t, api, id, "groups"); got != "[]" {
		t.Errorf("blueprint groups = %s, want []", got)
	}
	if n := strings.Count(res.stderr, "assignee(s) that are not in org"); n != 1 {
		t.Errorf("assignee warnings = %d, want exactly 1:\n%s", n, res.stderr)
	}
	for _, s := range []string{"leaves off 6 assignee(s) that are not in org org_test", "user 456 on the blueprint",
		"group grp_source_1 on the blueprint", "user 456 on step 2", "group grp_source_1 on step 3"} {
		if !strings.Contains(res.stderr, s) {
			t.Errorf("warning missing %q:\n%s", s, res.stderr)
		}
	}
	if strings.Contains(res.stderr, "user 123") {
		t.Errorf("the warning names user 123, who is a member:\n%s", res.stderr)
	}
}

// The blueprint's own users and groups start the lookup by themselves: no
// step names anyone here, and the import must still read the members and
// groups lists and leave off what the org lacks. Adapted from the reviewer's
// probe on tallyfy/cli#22; without it, dropping the two template lines in
// checks() went unnoticed.
func TestBlueprintImportOtherOrgTemplateOnlyAssignees(t *testing.T) {
	api, srv := newImportAPI(t)
	setMembers(api, []string{"123"}, nil)
	path := assigneesFixtureWith(t, "org_source", func(bp map[string]any, steps []map[string]any) {
		clearAssignees(steps)
		bp["users"] = []any{123, 456}
		bp["groups"] = []any{"grp_source_1"}
	})
	res := run(t, srv.URL, "", "--yes", "blueprint", "import", path, "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0 (the import completes); stderr=%s", res.code, res.stderr)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	id := importedID(t, res.stdout)
	if got := templateField(t, api, id, "users"); got != "[123]" {
		t.Errorf("blueprint users = %s, want [123]", got)
	}
	if got := templateField(t, api, id, "groups"); got != "[]" {
		t.Errorf("blueprint groups = %s, want []", got)
	}
	if !strings.Contains(res.stderr, "leaves off 2 assignee(s) that are not in org org_test: user 456 on the blueprint, group grp_source_1 on the blueprint") {
		t.Errorf("warning should name user 456 and group grp_source_1 on the blueprint:\n%s", res.stderr)
	}
	if api.lookups["users"] != 1 || api.lookups["groups"] != 1 {
		t.Errorf("lookups = %v, want the members and groups lists once each", api.lookups)
	}
}

// The blueprint's default tag must be one of the org's tags. When it is not,
// the import leaves it off and says so. Process tags count as tags, so the
// lookup must ask for them too.
func TestBlueprintImportOtherOrgDefaultTag(t *testing.T) {
	path := assigneesFixtureWith(t, "org_source", func(bp map[string]any, steps []map[string]any) {
		bp["default_tag"] = "tag_source_1"
		clearAssignees(steps)
	})
	for _, tc := range []struct {
		name, tagID, tagTitle string
		want                  string // the default_tag the template create got
	}{
		{"not in the org", "tag_other", "other", "(absent)"},
		{"a process tag of the org", "tag_source_1", "step:approve", `"tag_source_1"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, srv := newImportAPI(t)
			configure(api, func(a *importAPI) { a.tags[tc.tagID] = tc.tagTitle })
			res := run(t, srv.URL, "", "--yes", "blueprint", "import", path, "-o", "json")
			if res.code != 0 {
				t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
			}
			api.mu.Lock()
			defer api.mu.Unlock()
			if got := templateField(t, api, importedID(t, res.stdout), "default_tag"); got != tc.want {
				t.Errorf("default_tag = %s, want %s", got, tc.want)
			}
			warned := strings.Contains(res.stderr, "leaves off the default tag tag_source_1, which is not in org org_test")
			if warned != (tc.want == "(absent)") {
				t.Errorf("default tag warning = %v, want %v:\n%s", warned, !warned, res.stderr)
			}
			if api.lookups["tags"] != 1 || api.lookups["users"] != 0 || api.lookups["groups"] != 0 {
				t.Errorf("lookups = %v, want only the tags list", api.lookups)
			}
			if !strings.Contains(api.lookupQuery["tags"], "all=true") {
				t.Errorf("tags lookup query = %q, want all=true so process tags are listed", api.lookupQuery["tags"])
			}
		})
	}
}

// A step guest whose email belongs to a member of the org, or is at one of
// its company domains while single sign-on is on, is refused by api-v2's
// ValidGuestEmail. The import leaves such guests off, names each with the
// reason, and keeps the rest.
func TestBlueprintImportOtherOrgGuests(t *testing.T) {
	path := assigneesFixtureWith(t, "org_source", func(_ map[string]any, steps []map[string]any) {
		clearAssignees(steps)
		steps[0]["guests"] = []any{"Member123@Example.com"}
		steps[1]["guests"] = []any{"pat@corp.example"}
		steps[2]["guests"] = []any{"outsider@elsewhere.example"}
		steps[3]["guests"] = []any{"member123@example.com", "pat@corp.example", "outsider@elsewhere.example"}
	})
	for _, tc := range []struct {
		name    string
		sso     bool
		want    []string
		warning []string
		domains int // lookups of the company domains
	}{
		{"single sign-on on", true,
			[]string{"[]", "[]", "[outsider@elsewhere.example]", "[outsider@elsewhere.example]"},
			[]string{"leaves off 4 guest(s) that org org_test would refuse",
				`guest Member123@Example.com on step 1 ("wq4260 assign step 1 (member of both orgs)") (already a member)`,
				`guest pat@corp.example on step 2 ("wq4260 assign step 2 (source-only member)") (single sign-on domain corp.example)`,
				`guest member123@example.com on step 4 ("wq4260 assign step 4 (mixed)") (already a member)`,
				`guest pat@corp.example on step 4 ("wq4260 assign step 4 (mixed)") (single sign-on domain corp.example)`},
			1},
		{"single sign-on off", false,
			[]string{"[]", "[pat@corp.example]", "[outsider@elsewhere.example]", "[pat@corp.example outsider@elsewhere.example]"},
			[]string{"leaves off 2 guest(s) that org org_test would refuse"},
			0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, srv := newImportAPI(t)
			configure(api, func(a *importAPI) {
				a.members["123"] = true
				a.samlEnabled = tc.sso
				a.domains = []string{"corp.example"}
			})
			res := run(t, srv.URL, "", "--yes", "blueprint", "import", path, "-o", "json")
			if res.code != 0 {
				t.Fatalf("exit = %d, want 0 (the import completes); stderr=%s", res.code, res.stderr)
			}
			api.mu.Lock()
			defer api.mu.Unlock()
			if got := stepGuests(t, api, importedID(t, res.stdout)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("guests = %q\nwant %q", got, tc.want)
			}
			if n := strings.Count(res.stderr, "guest(s) that org"); n != 1 {
				t.Errorf("guest warnings = %d, want exactly 1:\n%s", n, res.stderr)
			}
			for _, s := range tc.warning {
				if !strings.Contains(res.stderr, s) {
					t.Errorf("warning missing %q:\n%s", s, res.stderr)
				}
			}
			if strings.Contains(res.stderr, "guest outsider@") {
				t.Errorf("the warning names a guest the org takes:\n%s", res.stderr)
			}
			if api.lookups["users"] != 1 || api.lookups["org"] != 1 || api.lookups["domains"] != tc.domains || api.lookups["groups"] != 0 {
				t.Errorf("lookups = %v, want users 1, org 1, domains %d, groups 0", api.lookups, tc.domains)
			}
		})
	}
}

// Into the org the file came from, the blueprint's users, groups and default
// tag and the step guests are kept as exported, and nothing is looked up.
func TestBlueprintImportSameOrgKeepsEverything(t *testing.T) {
	api, srv := newImportAPI(t)
	configure(api, func(a *importAPI) {
		a.members["123"], a.members["456"], a.groups["grp_source_1"] = true, true, true
		a.tags["tag_source_1"] = "source"
	})
	path := assigneesFixtureWith(t, "org_test", func(bp map[string]any, steps []map[string]any) {
		bp["users"] = []any{123, 456}
		bp["groups"] = []any{"grp_source_1"}
		bp["default_tag"] = "tag_source_1"
		steps[0]["guests"] = []any{"outsider@elsewhere.example"}
	})
	res := run(t, srv.URL, "", "--yes", "blueprint", "import", path, "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	id := importedID(t, res.stdout)
	for key, want := range map[string]string{"users": "[123,456]", "groups": `["grp_source_1"]`, "default_tag": `"tag_source_1"`} {
		if got := templateField(t, api, id, key); got != want {
			t.Errorf("blueprint %s = %s, want %s", key, got, want)
		}
	}
	if got := stepGuests(t, api, id); got[0] != "[outsider@elsewhere.example]" {
		t.Errorf("step 1 guests = %s, want the exported guest", got[0])
	}
	if n := totalLookups(api); n != 0 {
		t.Errorf("lookups = %v, want none for a same-org import", api.lookups)
	}
	if strings.Contains(res.stderr, "leaves off") {
		t.Errorf("a same-org import should leave nothing off:\n%s", res.stderr)
	}
}

// A file with nothing to check (no users, groups, default tag or guests)
// imports into another org without a lookup or a warning. Added from the
// review of tallyfy/cli#20.
func TestBlueprintImportNothingToCheckLooksNothingUp(t *testing.T) {
	src := filepath.Join("testdata", "blueprint-export-milestone.json")
	var obj struct {
		From   *string `json:"exported_from_org"`
		Users  []any   `json:"users"`
		Groups []any   `json:"groups"`
		Tag    *string `json:"default_tag"`
		Steps  struct {
			Data []struct {
				Assignees []any `json:"assignees"`
				Groups    []any `json:"groups"`
				Guests    []any `json:"guests"`
			} `json:"data"`
		} `json:"steps"`
	}
	b, _ := os.ReadFile(src)
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatal(err)
	}
	if obj.From != nil || len(obj.Users)+len(obj.Groups) > 0 || obj.Tag != nil || len(obj.Steps.Data) != 3 {
		t.Fatalf("fixture is not a cross-org file with nothing to check: %+v", obj)
	}
	for i, s := range obj.Steps.Data {
		if len(s.Assignees)+len(s.Groups)+len(s.Guests) > 0 {
			t.Fatalf("fixture step %d has assignees or guests: %+v", i+1, s)
		}
	}
	api, srv := newImportAPI(t)
	res := run(t, srv.URL, "", "--yes", "blueprint", "import", src, "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if n := totalLookups(api); n != 0 || api.memberLookups != 0 {
		t.Errorf("lookups = %v, want none", api.lookups)
	}
	if strings.Contains(res.stderr, "leaves off") {
		t.Errorf("nothing should be left off:\n%s", res.stderr)
	}
}

// A template whose steps name only groups is checked against the groups
// list alone: members of no kind are looked up. Added from the review of
// tallyfy/cli#20.
func TestBlueprintImportOtherOrgGroupsOnly(t *testing.T) {
	api, srv := newImportAPI(t)
	setMembers(api, nil, []string{"grp_source_1"})
	path := assigneesFixtureWith(t, "org_source", func(_ map[string]any, steps []map[string]any) {
		clearAssignees(steps)
		steps[2]["groups"] = []any{"grp_source_1"}
		steps[3]["groups"] = []any{"grp_source_1", "grp_source_2"}
	})
	res := run(t, srv.URL, "", "--yes", "blueprint", "import", path, "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	want := []string{"users=[] groups=[]", "users=[] groups=[]", "users=[] groups=[grp_source_1]", "users=[] groups=[grp_source_1]"}
	if got := stepAssignees(t, api, importedID(t, res.stdout)); !reflect.DeepEqual(got, want) {
		t.Errorf("assignees = %q\nwant %q", got, want)
	}
	if !strings.Contains(res.stderr, `leaves off 1 assignee(s) that are not in org org_test: group grp_source_2 on step 4 ("wq4260 assign step 4 (mixed)")`) {
		t.Errorf("warning should name grp_source_2 alone:\n%s", res.stderr)
	}
	if api.lookups["groups"] != 1 || api.lookups["users"] != 0 {
		t.Errorf("lookups = %v, want the groups list only", api.lookups)
	}
}

// A lookup that fails stops the import before anything is created, and says
// it was the check that failed. Added from the review of tallyfy/cli#20.
func TestBlueprintImportFailedLookupCreatesNothing(t *testing.T) {
	path := assigneesFixtureWith(t, "org_source", func(bp map[string]any, steps []map[string]any) {
		bp["default_tag"] = "tag_source_1"
		steps[0]["guests"] = []any{"outsider@elsewhere.example"}
	})
	for _, route := range []string{"users", "groups", "tags", "org", "domains"} {
		t.Run(route, func(t *testing.T) {
			api, srv := newImportAPI(t)
			configure(api, func(a *importAPI) {
				a.failLookup = route
				a.samlEnabled = true
				a.domains = []string{"corp.example"}
			})
			res := run(t, srv.URL, "", "--yes", "blueprint", "import", path, "-o", "json")
			if res.code == 0 {
				t.Fatalf("exit = 0, want a failure when the %s lookup fails; stderr=%s", route, res.stderr)
			}
			for _, s := range []string{"against org org_test before creating anything", "lookup failed on purpose"} {
				if !strings.Contains(res.stderr, s) {
					t.Errorf("stderr missing %q:\n%s", s, res.stderr)
				}
			}
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.lookups[route] != 1 {
				t.Errorf("the %s lookup ran %d time(s), want 1 (the failure must come from it)", route, api.lookups[route])
			}
			if api.posts != 0 || len(api.templates) != 0 {
				t.Errorf("writes = %d, templates = %d, want nothing created", api.posts, len(api.templates))
			}
		})
	}
}

// --dry-run says what it would check, looks nothing up and writes nothing.
// Added from the review of tallyfy/cli#20.
func TestBlueprintImportDryRunSaysWhatItChecks(t *testing.T) {
	api, srv := newImportAPI(t)
	cross := assigneesFixtureWith(t, "org_source", func(bp map[string]any, steps []map[string]any) {
		bp["default_tag"] = "tag_source_1"
		steps[0]["guests"] = []any{"outsider@elsewhere.example"}
	})
	for _, tc := range []struct {
		name, path, want string
	}{
		{"another org", cross, "[dry-run] first check the assigned users and groups, default tag and step guests against org org_test (the file was exported from org org_source), and leave off what org org_test would refuse"},
		{"an older file", assigneesFixture(t, ""), "[dry-run] first check the assigned users and groups against org org_test (the file does not say which org it was exported from), and leave off what org org_test would refuse"},
		{"the same org", assigneesFixture(t, "org_test"), ""},
	} {
		res := run(t, srv.URL, "", "blueprint", "import", tc.path, "--dry-run")
		if res.code != 0 {
			t.Fatalf("%s: exit = %d, want 0; stderr=%s", tc.name, res.code, res.stderr)
		}
		if tc.want == "" && strings.Contains(res.stdout, "first check") {
			t.Errorf("%s: dry run should check nothing:\n%s", tc.name, res.stdout)
		}
		if tc.want != "" && !strings.Contains(res.stdout, tc.want+"\n") {
			t.Errorf("%s: dry run missing\n%s\ngot:\n%s", tc.name, tc.want, res.stdout)
		}
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.posts != 0 || totalLookups(api) != 0 {
		t.Errorf("dry run sent %d write(s) and %v lookup(s), want none", api.posts, api.lookups)
	}
}
