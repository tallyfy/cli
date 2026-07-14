package auth

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func requireShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("helper tests use /bin/sh scripts")
	}
}

func TestRunHelperJSONResult(t *testing.T) {
	requireShell(t)
	tok, err := runHelper(`printf '{"token":"json-tok","expiresInSeconds":3600}'`, nil)
	if err != nil {
		t.Fatalf("runHelper() error: %v", err)
	}
	if tok != "json-tok" {
		t.Errorf("token = %q, want %q", tok, "json-tok")
	}
}

func TestRunHelperJSONMultilinePrettyPrinted(t *testing.T) {
	requireShell(t)
	tok, err := runHelper(`printf '{\n  "token": "pretty-tok"\n}\n'`, nil)
	if err != nil {
		t.Fatalf("runHelper() error: %v", err)
	}
	if tok != "pretty-tok" {
		t.Errorf("token = %q, want %q", tok, "pretty-tok")
	}
}

func TestRunHelperBareToken(t *testing.T) {
	requireShell(t)
	// echo adds a trailing newline; it must be trimmed.
	tok, err := runHelper("echo bare-tok", nil)
	if err != nil {
		t.Fatalf("runHelper() error: %v", err)
	}
	if tok != "bare-tok" {
		t.Errorf("token = %q, want %q", tok, "bare-tok")
	}
}

func TestRunHelperMultilineGarbage(t *testing.T) {
	requireShell(t)
	_, err := runHelper(`printf 'line-one\nline-two\n'`, nil)
	if err == nil {
		t.Fatal("expected error for multiline non-JSON output")
	}
	if !strings.Contains(err.Error(), "apiKeyHelper failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunHelperInvalidJSON(t *testing.T) {
	requireShell(t)
	_, err := runHelper(`printf '{this is not json'`, nil)
	if err == nil {
		t.Fatal("expected error for output that starts like JSON but is not")
	}
	if !strings.Contains(err.Error(), "does not parse") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunHelperJSONEmptyToken(t *testing.T) {
	requireShell(t)
	_, err := runHelper(`printf '{"token":""}'`, nil)
	if err == nil {
		t.Fatal("expected error for empty token in JSON result")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunHelperEmptyOutput(t *testing.T) {
	requireShell(t)
	_, err := runHelper("true", nil)
	if err == nil {
		t.Fatal("expected error for helper that prints nothing")
	}
	if !strings.Contains(err.Error(), "no output") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunHelperNonZeroExit(t *testing.T) {
	requireShell(t)
	_, err := runHelper("echo vault sealed >&2; exit 3", nil)
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	msg := err.Error()
	if !strings.Contains(msg, "apiKeyHelper failed") {
		t.Errorf("error should be prefixed 'apiKeyHelper failed': %v", err)
	}
	if !strings.Contains(msg, "vault sealed") {
		t.Errorf("error should include stderr summary: %v", err)
	}
	if !strings.Contains(msg, "exit status 3") {
		t.Errorf("error should include the exit status: %v", err)
	}
}

func TestRunHelperTimeoutKills(t *testing.T) {
	requireShell(t)
	prev := helperTimeout
	helperTimeout = 200 * time.Millisecond
	t.Cleanup(func() { helperTimeout = prev })

	start := time.Now()
	_, err := runHelper("sleep 5", nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error should mention the timeout: %v", err)
	}
	// 200ms timeout + 500ms WaitDelay pipe grace; anything close to the
	// 5s sleep means the helper was not killed.
	if elapsed > 3*time.Second {
		t.Errorf("helper was not killed promptly: took %s", elapsed)
	}
}

func TestRunHelperEnvPassthrough(t *testing.T) {
	requireShell(t)
	tok, err := runHelper(`printf '%s' "$TLFY_HELPER_UNIT_VAR"`, map[string]string{
		"TLFY_HELPER_UNIT_VAR": "env-tok",
	})
	if err != nil {
		t.Fatalf("runHelper() error: %v", err)
	}
	if tok != "env-tok" {
		t.Errorf("token = %q, want %q (cfg.Env not passed to helper)", tok, "env-tok")
	}
}

func TestRunHelperEnvOverridesInherited(t *testing.T) {
	requireShell(t)
	t.Setenv("TLFY_HELPER_UNIT_VAR", "inherited")
	tok, err := runHelper(`printf '%s' "$TLFY_HELPER_UNIT_VAR"`, map[string]string{
		"TLFY_HELPER_UNIT_VAR": "from-settings",
	})
	if err != nil {
		t.Fatalf("runHelper() error: %v", err)
	}
	if tok != "from-settings" {
		t.Errorf("token = %q, want %q (cfg.Env must win over inherited env)", tok, "from-settings")
	}
}

func TestRunHelperStdoutCapped(t *testing.T) {
	requireShell(t)
	// 80,000 'a' bytes on one line; capture is capped at 64 KiB and the
	// (truncated) single line is still parsed as a bare token.
	tok, err := runHelper(`head -c 80000 /dev/zero | tr '\0' 'a'`, nil)
	if err != nil {
		t.Fatalf("runHelper() error: %v", err)
	}
	if len(tok) != helperStdoutCap {
		t.Errorf("token length = %d, want capped %d", len(tok), helperStdoutCap)
	}
}

func TestParseHelperOutputTable(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"bare token", "tok\n", "tok", false},
		{"bare token padded", "  tok  \n", "tok", false},
		{"json", `{"token":"t1"}`, "t1", false},
		{"json with expiry", `{"token":"t2","expiresInSeconds":60}`, "t2", false},
		{"json token padded", `{"token":"  t3  "}`, "t3", false},
		{"empty", "", "", true},
		{"whitespace only", " \n\t", "", true},
		{"multiline", "a\nb", "", true},
		{"crlf multiline", "a\r\nb", "", true},
		{"json garbage", "{oops", "", true},
		{"json empty token", `{"token":""}`, "", true},
		{"json whitespace token", `{"token":"   "}`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseHelperOutput(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseHelperOutput(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseHelperOutput(%q) error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("parseHelperOutput(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
