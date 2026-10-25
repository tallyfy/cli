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
	api := &importAPI{templates: map[string]*fakeTemplate{}, stepOwner: map[string]string{}}
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

func fail422(w http.ResponseWriter, msg string) {
	b, _ := json.Marshal(map[string]any{"error": true, "message": msg})
	writeJSON(w, 422, string(b))
}

func (a *importAPI) createTemplate(w http.ResponseWriter, body map[string]any) {
	if _, ok := body["type"]; !ok {
		fail422(w, "The type field is required.")
		return
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
