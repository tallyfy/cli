package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeStatePath redirects the state file to a temp location.
func fakeStatePath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nested", "update-check.json")
	old := StatePath
	StatePath = func() (string, error) { return path, nil }
	t.Cleanup(func() { StatePath = old })
	return path
}

func TestStateRoundtrip(t *testing.T) {
	path := fakeStatePath(t)

	st := State{LastCheckUnix: 1720000000, LatestSeen: "v0.2.0"}
	if err := writeState(st); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	for _, key := range []string{`"last_check_unix":1720000000`, `"latest_seen":"v0.2.0"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("state file %s missing %s", raw, key)
		}
	}
	if got := readState(); got != st {
		t.Errorf("readState = %+v, want %+v", got, st)
	}
	// Overwrite must be atomic-friendly (no error on existing file).
	if err := writeState(State{LastCheckUnix: 1, LatestSeen: "v0.3.0"}); err != nil {
		t.Fatal(err)
	}
	if got := readState(); got.LatestSeen != "v0.3.0" {
		t.Errorf("overwrite failed: %+v", got)
	}
	// No temp litter left behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only the state file in dir, found %d entries", len(entries))
	}
}

func TestReadStateMissingOrCorrupt(t *testing.T) {
	path := fakeStatePath(t)

	if got := readState(); got != (State{}) {
		t.Errorf("missing file should read as zero state, got %+v", got)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readState(); got != (State{}) {
		t.Errorf("corrupt file should read as zero state, got %+v", got)
	}
}
