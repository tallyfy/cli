// Package e2e compiles the real tallyfy binary and exercises it end to end
// against an httptest mock API. The mock asserts the three mandatory headers
// (Authorization, Accept, X-Tallyfy-Client) on every request, so these tests
// prove the client contract as well as command wiring and the exit-code map.
package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// binPath is the compiled test binary, built once in TestMain.
var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tallyfy-e2e")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	binPath = filepath.Join(dir, "tallyfy")
	// Build from the repo root (two levels up from test/e2e).
	build := exec.Command("go", "build", "-o", binPath, "./cmd/tallyfy")
	build.Dir = "../.."
	build.Env = os.Environ()
	if out, berr := build.CombinedOutput(); berr != nil {
		panic("build failed: " + berr.Error() + "\n" + string(out))
	}
	os.Exit(m.Run())
}

// --- mock API ---------------------------------------------------------------

const e2eToken = "e2e-token"

type mockAPI struct {
	mu             sync.Mutex
	sawGoodHeaders bool
	runPosts       int
	deletes        int
	completePosts  int    // POSTs to /runs/{run}/completed-tasks
	lastRunBody    string // most recent POST /runs request body
}

func newMockAPI() (*mockAPI, *httptest.Server) {
	api := &mockAPI{}
	srv := httptest.NewServer(http.HandlerFunc(api.handle))
	return api, srv
}

func (a *mockAPI) handle(w http.ResponseWriter, r *http.Request) {
	// Enforce the three mandatory headers on EVERY request.
	if r.Header.Get("Accept") != "application/json" ||
		r.Header.Get("X-Tallyfy-Client") != "APIClient" ||
		!strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		writeJSON(w, 401, `{"error":true,"message":"missing required headers"}`)
		return
	}
	// Enforce the token value (wrong token -> 401 -> exit 3).
	if r.Header.Get("Authorization") != "Bearer "+e2eToken {
		writeJSON(w, 401, `{"error":true,"message":"invalid token"}`)
		return
	}
	a.mu.Lock()
	a.sawGoodHeaders = true
	a.mu.Unlock()

	path := r.URL.Path
	switch {
	case r.Method == "GET" && path == "/me":
		writeJSON(w, 200, `{"data":{"id":123,"email":"e2e@example.com","first_name":"E","last_name":"2E"}}`)
	case r.Method == "GET" && path == "/me/organizations":
		writeJSON(w, 200, `{"data":[{"id":"org_test","name":"E2E Org","created_at":"2026-01-01"}]}`)
	case r.Method == "GET" && path == "/organizations/org_test/checklists":
		writeJSON(w, 200, `{"data":[{"id":"bp_1","title":"Onboarding","status":"published"},{"id":"bp_2","title":"Offboarding","status":"draft"}],"meta":{"pagination":{"total":2,"count":2,"per_page":20,"current_page":1,"total_pages":1}}}`)
	case r.Method == "GET" && path == "/organizations/org_test/checklists/bp_404":
		writeJSON(w, 404, `{"error":true,"message":"blueprint not found"}`)
	case r.Method == "GET" && path == "/organizations/org_test/checklists/bp_1":
		writeJSON(w, 200, `{"data":{"id":"bp_1","title":"Onboarding","status":"published"}}`)
	case r.Method == "DELETE" && strings.HasPrefix(path, "/organizations/org_test/checklists/"):
		a.mu.Lock()
		a.deletes++
		a.mu.Unlock()
		w.WriteHeader(204)
	case r.Method == "POST" && path == "/organizations/org_test/runs":
		body := readBody(r)
		a.mu.Lock()
		a.runPosts++
		a.lastRunBody = body
		a.mu.Unlock()
		// Rows whose name contains "fail" get a 422 (drives the bulk-partial test).
		if strings.Contains(strings.ToLower(body), "fail") {
			writeJSON(w, 422, `{"error":true,"message":"validation failed"}`)
			return
		}
		writeJSON(w, 201, `{"data":{"id":"run_9","name":"launched","status":"active"}}`)
	case r.Method == "GET" && path == "/organizations/org_test/runs/run_1/tasks":
		writeJSON(w, 200, `{"data":[{"id":"task_1","title":"Approve","status":"active"},{"id":"task_2","title":"Sign","status":"completed"}]}`)
	case r.Method == "POST" && path == "/organizations/org_test/runs/run_1/completed-tasks":
		a.mu.Lock()
		a.completePosts++
		a.mu.Unlock()
		writeJSON(w, 200, `{"data":{"id":"task_1","title":"Approve","status":"completed"}}`)
	case r.Method == "POST" && path == "/organizations/org_test/tags":
		writeJSON(w, 422, `{"error":true,"message":"title already taken"}`)
	default:
		writeJSON(w, 404, `{"error":true,"message":"no mock route: `+r.Method+" "+path+`"}`)
	}
}

