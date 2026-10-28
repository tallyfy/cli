package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The steps fixtures were recorded from the staging API on 2026-10-24 for a
// throwaway 11 step template (tallyfy/work-queue#4260), with the org id,
// host and user id replaced:
//
//	testdata/steps-11.json        GET .../steps?per_page=100&page=1, the data array
//	testdata/steps-11-page1.json  GET .../steps with no per_page, the whole response
const fixtureBlueprint = "329838beb6e8e96c587fb72ed6bf00ca"

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// authorized applies the same header and token checks as mockAPI.handle.
func authorized(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Accept") != "application/json" ||
		r.Header.Get("X-Tallyfy-Client") != "APIClient" ||
		r.Header.Get("Authorization") != "Bearer "+e2eToken {
		writeJSON(w, 401, `{"error":true,"message":"missing or wrong headers"}`)
		return false
	}
	return true
}

// stepsAPI serves one template's steps the way api-v2 pages them: 10 per
// page when the request sends no per_page (BaseControllerNew), the page the
// caller asks for, and meta.pagination in the recorded shape. maxPerPage,
// when set, caps the page size whatever the caller asks for.
type stepsAPI struct {
	steps      []json.RawMessage
	maxPerPage int

	mu      sync.Mutex
	queries []string
}

func newStepsAPI(t *testing.T, maxPerPage int) (*stepsAPI, *httptest.Server) {
	t.Helper()
	api := &stepsAPI{maxPerPage: maxPerPage}
	if err := json.Unmarshal(readFixture(t, "steps-11.json"), &api.steps); err != nil {
		t.Fatal(err)
	}
	if len(api.steps) != 11 {
		t.Fatalf("fixture has %d steps, want 11", len(api.steps))
	}
	srv := httptest.NewServer(http.HandlerFunc(api.handle))
	t.Cleanup(srv.Close)
	return api, srv
}

func (a *stepsAPI) handle(w http.ResponseWriter, r *http.Request) {
	if !authorized(w, r) {
		return
	}
	want := "/organizations/org_test/checklists/" + fixtureBlueprint + "/steps"
	if r.Method != http.MethodGet || r.URL.Path != want {
		writeJSON(w, 404, `{"error":true,"message":"no mock route: `+r.Method+" "+r.URL.Path+`"}`)
		return
	}
	a.mu.Lock()
	a.queries = append(a.queries, r.URL.RawQuery)
	a.mu.Unlock()
	writeJSON(w, 200, a.page(r.URL.Query()))
}

func (a *stepsAPI) page(q map[string][]string) string {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	perPage := 10
	if v, err := strconv.Atoi(get("per_page")); err == nil && v > 0 {
		perPage = v
	}
	if a.maxPerPage > 0 && perPage > a.maxPerPage {
		perPage = a.maxPerPage
	}
	page := 1
	if v, err := strconv.Atoi(get("page")); err == nil && v > 0 {
		page = v
	}
	total := len(a.steps)
	totalPages := (total + perPage - 1) / perPage
	from := (page - 1) * perPage
	if from > total {
		from = total
	}
	to := from + perPage
	if to > total {
		to = total
	}
	base := "https://api.example.test/organizations/org_test/checklists/" + fixtureBlueprint + "/steps"
	links := map[string]string{}
	if page > 1 {
		links["previous"] = fmt.Sprintf("%s?page=%d", base, page-1)
	}
	if page < totalPages {
		links["next"] = fmt.Sprintf("%s?page=%d", base, page+1)
	}
	data, _ := json.Marshal(a.steps[from:to])
	lb, _ := json.Marshal(links)
	return fmt.Sprintf(`{"data":%s,"meta":{"pagination":{"total":%d,"count":%d,"per_page":%d,"current_page":%d,"total_pages":%d,"links":%s}}}`,
		data, total, to-from, perPage, page, totalPages, lb)
}

