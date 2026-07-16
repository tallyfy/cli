//go:build live

package live

// Package live is the LIVE integration suite for the tallyfy CLI. It executes
// the real compiled binary against a real (staging-only) Tallyfy API and
// therefore NEVER runs in normal CI:
//
//   - it compiles only under `go test -tags live` (see the build tag above), and
//   - every test skips unless ALL of these env vars are set:
//       TALLYFY_E2E=1
//       TALLYFY_E2E_CREDENTIALS=/path/to/creds.json
//       TALLYFY_E2E_CONFIRM_ORG=<org id, must equal the creds file's org_id>
//
// Credentials file shape (TALLYFY_E2E_CREDENTIALS):
//
//	{
//	  "api_endpoint": "https://staging.go.tallyfy.com/api",
//	  "org_id":       "...",
//	  "user_id":      "...",
//	  "access_token": "..."
//	}
//
// Safety rails (all enforced in liveSetup before any request is made):
//
//	RAIL 1 — host allowlist: the api_endpoint host must be one of
//	         allowedHosts (staging only). Production hosts hard-fail.
//	RAIL 2 — org confirm: TALLYFY_E2E_CONFIRM_ORG must equal the creds
//	         file's org_id, so the operator explicitly names the org
//	         this suite is allowed to mutate.
//	RAIL 3 — fixture prefix: every object this suite creates is named
//	         with fixturePrefix() ("cli-e2e-<unixnano>-..."). Mutations
//	         MUST refuse to touch any name/title that does not start
//	         with the "cli-e2e-" prefix (see requireFixtureName).
//
// Run it deliberately:
//
//	TALLYFY_E2E=1 \
//	TALLYFY_E2E_CREDENTIALS=$HOME/tallyfy-staging-creds.json \
//	TALLYFY_E2E_CONFIRM_ORG=<org id> \
//	go test -tags live ./test/live/... -v

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// binPath is the compiled CLI binary, built once in TestMain (same pattern as
// the mock suite in test/e2e).
var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tallyfy-live")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	binPath = filepath.Join(dir, "tallyfy")
	// Build from the repo root (two levels up from test/live).
	build := exec.Command("go", "build", "-o", binPath, "./cmd/tallyfy")
	build.Dir = "../.."
	build.Env = os.Environ()
	if out, berr := build.CombinedOutput(); berr != nil {
		panic("build failed: " + berr.Error() + "\n" + string(out))
	}
	os.Exit(m.Run())
}

// --- gate + credentials + safety rails ---------------------------------------

// credentials mirrors the JSON file at TALLYFY_E2E_CREDENTIALS.
type credentials struct {
	APIEndpoint string `json:"api_endpoint"`
	OrgID       string `json:"org_id"`
	UserID      string `json:"user_id"`
	AccessToken string `json:"access_token"`
}

// allowedHosts is SAFETY RAIL 1: the only API hosts this suite will ever talk
// to. Staging only — NEVER add a production host to this list.
var allowedHosts = []string{"staging.go.tallyfy.com", "staging-api.tallyfy.com"}

// sweepPrefix is the stable marker (RAIL 3) every fixture name starts with;
// fixturePrefix() appends a per-run timestamp so parallel/aborted runs never
// collide and leftovers are attributable.
const sweepPrefix = "cli-e2e-"

// liveCreds is populated by liveSetup and consumed by runCLI. Tests in this
// package do not run in parallel, and every test loads the same file, so a
// package-level cache is safe.
var liveCreds *credentials

// requireLiveEnv gates the suite: it SKIPS (never fails) unless all three
// opt-in env vars are present. Called at the top of every test via liveSetup.
func requireLiveEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("TALLYFY_E2E") != "1" {
		t.Skip("live suite disabled: set TALLYFY_E2E=1 to opt in")
	}
	if os.Getenv("TALLYFY_E2E_CREDENTIALS") == "" {
		t.Skip("live suite disabled: set TALLYFY_E2E_CREDENTIALS to a staging credentials JSON file")
	}
	if os.Getenv("TALLYFY_E2E_CONFIRM_ORG") == "" {
		t.Skip("live suite disabled: set TALLYFY_E2E_CONFIRM_ORG to the org id you intend to mutate")
	}
}