func writeJSON(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}

func readBody(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

// --- run helper -------------------------------------------------------------

type result struct {
	stdout string
	stderr string
	code   int
}

// run executes the compiled binary against baseURL with the standard test
// credentials, plus any extra args. token overrides the api-key when non-empty.
func run(t *testing.T, baseURL, token string, args ...string) result {
	t.Helper()
	if token == "" {
		token = e2eToken
	}
	full := append([]string{
		"--base-url", baseURL,
		"--api-key", token,
		"--org", "org_test",
		"--no-input",
	}, args...)
	cmd := exec.Command(binPath, full...)
	cmd.Env = append(os.Environ(),
		"TALLYFY_NO_UPDATE_CHECK=1",
		"TALLYFY_API_TOKEN=", // force the flag path, not a stray env token
	)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("failed to run binary: %v", err)
		}
	}
	return result{stdout: out.String(), stderr: errb.String(), code: code}
}

// --- tests ------------------------------------------------------------------

func TestHeadersEnforcedAndWhoami(t *testing.T) {
	api, srv := newMockAPI()
	defer srv.Close()

	res := run(t, srv.URL, "", "whoami")
	if res.code != 0 {
		t.Fatalf("whoami exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "e2e@example.com") {
		t.Errorf("whoami stdout missing email: %q", res.stdout)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if !api.sawGoodHeaders {
		t.Error("mock never saw a request with all three mandatory headers")
	}
}

func TestBlueprintListTable(t *testing.T) {
	_, srv := newMockAPI()
	defer srv.Close()
	res := run(t, srv.URL, "", "blueprint", "list")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	for _, want := range []string{"bp_1", "Onboarding", "bp_2", "Offboarding"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("table output missing %q; got:\n%s", want, res.stdout)
		}
	}
}

func TestBlueprintListJSON(t *testing.T) {
	_, srv := newMockAPI()
	defer srv.Close()
	res := run(t, srv.URL, "", "blueprint", "list", "-o", "json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	var parsed any
	if err := json.Unmarshal([]byte(res.stdout), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, res.stdout)
	}
}

func TestExitNotFound(t *testing.T) {
	_, srv := newMockAPI()
	defer srv.Close()
	res := run(t, srv.URL, "", "blueprint", "get", "bp_404")
	if res.code != 5 {
		t.Fatalf("exit = %d, want 5 (not found); stderr=%s", res.code, res.stderr)
	}
}

func TestExitValidation(t *testing.T) {
	_, srv := newMockAPI()
	defer srv.Close()
	// create is a mutation: under the default "ask" mode a non-interactive run
	// needs --yes to reach the API (where the mock returns 422 -> exit 7).
	res := run(t, srv.URL, "", "--yes", "tag", "create", "--title", "dupe")
	if res.code != 7 {
		t.Fatalf("exit = %d, want 7 (validation); stderr=%s", res.code, res.stderr)
	}
}

func TestExitAuth(t *testing.T) {
	_, srv := newMockAPI()
	defer srv.Close()
	res := run(t, srv.URL, "wrong-token", "whoami")
	if res.code != 3 {
		t.Fatalf("exit = %d, want 3 (auth); stderr=%s", res.code, res.stderr)
	}
}

func TestDryRunMakesNoMutation(t *testing.T) {
	api, srv := newMockAPI()
	defer srv.Close()
	res := run(t, srv.URL, "", "process", "launch", "--blueprint", "bp_1", "--name", "Preview", "--dry-run")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "[dry-run]") {
		t.Errorf("dry-run output missing [dry-run] marker: %q", res.stdout)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.runPosts != 0 {
		t.Errorf("dry-run made %d POST(s) to /runs, want 0", api.runPosts)
	}
}

func TestPermissionDenyWinsOverYes(t *testing.T) {
	api, srv := newMockAPI()
	defer srv.Close()
	// A deny rule must block even with --yes, and must not reach the API.
	res := run(t, srv.URL, "",
		"--settings", `{"permissions":{"deny":["Blueprint(delete)"]}}`,
		"--yes", "blueprint", "delete", "bp_1")
	if res.code != 4 {
		t.Fatalf("exit = %d, want 4 (permission); stderr=%s", res.code, res.stderr)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.deletes != 0 {
		t.Errorf("denied delete still hit the API %d time(s)", api.deletes)
	}
}

func TestBulkPartialExit9(t *testing.T) {
	api, srv := newMockAPI()
	defer srv.Close()

	csv := "name\nGood One\nfail row\nGood Two\n"
	csvPath := filepath.Join(t.TempDir(), "hires.csv")
	if err := os.WriteFile(csvPath, []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}
	res := run(t, srv.URL, "",
		"--settings", `{"permissions":{"allow":["Process(launch)"]}}`,
		"process", "launch", "--blueprint", "bp_1", "--from-csv", csvPath)
	if res.code != 9 {
		t.Fatalf("exit = %d, want 9 (bulk partial); stderr=%s\nstdout=%s", res.code, res.stderr, res.stdout)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.runPosts != 3 {
		t.Errorf("bulk launch made %d POSTs, want 3 (one per row)", api.runPosts)
	}
}

func TestProcessLaunchSingleSuccess(t *testing.T) {
	api, srv := newMockAPI()
	defer srv.Close()

	// launch is a mutation: the ask default needs --yes non-interactively.
	res := run(t, srv.URL, "", "process", "launch", "--blueprint", "bp_1", "--name", "Solo", "--yes")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	// The mock returns the launched run as run_9; the result table must show it.
	if !strings.Contains(res.stdout, "run_9") {
		t.Errorf("launch stdout missing run id run_9: %q", res.stdout)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.runPosts != 1 {
		t.Errorf("single launch made %d POST(s) to /runs, want 1", api.runPosts)
	}
	// json.Marshal of the payload map orders keys alphabetically and emits no
	// spaces, so the raw body contains this exact substring.
	if !strings.Contains(api.lastRunBody, `"checklist_id":"bp_1"`) {
		t.Errorf("POST /runs body missing checklist_id bp_1: %q", api.lastRunBody)
	}
}

func TestTaskListInProcess(t *testing.T) {
	_, srv := newMockAPI()
	defer srv.Close()

	res := run(t, srv.URL, "", "task", "list", "--process", "run_1")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	for _, want := range []string{"task_1", "Approve"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("task list output missing %q; got:\n%s", want, res.stdout)
		}
	}
}

func TestTaskCompleteInProcess(t *testing.T) {
	api, srv := newMockAPI()
	defer srv.Close()

	// complete is a mutation: the ask default needs --yes non-interactively.
	res := run(t, srv.URL, "", "task", "complete", "task_1", "--process", "run_1", "--yes")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.completePosts != 1 {
		t.Errorf("complete made %d POST(s) to /runs/run_1/completed-tasks, want exactly 1", api.completePosts)
	}
}

func TestOrgList(t *testing.T) {
	_, srv := newMockAPI()
	defer srv.Close()

	res := run(t, srv.URL, "", "org", "list")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	for _, want := range []string{"org_test", "E2E Org"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("org list output missing %q; got:\n%s", want, res.stdout)
		}
	}
}

func TestApiPassthroughGetMe(t *testing.T) {
	_, srv := newMockAPI()
	defer srv.Close()

	// The api command guards as Api(request) — not a read verb in the
	// permission engine — so the ask default needs --yes non-interactively
	// even for a GET. The mock's header enforcement (401 on any missing
	// header -> exit 3) proves the raw passthrough sends all three mandatory
	// headers too.
	res := run(t, srv.URL, "", "--yes", "api", "GET", "me")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "e2e@example.com") {
		t.Errorf("api GET me output missing email; got:\n%s", res.stdout)
	}
}

func TestConfigListLocalOnly(t *testing.T) {
	// config list resolves layered settings locally: point the CLI at a dead
	// URL to prove no network round-trip and no auth resolution are needed.
	res := run(t, "http://127.0.0.1:1", "", "config", "list")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	if strings.TrimSpace(res.stdout) == "" {
		t.Fatal("config list produced empty stdout")
	}
	for _, want := range []string{"output", "baseUrl"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("config list output missing key %q; got:\n%s", want, res.stdout)
		}
	}
}

func TestOutputColumnsPresent(t *testing.T) {
	_, srv := newMockAPI()
	defer srv.Close()

	res := run(t, srv.URL, "", "blueprint", "list")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
	if len(lines) == 0 {
		t.Fatal("blueprint list produced no output lines")
	}
	// blueprint list renders columns ID, TITLE, STATUS, STEPS, UPDATED
	// (internal/cli/blueprint.go); the table renderer prints the uppercased
	// header as the first line.
	header := lines[0]
	for _, want := range []string{"ID", "TITLE", "STATUS"} {
		if !strings.Contains(header, want) {
			t.Errorf("table header missing column %q; header line: %q", want, header)
		}
	}
}
