package e2e

import (
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