// liveSetup runs the gate and ALL safety rails, returning the loaded
// credentials. Every test calls this first.
func liveSetup(t *testing.T) *credentials {
	t.Helper()
	requireLiveEnv(t)

	path := os.Getenv("TALLYFY_E2E_CREDENTIALS")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read TALLYFY_E2E_CREDENTIALS %q: %v", path, err)
	}
	var c credentials
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("parse TALLYFY_E2E_CREDENTIALS %q: %v", path, err)
	}
	if c.APIEndpoint == "" || c.OrgID == "" || c.AccessToken == "" {
		t.Fatalf("credentials file %q missing api_endpoint, org_id, or access_token", path)
	}

	// SAFETY RAIL 1: hard host allowlist — staging only, never production.
	u, err := url.Parse(c.APIEndpoint)
	if err != nil {
		t.Fatalf("api_endpoint %q is not a valid URL: %v", c.APIEndpoint, err)
	}
	host := u.Hostname()
	allowed := false
	for _, h := range allowedHosts {
		if host == h {
			allowed = true
			break
		}
	}
	if !allowed {
		t.Fatalf("REFUSING to run: api_endpoint host %q is not in the staging allowlist %v — this suite never touches production", host, allowedHosts)
	}

	// SAFETY RAIL 2: the operator must explicitly name the org being mutated.
	if confirm := os.Getenv("TALLYFY_E2E_CONFIRM_ORG"); confirm != c.OrgID {
		t.Fatalf("REFUSING to run: TALLYFY_E2E_CONFIRM_ORG=%q does not match the credentials file's org_id %q", confirm, c.OrgID)
	}

	liveCreds = &c
	return &c
}

// fixturePrefix returns the per-run fixture name prefix (SAFETY RAIL 3).
// Every object this suite creates MUST have a name/title starting with this
// prefix, and mutations must refuse to operate on non-prefixed names.
func fixturePrefix() string {
	return fmt.Sprintf("%s%d-", sweepPrefix, time.Now().UnixNano())
}

// requireFixtureName enforces RAIL 3 at the call site: any name about to be
// used in a create/launch mutation must carry the fixture marker.
func requireFixtureName(t *testing.T, name string) {
	t.Helper()
	if !strings.HasPrefix(name, sweepPrefix) {
		t.Fatalf("REFUSING mutation: name %q does not start with the fixture prefix %q", name, sweepPrefix)
	}
}

// --- CLI runner ---------------------------------------------------------------

type result struct {
	stdout string
	stderr string
	code   int
}

