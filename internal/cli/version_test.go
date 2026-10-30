package cli

import (
	"bytes"
	"testing"

	"github.com/tallyfy/cli/internal/version"
)

// --version prints the version line itself, the same as the version command
// prints it. A version string with template syntax in it must still print as
// is (tallyfy/work-queue#4437 moved it off cobra's version template).
func TestVersionFlagPrintsTheVersionLineAsIs(t *testing.T) {
	old := version.Version
	t.Cleanup(func() { version.Version = old })
	version.Version = `1.2.3-{{.Name}}-"q"`
	want := versionLine()
	if !bytes.Contains([]byte(want), []byte(`{{.Name}}`)) {
		t.Fatalf("versionLine() = %q, does not carry the test version", want)
	}
	for _, args := range [][]string{{"version"}, {"--version"}} {
		root := NewRootCmd()
		var out, errb bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&errb)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v (stderr %q)", args, err, errb.String())
		}
		if out.String() != want {
			t.Errorf("%v printed %q, want %q", args, out.String(), want)
		}
	}
}
