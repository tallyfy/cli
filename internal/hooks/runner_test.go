package hooks

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tallyfy/cli/internal/config"
)

func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("exec hook test fixtures are /bin/sh scripts")
	}
}

// writeScript drops an executable /bin/sh fixture into a temp dir and
// returns its single-quoted path, ready to use as a hook command.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return "'" + path + "'"
}

func testPayload() Payload {
	return Payload{
		Event:     PreDelete,
		Resource:  "blueprint",
		ID:        "bp_123",
		Org:       "org_abc",
		Args:      map[string]any{"force": false},
		User:      "u_1",
		Timestamp: time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC),
	}
}

func TestExecHookReceivesPayloadOnStdin(t *testing.T) {
	skipIfWindows(t)
	out := filepath.Join(t.TempDir(), "payload.json")
	cmd := writeScript(t, "cat > '"+out+"'")

	warns, err := NewRunner(Options{}).Fire(PreDelete, []config.Hook{{Command: cmd}}, testPayload())
	if err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("hook did not write stdin capture: %v", err)
	}
	var got struct {
		Event     string         `json:"event"`
		Resource  string         `json:"resource"`
		ID        string         `json:"id"`
		Org       string         `json:"org"`
		Args      map[string]any `json:"args"`
		User      string         `json:"user"`
		Timestamp string         `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("hook stdin was not valid JSON: %v\n%s", err, data)
	}
	if got.Event != "PreDelete" || got.Resource != "blueprint" || got.ID != "bp_123" ||
		got.Org != "org_abc" || got.User != "u_1" {
		t.Errorf("payload fields = %+v, want the testPayload values", got)
	}
	if force, ok := got.Args["force"].(bool); !ok || force {
		t.Errorf("args.force = %v, want false", got.Args["force"])
	}
	if got.Timestamp != "2026-07-05T12:00:00Z" {
		t.Errorf("timestamp = %q, want RFC3339 UTC 2026-07-05T12:00:00Z", got.Timestamp)
	}
}

func TestExecHookEnvInjection(t *testing.T) {
	skipIfWindows(t)
	t.Setenv("TALLYFY_HOOK_TEST", "from-os")
	out := filepath.Join(t.TempDir(), "env.txt")
	cmd := writeScript(t, `printf '%s' "$TALLYFY_HOOK_TEST" > '`+out+`'`)

	opts := Options{Env: map[string]string{"TALLYFY_HOOK_TEST": "from-options"}}
	if _, err := NewRunner(opts).Fire(PostLaunch, []config.Hook{{Command: cmd}}, testPayload()); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("hook did not run: %v", err)
	}
	if string(data) != "from-options" {
		t.Errorf("hook env TALLYFY_HOOK_TEST = %q, want settings env to override os env", data)
	}
}

func TestPreHookNonZeroBlocksWithStderr(t *testing.T) {
	skipIfWindows(t)
	cmd := writeScript(t, "echo 'boom reason' >&2\nexit 3")

	warns, err := NewRunner(Options{}).Fire(PreDelete, []config.Hook{{Command: cmd}}, testPayload())
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	var be *BlockError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v (%T), want *BlockError", err, err)
	}
	if be.HookDesc != cmd {
		t.Errorf("HookDesc = %q, want the hook command %q", be.HookDesc, cmd)
	}
	if !strings.Contains(be.Stderr, "boom reason") {
		t.Errorf("Stderr = %q, want captured 'boom reason'", be.Stderr)
	}
	if !strings.Contains(err.Error(), "blocked by hook") {
		t.Errorf("Error() = %q, want 'blocked by hook ...'", err.Error())
	}
}

func TestPreHookBlockStopsRemainingHooks(t *testing.T) {
	skipIfWindows(t)
	fail := writeScript(t, "exit 1")
	marker := filepath.Join(t.TempDir(), "marker")
	second := writeScript(t, "touch '"+marker+"'")

	_, err := NewRunner(Options{}).Fire(PreArchive,
		[]config.Hook{{Command: fail}, {Command: second}}, testPayload())
	var be *BlockError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want *BlockError", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Error("second hook ran after a Pre* block; blocking must stop remaining hooks")
	}
}

func TestPostHookNonZeroOnlyWarns(t *testing.T) {
	skipIfWindows(t)
	cmd := writeScript(t, "echo 'boom reason' >&2\nexit 3")

	warns, err := NewRunner(Options{}).Fire(PostDelete, []config.Hook{{Command: cmd}}, testPayload())
	if err != nil {
		t.Fatalf("Post* hook failure must be advisory, got err %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warns)
	}
	if !strings.Contains(warns[0], "failed") || !strings.Contains(warns[0], "boom reason") {
		t.Errorf("warning %q should mention the failure and captured stderr", warns[0])
	}
}

func TestProjectScopeHookSkippedWhenUntrusted(t *testing.T) {
	skipIfWindows(t)
	marker := filepath.Join(t.TempDir(), "marker")
	cmd := writeScript(t, "touch '"+marker+"'")
	h := []config.Hook{{Command: cmd, Scope: config.ScopeProject}}

	warns, err := NewRunner(Options{WorkspaceTrusted: false}).Fire(PreLaunch, h, testPayload())
	if err != nil {
		t.Fatalf("Fire: %v", err)
	}
	want := "skipping project-scope hook (workspace not trusted; run `tallyfy trust`)"
	if len(warns) != 1 || warns[0] != want {
		t.Errorf("warnings = %v, want exactly [%q]", warns, want)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Error("project-scope hook executed in an untrusted workspace")
	}
}

func TestProjectScopeHookRunsWhenTrusted(t *testing.T) {
	skipIfWindows(t)
	marker := filepath.Join(t.TempDir(), "marker")
	cmd := writeScript(t, "touch '"+marker+"'")
	h := []config.Hook{{Command: cmd, Scope: config.ScopeProject}}

	warns, err := NewRunner(Options{WorkspaceTrusted: true}).Fire(PreLaunch, h, testPayload())
	if err != nil || len(warns) != 0 {
		t.Fatalf("Fire: err=%v warns=%v", err, warns)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Error("project-scope hook did not run in a trusted workspace")
	}
}

func TestMatcherFiltersHooks(t *testing.T) {
	skipIfWindows(t)
	dir := t.TempDir()
	matched := filepath.Join(dir, "matched")
	unmatched := filepath.Join(dir, "unmatched")
	hs := []config.Hook{
		{Matcher: "Blueprint(*)", Command: writeScript(t, "touch '"+matched+"'")},
		{Matcher: "Task(*)", Command: writeScript(t, "touch '"+unmatched+"'")},
	}
	opts := Options{MatchToken: func(m string) (bool, error) { return m == "Blueprint(*)", nil }}

	warns, err := NewRunner(opts).Fire(PreDelete, hs, testPayload())
	if err != nil || len(warns) != 0 {
		t.Fatalf("Fire: err=%v warns=%v", err, warns)
	}
	if _, statErr := os.Stat(matched); statErr != nil {
		t.Error("hook with matching matcher did not run")
	}
	if _, statErr := os.Stat(unmatched); !os.IsNotExist(statErr) {
		t.Error("hook with non-matching matcher ran")
	}
}

func TestInvalidMatcherSkipsWithWarning(t *testing.T) {
	skipIfWindows(t)
	marker := filepath.Join(t.TempDir(), "marker")
	hs := []config.Hook{{Matcher: "Blueprint(", Command: writeScript(t, "touch '"+marker+"'")}}
	opts := Options{MatchToken: func(_ string) (bool, error) { return false, errors.New("bad matcher") }}

	warns, err := NewRunner(opts).Fire(PreDelete, hs, testPayload())
	if err != nil {
		t.Fatalf("invalid matcher must skip, not block: %v", err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "invalid matcher") {
		t.Errorf("warnings = %v, want one 'invalid matcher' warning", warns)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Error("hook with invalid matcher ran")
	}
}

func TestNilMatchTokenMatchesAll(t *testing.T) {
	skipIfWindows(t)
	marker := filepath.Join(t.TempDir(), "marker")
	hs := []config.Hook{{Matcher: "Task(*)", Command: writeScript(t, "touch '"+marker+"'")}}

	if _, err := NewRunner(Options{}).Fire(PostComplete, hs, testPayload()); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Error("nil MatchToken must match all hooks; hook did not run")
	}
}

func TestTimeoutBlocksPre(t *testing.T) {
	skipIfWindows(t)
	cmd := writeScript(t, "sleep 5")
	start := time.Now()

	_, err := NewRunner(Options{Timeout: 150 * time.Millisecond}).
		Fire(PreLaunch, []config.Hook{{Command: cmd}}, testPayload())
	elapsed := time.Since(start)

	var be *BlockError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want *BlockError on timeout", err)
	}
	if !strings.Contains(be.Stderr, "timed out") {
		t.Errorf("Stderr = %q, want a timeout mention", be.Stderr)
	}
	if elapsed > 4*time.Second {
		t.Errorf("hook was not killed by the timeout (took %s)", elapsed)
	}
}

func TestTimeoutOnlyWarnsOnPost(t *testing.T) {
	skipIfWindows(t)
	cmd := writeScript(t, "sleep 5")

	warns, err := NewRunner(Options{Timeout: 150 * time.Millisecond}).
		Fire(PostLaunch, []config.Hook{{Command: cmd}}, testPayload())
	if err != nil {
		t.Fatalf("Post* timeout must be advisory, got %v", err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "timed out") {
		t.Errorf("warnings = %v, want one timeout warning", warns)
	}
}

func TestDefaultTimeoutApplied(t *testing.T) {
	r, ok := NewRunner(Options{}).(*runner)
	if !ok {
		t.Fatal("NewRunner did not return *runner")
	}
	if r.opts.Timeout != 30*time.Second {
		t.Errorf("default Timeout = %s, want 30s", r.opts.Timeout)
	}
	r2 := NewRunner(Options{Timeout: 5 * time.Second}).(*runner)
	if r2.opts.Timeout != 5*time.Second {
		t.Errorf("explicit Timeout = %s, want 5s", r2.opts.Timeout)
	}
}

func TestStderrCappedAt4KiB(t *testing.T) {
	skipIfWindows(t)
	cmd := writeScript(t, `i=0
while [ $i -lt 300 ]; do
  echo "0123456789012345678901234567890123456789" >&2
  i=$((i+1))
done
exit 1`)

	_, err := NewRunner(Options{}).Fire(PreImport, []config.Hook{{Command: cmd}}, testPayload())
	var be *BlockError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want *BlockError", err)
	}
	if len(be.Stderr) == 0 || len(be.Stderr) > stderrCap {
		t.Errorf("captured stderr length = %d, want (0, %d]", len(be.Stderr), stderrCap)
	}
}

func TestUnsupportedHookTypeWarns(t *testing.T) {
	hs := []config.Hook{{Type: "pigeon", Command: "true"}}
	warns, err := NewRunner(Options{}).Fire(PreDelete, hs, testPayload())
	if err != nil {
		t.Fatalf("unsupported type must skip, not block: %v", err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], `unsupported type "pigeon"`) {
		t.Errorf("warnings = %v, want one unsupported-type warning", warns)
	}
}

func TestEmptyCommandExecHookWarns(t *testing.T) {
	warns, err := NewRunner(Options{}).Fire(PreDelete, []config.Hook{{}}, testPayload())
	if err != nil {
		t.Fatalf("empty command must skip, not block: %v", err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "no command") {
		t.Errorf("warnings = %v, want one no-command warning", warns)
	}
}

func TestNoHooksIsNoop(t *testing.T) {
	warns, err := NewRunner(Options{}).Fire(PreDelete, nil, testPayload())
	if warns != nil || err != nil {
		t.Errorf("Fire with no hooks = (%v, %v), want (nil, nil)", warns, err)
	}
}

func TestWarningsCollectedBeforeBlock(t *testing.T) {
	skipIfWindows(t)
	fail := writeScript(t, "exit 1")
	hs := []config.Hook{
		{Command: "true", Scope: config.ScopeProject}, // skipped: untrusted
		{Command: fail},
	}
	warns, err := NewRunner(Options{}).Fire(PreDelete, hs, testPayload())
	var be *BlockError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want *BlockError", err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "workspace not trusted") {
		t.Errorf("warnings before the block = %v, want the project-scope skip warning", warns)
	}
}

func TestPayloadMarshalFailure(t *testing.T) {
	p := testPayload()
	p.Args = map[string]any{"bad": make(chan int)} // channels cannot marshal
	hs := []config.Hook{{Command: "true"}}

	// Pre*: fail closed.
	_, err := NewRunner(Options{}).Fire(PreDelete, hs, p)
	if err == nil {
		t.Error("Pre* with unmarshalable payload must fail closed")
	}
	var be *BlockError
	if errors.As(err, &be) {
		t.Error("marshal failure is not a hook block; want a plain error")
	}

	// Advisory events: warn only.
	warns, err := NewRunner(Options{}).Fire(PostDelete, hs, p)
	if err != nil {
		t.Errorf("Post* with unmarshalable payload must not error: %v", err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "marshal") {
		t.Errorf("warnings = %v, want one marshal warning", warns)
	}
}