// runCLI executes the compiled binary against the staging endpoint from the
// credentials file, with auth/org/non-interactive flags pre-applied.
// liveSetup must have been called first (it populates liveCreds).
func runCLI(t *testing.T, args ...string) result {
	t.Helper()
	if liveCreds == nil {
		t.Fatal("runCLI called before liveSetup")
	}
	full := append([]string{
		"--base-url", liveCreds.APIEndpoint,
		"--api-key", liveCreds.AccessToken,
		"--org", liveCreds.OrgID,
		"--no-input",
		"--yes",
	}, args...)
	cmd := exec.Command(binPath, full...)
	cmd.Env = append(os.Environ(),
		"TALLYFY_NO_UPDATE_CHECK=1",
		"TALLYFY_API_TOKEN=", // force the --api-key flag path, not a stray env token
		"TALLYFY_BASE_URL=",  // force the --base-url flag path, not a stray env URL
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

// extractID pulls the object id out of `-o json` stdout. The CLI prints the
// UNWRAPPED transformer item (pkg/tallyfy doItem strips the {"data": ...}
// envelope and internal/output renderJSON emits a single item directly), so
// the normal shape is {"id": "..."} at the top level; a {"data": {"id": ...}}
// envelope is tolerated for robustness. Blueprint.ID and Process.ID are both
// strings per pkg/tallyfy/types.go.
func extractID(t *testing.T, stdout string) string {
	t.Helper()
	var v struct {
		ID   string `json:"id"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("output is not a JSON object: %v\n%s", err, stdout)
	}
	if v.ID != "" {
		return v.ID
	}
	if v.Data.ID != "" {
		return v.Data.ID
	}
	t.Fatalf("no id found in JSON output:\n%s", stdout)
	return "" // unreachable
}

// --- tests --------------------------------------------------------------------

func TestLiveWhoami(t *testing.T) {
	liveSetup(t)

	res := runCLI(t, "whoami", "-o", "json")
	if res.code != 0 {
		t.Fatalf("whoami exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	var who struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &who); err != nil {
		t.Fatalf("whoami output is not valid JSON: %v\n%s", err, res.stdout)
	}
	if who.Email == "" {
		t.Fatalf("whoami JSON has empty email:\n%s", res.stdout)
	}
	t.Logf("authenticated as %s", who.Email)
}

func TestLiveOrgList(t *testing.T) {
	c := liveSetup(t)

	res := runCLI(t, "org", "list", "--all", "-o", "json")
	if res.code != 0 {
		t.Fatalf("org list exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	var parsed any
	if err := json.Unmarshal([]byte(res.stdout), &parsed); err != nil {
		t.Fatalf("org list output is not valid JSON: %v\n%s", err, res.stdout)
	}
	// renderJSON emits a single object for exactly one org and an array
	// otherwise; a substring check on the raw stdout covers both shapes.
	if !strings.Contains(res.stdout, c.OrgID) {
		t.Fatalf("configured org %q not present in org list output:\n%s", c.OrgID, res.stdout)
	}
}

func TestLiveBlueprintList(t *testing.T) {
	liveSetup(t)

	res := runCLI(t, "blueprint", "list", "-o", "json")
	if res.code != 0 {
		t.Fatalf("blueprint list exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	var parsed any
	if err := json.Unmarshal([]byte(res.stdout), &parsed); err != nil {
		t.Fatalf("blueprint list output is not valid JSON: %v\n%s", err, res.stdout)
	}
}

// TestLiveFullFlow exercises the full create -> launch -> tasks -> archive ->
// delete lifecycle against staging, with cleanup registered via t.Cleanup so
// the blueprint (and, best-effort, the process) is removed even when a
// mid-test assertion fails. Every created object name carries fixturePrefix()
// (RAIL 3).
func TestLiveFullFlow(t *testing.T) {
	liveSetup(t)
	prefix := fixturePrefix()

	// 1. Create a blueprint.
	bpTitle := prefix + "flow"
	requireFixtureName(t, bpTitle)
	res := runCLI(t, "blueprint", "create", "--title", bpTitle, "-o", "json")
	if res.code != 0 {
		t.Fatalf("blueprint create exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	bpID := extractID(t, res.stdout)
	t.Logf("created blueprint %s (%s)", bpID, bpTitle)

	// Cleanup net: delete the blueprint even if any later assertion fails.
	// It only ever targets the prefixed fixture created above. Best-effort:
	// a failure here is logged, not asserted (the primary-path delete below
	// is the asserted one; after it succeeds this cleanup is skipped).
	bpDeleted := false
	t.Cleanup(func() {
		if bpDeleted {
			return
		}
		del := runCLI(t, "blueprint", "delete", bpID)
		t.Logf("cleanup: delete blueprint %s -> exit %d (stderr=%s)", bpID, del.code, strings.TrimSpace(del.stderr))
	})

	// 2. Launch a process from it.
	procName := prefix + "run"
	requireFixtureName(t, procName)
	res = runCLI(t, "process", "launch", "--blueprint", bpID, "--name", procName, "-o", "json")
	if res.code != 0 {
		t.Fatalf("process launch exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	procID := extractID(t, res.stdout)
	t.Logf("launched process %s (%s)", procID, procName)

	// Cleanup net for the process (t.Cleanup is LIFO, so this archive runs
	// before the blueprint delete above).
	procArchived := false
	t.Cleanup(func() {
		if procArchived {
			return
		}
		arch := runCLI(t, "process", "archive", procID)
		t.Logf("cleanup: archive process %s -> exit %d (stderr=%s)", procID, arch.code, strings.TrimSpace(arch.stderr))
	})

	// 3. List the tasks in that process.
	res = runCLI(t, "task", "list", "--process", procID, "-o", "json")
	if res.code != 0 {
		t.Fatalf("task list --process exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	var tasks any
	if err := json.Unmarshal([]byte(res.stdout), &tasks); err != nil {
		t.Fatalf("task list output is not valid JSON: %v\n%s", err, res.stdout)
	}

	// 4. Archive the process.
	res = runCLI(t, "process", "archive", procID)
	if res.code != 0 {
		t.Fatalf("process archive exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	procArchived = true

	// 5. Delete the blueprint.
	res = runCLI(t, "blueprint", "delete", bpID)
	if res.code != 0 {
		t.Fatalf("blueprint delete exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}
	bpDeleted = true
}

// TestLiveSweeper is a non-destructive helper, not a mutation test: it lists
// blueprints and logs any whose title carries the sweepPrefix so an operator
// can find leftovers from aborted runs. It deliberately deletes NOTHING —
// other runs' fixtures are never auto-deleted.
func TestLiveSweeper(t *testing.T) {
	liveSetup(t)

	res := runCLI(t, "blueprint", "list", "--all", "-o", "json")
	if res.code != 0 {
		t.Fatalf("blueprint list exit = %d, want 0; stderr=%s", res.code, res.stderr)
	}

	type bpItem struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	var items []bpItem
	if err := json.Unmarshal([]byte(res.stdout), &items); err != nil {
		// renderJSON emits a bare object when the list has exactly one item.
		var single bpItem
		if err2 := json.Unmarshal([]byte(res.stdout), &single); err2 != nil {
			t.Fatalf("blueprint list output is neither a JSON array nor an object: %v / %v\n%s", err, err2, res.stdout)
		}
		items = []bpItem{single}
	}

	leftovers := 0
	for _, b := range items {
		if strings.HasPrefix(b.Title, sweepPrefix) {
			leftovers++
			t.Logf("leftover fixture blueprint: %s %q (delete manually with: tallyfy blueprint delete %s)", b.ID, b.Title, b.ID)
		}
	}
	t.Logf("sweep: %d blueprint(s) scanned, %d leftover fixture(s) with prefix %q", len(items), leftovers, sweepPrefix)
}
