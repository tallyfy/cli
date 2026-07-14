package saved

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeHome points the user layer at a temp dir for the duration of a test.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	old := UserHomeDir
	UserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { UserHomeDir = old })
	return home
}

func writeCommandFile(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, ".tallyfy", "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAllUserAndProjectOverride(t *testing.T) {
	home := fakeHome(t)
	proj := t.TempDir()

	writeCommandFile(t, home, "onboard.yaml", "name: onboard\ndescription: user version\ncommand: task list\nparams: []\n")
	writeCommandFile(t, home, "report.yaml", "command: process list --limit 5\n") // name defaults to "report"
	projPath := writeCommandFile(t, proj, "onboard.yaml", "name: onboard\ndescription: project version\ncommand: task list --all\n")

	cmds, err := LoadAll(proj)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("got %d commands, want 2: %+v", len(cmds), cmds)
	}
	// Sorted by name: onboard, report.
	if cmds[0].Name != "onboard" || cmds[1].Name != "report" {
		t.Fatalf("unexpected order/names: %+v", cmds)
	}
	if cmds[0].Source != "project" || cmds[0].Path != projPath || cmds[0].Description != "project version" {
		t.Errorf("project should override user: %+v", cmds[0])
	}
	if cmds[1].Source != "user" {
		t.Errorf("report should come from user scope: %+v", cmds[1])
	}
	if len(LastWarnings) != 0 {
		t.Errorf("unexpected warnings: %v", LastWarnings)
	}
}