// TestStepsMockMatchesRecordedFirstPage keeps the mock honest: its answer to
// the request the CLI used to send (no per_page) must equal what staging
// returned for it, data and pagination alike.
func TestStepsMockMatchesRecordedFirstPage(t *testing.T) {
	_, srv := newStepsAPI(t, 0)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/organizations/org_test/checklists/"+fixtureBlueprint+"/steps", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Tallyfy-Client", "APIClient")
	req.Header.Set("Authorization", "Bearer "+e2eToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, _ := io.ReadAll(resp.Body)

	var mock, recorded struct {
		Data []json.RawMessage `json:"data"`
		Meta struct {
			Pagination map[string]any `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(got, &mock); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(readFixture(t, "steps-11-page1.json"), &recorded); err != nil {
		t.Fatal(err)
	}
	if len(recorded.Data) != 10 {
		t.Fatalf("recorded first page has %d steps, want 10", len(recorded.Data))
	}
	if !reflect.DeepEqual(mock.Meta.Pagination, recorded.Meta.Pagination) {
		t.Errorf("mock pagination %v\nrecorded pagination %v", mock.Meta.Pagination, recorded.Meta.Pagination)
	}
	if len(mock.Data) != len(recorded.Data) {
		t.Fatalf("mock first page has %d steps, recorded has %d", len(mock.Data), len(recorded.Data))
	}
	for i := range mock.Data {
		var m, r any
		_ = json.Unmarshal(mock.Data[i], &m)
		_ = json.Unmarshal(recorded.Data[i], &r)
		if !reflect.DeepEqual(m, r) {
			t.Errorf("step %d differs from the recording", i+1)
		}
	}
}

func stepTitles(t *testing.T, stdout string) []string {
	t.Helper()
	var items []struct {
		Title    string `json:"title"`
		Position int    `json:"position"`
	}
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatalf("steps output is not a JSON array: %v\n%s", err, stdout)
	}
	titles := make([]string, len(items))
	for i, s := range items {
		titles[i] = s.Title
		if s.Position != i+1 {
			t.Errorf("row %d has position %d, want %d (order lost)", i+1, s.Position, i+1)
		}
	}
	return titles
}

func wantFixtureTitles() []string {
	out := make([]string, 11)
	for i := range out {
		out[i] = fmt.Sprintf("Fixture step %02d", i+1)
	}
	return out
}

// TestBlueprintStepsListsEveryStep is tallyfy/work-queue#4260: the steps
// route answers 10 steps when no per_page is sent, so `blueprint steps`
// stopped at 10 on an 11 step template.
func TestBlueprintStepsListsEveryStep(t *testing.T) {
	_, srv := newStepsAPI(t, 0)
	res := run(t, srv.URL, "", "blueprint", "steps", fixtureBlueprint, "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	if got := stepTitles(t, res.stdout); !reflect.DeepEqual(got, wantFixtureTitles()) {
		t.Errorf("steps = %q\nwant all 11 in order %q", got, wantFixtureTitles())
	}

	table := run(t, srv.URL, "", "blueprint", "steps", fixtureBlueprint)
	if table.code != 0 {
		t.Fatalf("table exit = %d; stderr=%s", table.code, table.stderr)
	}
	if !strings.Contains(table.stdout, "Fixture step 11") {
		t.Errorf("table output is missing the 11th step:\n%s", table.stdout)
	}
}

// TestBlueprintStepsFollowsPagesWhenPageSizeIsCapped proves the command
// follows pagination rather than relying on one large page: with the server
// capping pages at 4, it must make 3 requests and keep the API's order.
func TestBlueprintStepsFollowsPagesWhenPageSizeIsCapped(t *testing.T) {
	api, srv := newStepsAPI(t, 4)
	res := run(t, srv.URL, "", "blueprint", "steps", fixtureBlueprint, "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	if got := stepTitles(t, res.stdout); !reflect.DeepEqual(got, wantFixtureTitles()) {
		t.Errorf("steps = %q\nwant all 11 in order %q", got, wantFixtureTitles())
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.queries) != 3 {
		t.Fatalf("requests = %d (%q), want 3 pages of at most 4", len(api.queries), api.queries)
	}
	for i, q := range api.queries {
		v, err := url.ParseQuery(q)
		if err != nil || v.Get("page") != strconv.Itoa(i+1) {
			t.Errorf("request %d query %q does not ask for page %d", i+1, q, i+1)
		}
	}
}
