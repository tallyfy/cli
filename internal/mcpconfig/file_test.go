package mcpconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLoadMissingFile(t *testing.T) {
	proj := t.TempDir()
	f, err := Load(proj)
	if err != nil {
		t.Fatal(err)
	}
	if f == nil || f.MCPServers == nil {
		t.Fatal("missing file must yield an initialized empty File")
	}
	if len(f.MCPServers) != 0 {
		t.Errorf("expected no servers, got %v", f.MCPServers)
	}
	// The initialized map must be usable straight away.
	Add(f, "tallyfy", DefaultTallyfyServer)
	if len(f.MCPServers) != 1 {
		t.Error("Add on freshly loaded empty file failed")
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	proj := t.TempDir()
	f := &File{}
	Add(f, "tallyfy", DefaultTallyfyServer)
	Add(f, "local-tools", Server{Type: "stdio", Cmd: "npx", Args: []string{"-y", "my-mcp"}})

	if err := Save(proj, f); err != nil {
		t.Fatal(err)
	}
	path := Path(proj)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Save did not create %s: %v", path, err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Error("mcp.json should end with a newline")
	}
	if !strings.Contains(string(raw), `"mcpServers"`) {
		t.Errorf("unexpected file shape:\n%s", raw)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o644 {
			t.Errorf("mcp.json mode = %o, want 0644", perm)
		}
	}

	got, err := Load(proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.MCPServers) != 2 {
		t.Fatalf("roundtrip lost servers: %v", got.MCPServers)
	}
	if !reflect.DeepEqual(got.MCPServers["tallyfy"], DefaultTallyfyServer) {
		t.Errorf("tallyfy entry = %+v", got.MCPServers["tallyfy"])
	}
	local := got.MCPServers["local-tools"]
	if local.Type != "stdio" || local.Cmd != "npx" || len(local.Args) != 2 {
		t.Errorf("local-tools entry = %+v", local)
	}
	// No temp litter in .tallyfy.
	entries, err := os.ReadDir(filepath.Join(proj, ".tallyfy"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only mcp.json in .tallyfy, found %d entries", len(entries))
	}
}

func TestSaveCreatesDotTallyfyDir(t *testing.T) {
	proj := filepath.Join(t.TempDir(), "sub") // .tallyfy parent also missing
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Save(proj, &File{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Path(proj)); err != nil {
		t.Fatalf("mcp.json missing: %v", err)
	}
}

func TestSaveNilFile(t *testing.T) {
	if err := Save(t.TempDir(), nil); err == nil {
		t.Fatal("expected error for nil file")
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".tallyfy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(proj), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(proj)
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("expected invalid-JSON error, got %v", err)
	}
}

func TestAddInitializesNilMap(t *testing.T) {
	f := &File{}
	Add(f, "x", Server{Type: "http", URL: "https://x.test"})
	if f.MCPServers["x"].URL != "https://x.test" {
		t.Errorf("Add on nil map failed: %+v", f)
	}
}

func TestRemove(t *testing.T) {
	f := &File{}
	Add(f, "x", Server{Type: "http", URL: "https://x.test"})
	if !Remove(f, "x") {
		t.Error("Remove existing should report true")
	}
	if Remove(f, "x") {
		t.Error("Remove missing should report false")
	}
	if Remove(nil, "x") {
		t.Error("Remove on nil file should report false")
	}
	if Remove(&File{}, "x") {
		t.Error("Remove on nil map should report false")
	}
}

func TestSnippetClaudeCodeHTTP(t *testing.T) {
	got, err := Snippet("claude-code", "tallyfy", DefaultTallyfyServer)
	if err != nil {
		t.Fatal(err)
	}
	want := "claude mcp add --transport http tallyfy https://mcp.tallyfy.com"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSnippetClaudeCodeStdio(t *testing.T) {
	got, err := Snippet("claude-code", "local", Server{Type: "stdio", Cmd: "npx", Args: []string{"-y", "my-mcp"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "claude mcp add --transport stdio local -- npx -y my-mcp"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

const wantJSONFragment = `{
  "mcpServers": {
    "tallyfy": {
      "type": "http",
      "url": "https://mcp.tallyfy.com"
    }
  }
}`

func TestSnippetClaudeDesktopAndCursor(t *testing.T) {
	for _, client := range []string{"claude-desktop", "cursor"} {
		got, err := Snippet(client, "tallyfy", DefaultTallyfyServer)
		if err != nil {
			t.Fatal(err)
		}
		if got != wantJSONFragment {
			t.Errorf("%s snippet:\n%s\nwant:\n%s", client, got, wantJSONFragment)
		}
	}
}

func TestSnippetStdioOmitsURL(t *testing.T) {
	got, err := Snippet("cursor", "local", Server{Type: "stdio", Cmd: "npx", Args: []string{"-y", "my-mcp"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, `"url"`) {
		t.Errorf("stdio snippet must omit url:\n%s", got)
	}
	for _, want := range []string{`"command": "npx"`, `"args"`} {
		if !strings.Contains(got, want) {
			t.Errorf("stdio snippet missing %s:\n%s", want, got)
		}
	}
}

func TestSnippetUnknownClient(t *testing.T) {
	_, err := Snippet("vscode", "tallyfy", DefaultTallyfyServer)
	if err == nil {
		t.Fatal("expected error for unknown client")
	}
	for _, c := range SupportedClients {
		if !strings.Contains(err.Error(), c) {
			t.Errorf("error should list %q: %v", c, err)
		}
	}
}
