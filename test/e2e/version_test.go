package e2e

import (
	"os/exec"
	"strings"
	"testing"
)

// `tallyfy --version` prints the same line as `tallyfy version` and exits 0.
// The released v0.1.3 answered it with "unknown flag" and exit 2
// (owner decision 2026-10-28, tallyfy/work-queue#4280).
func TestVersionFlagMatchesVersionCommand(t *testing.T) {
	// No request is sent, so the base URL is never dialed.
	const unused = "http://127.0.0.1:9"
	cmd := run(t, unused, "", "version")
	if cmd.code != 0 || !strings.HasPrefix(cmd.stdout, "tallyfy ") {
		t.Fatalf("version: exit = %d, stdout = %q, stderr = %q", cmd.code, cmd.stdout, cmd.stderr)
	}
	flag := run(t, unused, "", "--version")
	if flag.code != 0 {
		t.Fatalf("--version: exit = %d, want 0; stderr = %q", flag.code, flag.stderr)
	}
	if flag.stdout != cmd.stdout {
		t.Errorf("--version printed %q, want the version command's line %q", flag.stdout, cmd.stdout)
	}
	if flag.stderr != "" {
		t.Errorf("--version wrote to stderr: %q", flag.stderr)
	}
}

// The binary must not link the text/template evaluator. A reachable
// text/template makes the Go linker keep every exported method, and
// printing --version through a cobra version template made v0.1.4 about
// 1.8 MB (23 percent) larger (tallyfy/work-queue#4437). The evaluator's
// field lookup is linked only when a template can run, so its symbol is
// the check, and main.main is the control that the symbol table was read.
func TestBinaryDoesNotLinkTemplateEvaluator(t *testing.T) {
	out, err := exec.Command("go", "tool", "nm", binPath).CombinedOutput()
	if err != nil {
		t.Fatalf("go tool nm: %v\n%.500s", err, out)
	}
	syms := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) >= 3 {
			syms[f[len(f)-1]] = true
		}
	}
	if !syms["main.main"] {
		t.Fatalf("the symbol table has no main.main, so it proves nothing (%d symbols)", len(syms))
	}
	if syms["text/template.(*state).evalField"] {
		t.Errorf("the binary links text/template.(*state).evalField: something makes a text/template reachable, which keeps every exported method")
	}
}

// --version is answered in the root's help function, so the help must stay
// as it was: a bare `tallyfy` and `tallyfy --version --help` print the help
// with exit 0, and its usage does not offer `tallyfy [flags]`, which it
// would if the root became runnable.
func TestVersionFlagLeavesHelpAlone(t *testing.T) {
	const unused = "http://127.0.0.1:9"
	version := run(t, unused, "", "version")
	for _, args := range [][]string{{}, {"--version", "--help"}, {"--help"}} {
		res := run(t, unused, "", args...)
		if res.code != 0 {
			t.Fatalf("%v: exit = %d, want 0; stderr = %q", args, res.code, res.stderr)
		}
		if !strings.Contains(res.stdout, "Usage:\n  tallyfy [command]\n") || strings.Contains(res.stdout, "tallyfy [flags]") {
			t.Errorf("%v: help usage changed:\n%.400s", args, res.stdout)
		}
		if strings.Contains(res.stdout, strings.TrimSpace(version.stdout)) {
			t.Errorf("%v printed the version line instead of the help", args)
		}
	}
}