func TestLoadAllNameDefaultsToFilename(t *testing.T) {
	fakeHome(t)
	proj := t.TempDir()
	writeCommandFile(t, proj, "daily-report.yaml", "command: process list\n")

	cmds, err := LoadAll(proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || cmds[0].Name != "daily-report" {
		t.Fatalf("expected name from filename, got %+v", cmds)
	}
}

func TestLoadAllProjectDefaultNameOverridesUser(t *testing.T) {
	home := fakeHome(t)
	proj := t.TempDir()
	writeCommandFile(t, home, "deploy.yaml", "command: user variant\n")
	writeCommandFile(t, proj, "deploy.yaml", "command: project variant\n")

	cmds, err := LoadAll(proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || cmds[0].Source != "project" || cmds[0].Command != "project variant" {
		t.Fatalf("filename-based override failed: %+v", cmds)
	}
}

func TestLoadAllSkipsInvalidFiles(t *testing.T) {
	fakeHome(t)
	proj := t.TempDir()
	writeCommandFile(t, proj, "good.yaml", "command: task list\n")
	writeCommandFile(t, proj, "broken.yaml", "command: [unclosed\n")
	writeCommandFile(t, proj, "nocmd.yaml", "name: nocmd\ndescription: no command line\n")

	cmds, err := LoadAll(proj)
	if err != nil {
		t.Fatalf("LoadAll should tolerate bad files when something loads: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Name != "good" {
		t.Fatalf("expected only the good command, got %+v", cmds)
	}
	if len(LastWarnings) != 2 {
		t.Fatalf("expected 2 warnings, got %v", LastWarnings)
	}
	joined := strings.Join(LastWarnings, "\n")
	if !strings.Contains(joined, "broken.yaml") || !strings.Contains(joined, "nocmd.yaml") {
		t.Errorf("warnings should name the offending files: %v", LastWarnings)
	}
}

func TestLoadAllErrorsWhenNothingLoads(t *testing.T) {
	home := fakeHome(t)
	writeCommandFile(t, home, "broken.yaml", "command: [unclosed\n")

	_, err := LoadAll("")
	if err == nil {
		t.Fatal("expected error when files exist but none load")
	}
	if !strings.Contains(err.Error(), "broken.yaml") {
		t.Errorf("error should name the file: %v", err)
	}
}

func TestLoadAllMissingDirs(t *testing.T) {
	fakeHome(t) // no .tallyfy/commands created
	cmds, err := LoadAll(t.TempDir())
	if err != nil {
		t.Fatalf("missing dirs must not error: %v", err)
	}
	if len(cmds) != 0 {
		t.Fatalf("expected no commands, got %+v", cmds)
	}
}

func TestLoadAllEmptyProjectDir(t *testing.T) {
	home := fakeHome(t)
	writeCommandFile(t, home, "solo.yaml", "command: task list\n")
	cmds, err := LoadAll("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || cmds[0].Source != "user" {
		t.Fatalf("user-only load failed: %+v", cmds)
	}
}

func TestFind(t *testing.T) {
	cmds := []Command{{Name: "a"}, {Name: "b"}}
	if c, ok := Find(cmds, "b"); !ok || c.Name != "b" {
		t.Errorf("Find(b) = %v, %v", c, ok)
	}
	if _, ok := Find(cmds, "zzz"); ok {
		t.Error("Find(zzz) should not match")
	}
}

func TestExpandHappyPath(t *testing.T) {
	c := Command{
		Name:    "onboard",
		Command: `process launch --blueprint "Employee Onboarding" --field name={{name}} --field email={{email}}`,
		Params:  []string{"name", "email"},
	}
	argv, err := Expand(c, map[string]string{"name": "Jo", "email": "jo@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"process", "launch", "--blueprint", "Employee Onboarding", "--field", "name=Jo", "--field", "email=jo@example.com"}
	if !reflect.DeepEqual(argv, want) {
		t.Errorf("argv = %q, want %q", argv, want)
	}
}

func TestExpandQuotedPlaceholderKeepsSpaces(t *testing.T) {
	c := Command{Name: "x", Command: `task complete --comment "{{comment}}"`, Params: []string{"comment"}}
	argv, err := Expand(c, map[string]string{"comment": "looks good to me"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"task", "complete", "--comment", "looks good to me"}
	if !reflect.DeepEqual(argv, want) {
		t.Errorf("argv = %q, want %q", argv, want)
	}
}

func TestExpandMissingParams(t *testing.T) {
	c := Command{Name: "onboard", Command: "x {{name}} {{email}}", Params: []string{"name", "email"}}
	_, err := Expand(c, map[string]string{"name": "Jo"})
	if err == nil {
		t.Fatal("expected error for missing params")
	}
	if !strings.Contains(err.Error(), "email") {
		t.Errorf("error should name the missing param: %v", err)
	}
	_, err = Expand(c, nil)
	if err == nil || !strings.Contains(err.Error(), "name") || !strings.Contains(err.Error(), "email") {
		t.Errorf("error should name all missing params: %v", err)
	}
}

func TestExpandUnknownPlaceholder(t *testing.T) {
	c := Command{Name: "x", Command: "task list --org {{org}}", Params: []string{"name"}}
	_, err := Expand(c, map[string]string{"name": "Jo"})
	if err == nil {
		t.Fatal("expected error for undeclared placeholder")
	}
	if !strings.Contains(err.Error(), "{{org}}") {
		t.Errorf("error should name the placeholder: %v", err)
	}
}

func TestExpandEmptyParamValueAllowed(t *testing.T) {
	c := Command{Name: "x", Command: `task list --filter "{{q}}"`, Params: []string{"q"}}
	argv, err := Expand(c, map[string]string{"q": ""})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"task", "list", "--filter", ""}
	if !reflect.DeepEqual(argv, want) {
		t.Errorf("argv = %q, want %q", argv, want)
	}
}

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{`a b c`, []string{"a", "b", "c"}, false},
		{`a  b\tc`, nil, false}, // placeholder, replaced below
		{`a "b c" d`, []string{"a", "b c", "d"}, false},
		{`a 'b c' d`, []string{"a", "b c", "d"}, false},
		{`--field name="Jo \"JJ\" Smith"`, []string{"--field", `name=Jo "JJ" Smith`}, false},
		{`"a\\b"`, []string{`a\b`}, false},
		{`a"b c"d`, []string{"ab cd"}, false},
		{`""`, []string{""}, false},
		{`''`, []string{""}, false},
		{`'don"t'`, []string{`don"t`}, false},
		{``, nil, false},
		{`   `, nil, false},
		{`"unterminated`, nil, true},
		{`'unterminated`, nil, true},
	}
	// Fix the tab case (raw string cannot express \t).
	cases[1].in = "a \tb\nc"
	cases[1].want = []string{"a", "b", "c"}

	for _, tc := range cases {
		got, err := splitArgs(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("splitArgs(%q): expected error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("splitArgs(%q): %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitArgs(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
