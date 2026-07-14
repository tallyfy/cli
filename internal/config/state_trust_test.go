package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateSaveLoadRoundTrip(t *testing.T) {
	e := setupTestEnv(t)
	in := &State{CurrentOrg: "org_abc", TrustedWorkspaces: []string{"/x/y", "/a/b"}}
	if err := SaveState(in); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	out, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if out.CurrentOrg != in.CurrentOrg {
		t.Errorf("CurrentOrg = %q, want %q", out.CurrentOrg, in.CurrentOrg)
	}
	if len(out.TrustedWorkspaces) != 2 || out.TrustedWorkspaces[0] != "/x/y" || out.TrustedWorkspaces[1] != "/a/b" {
		t.Errorf("TrustedWorkspaces = %v, want %v", out.TrustedWorkspaces, in.TrustedWorkspaces)
	}

	p := filepath.Join(e.home, ".tallyfy", "state.json")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat state.json: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("state.json mode = %v, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatalf("stat ~/.tallyfy: %v", err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("~/.tallyfy mode = %v, want 0700", di.Mode().Perm())
	}
}

func TestLoadStateMissingFile(t *testing.T) {
	setupTestEnv(t)
	st, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState on missing file: %v", err)
	}
	if st.CurrentOrg != "" || len(st.TrustedWorkspaces) != 0 {
		t.Errorf("expected empty state, got %+v", st)
	}
}

func TestLoadStateCorruptFile(t *testing.T) {
	e := setupTestEnv(t)
	writeFileT(t, filepath.Join(e.home, ".tallyfy", "state.json"), `{ broken`)
	st, err := LoadState()
	if err == nil {
		t.Error("LoadState on corrupt file: want error")
	}
	if st == nil {
		t.Fatal("LoadState must still return a usable empty state")
	}
}

func TestTrustUntrustWorkspace(t *testing.T) {
	e := setupTestEnv(t)
	dir := e.project

	if WorkspaceTrusted(dir) {
		t.Error("workspace trusted before TrustWorkspace")
	}
	if err := TrustWorkspace(dir); err != nil {
		t.Fatalf("TrustWorkspace: %v", err)
	}
	if !WorkspaceTrusted(dir) {
		t.Error("workspace not trusted after TrustWorkspace")
	}

	// Idempotent: trusting again must not duplicate the entry.
	if err := TrustWorkspace(dir); err != nil {
		t.Fatalf("TrustWorkspace (repeat): %v", err)
	}
	st, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(st.TrustedWorkspaces) != 1 {
		t.Errorf("TrustedWorkspaces = %v, want exactly 1 entry", st.TrustedWorkspaces)
	}

	if err := UntrustWorkspace(dir); err != nil {
		t.Fatalf("UntrustWorkspace: %v", err)
	}
	if WorkspaceTrusted(dir) {
		t.Error("workspace still trusted after UntrustWorkspace")
	}
	// Untrusting a never-trusted dir is not an error.
	if err := UntrustWorkspace(dir); err != nil {
		t.Errorf("UntrustWorkspace (repeat): %v", err)
	}
}

func TestTrustExactPathMatchOnly(t *testing.T) {
	e := setupTestEnv(t)
	if err := TrustWorkspace(e.project); err != nil {
		t.Fatalf("TrustWorkspace: %v", err)
	}
	child := filepath.Join(e.project, "sub")
	if WorkspaceTrusted(child) {
		t.Error("child dir trusted via parent; trust must be an exact path match")
	}
	if WorkspaceTrusted(filepath.Dir(e.project)) {
		t.Error("parent dir trusted via child; trust must be an exact path match")
	}
	// Unclean spellings of the same path still match.
	if !WorkspaceTrusted(e.project + string(filepath.Separator)) {
		t.Error("trailing separator broke the exact-path match")
	}
}

func TestWorkspaceTrustedEmptyAndErrors(t *testing.T) {
	setupTestEnv(t)
	if WorkspaceTrusted("") {
		t.Error(`WorkspaceTrusted("") = true, want false`)
	}
	if err := TrustWorkspace(""); err == nil {
		t.Error(`TrustWorkspace("") succeeded, want error`)
	}
	if err := UntrustWorkspace(""); err == nil {
		t.Error(`UntrustWorkspace("") succeeded, want error`)
	}
}

func TestWorkspaceTrustedCorruptStateFailsClosed(t *testing.T) {
	e := setupTestEnv(t)
	if err := TrustWorkspace(e.project); err != nil {
		t.Fatalf("TrustWorkspace: %v", err)
	}
	writeFileT(t, filepath.Join(e.home, ".tallyfy", "state.json"), `{ broken`)
	if WorkspaceTrusted(e.project) {
		t.Error("corrupt state must resolve to untrusted (fail closed)")
	}
}
