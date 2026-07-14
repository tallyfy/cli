package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	helperStdoutCap = 64 << 10 // 64 KiB of stdout kept; the rest is dropped
	helperStderrCap = 4 << 10  // 4 KiB of stderr kept for error summaries
	// helperStderrSummaryMax bounds how much captured stderr is embedded in
	// the returned error message.
	helperStderrSummaryMax = 512
)

// helperTimeout is how long the apiKeyHelper subprocess may run. A var so
// tests can shorten it.
var helperTimeout = 10 * time.Second

// helperWaitDelay bounds the extra wait for I/O pipes after the helper is
// killed on timeout (grandchildren of the shell can hold the pipe open).
var helperWaitDelay = 500 * time.Millisecond

// capWriter keeps at most max bytes and silently discards the rest, so a
// misbehaving helper cannot balloon memory. It always reports a full write.
type capWriter struct {
	buf bytes.Buffer
	max int
}

func (w *capWriter) Write(p []byte) (int, error) {
	n := len(p)
	if remain := w.max - w.buf.Len(); remain > 0 {
		if len(p) > remain {
			p = p[:remain]
		}
		w.buf.Write(p) // bytes.Buffer.Write never returns an error
	}
	return n, nil
}

// runHelper executes the auth.apiKeyHelper command line via the platform
// shell (`/bin/sh -c` on unix, `cmd /C` on Windows), with os.Environ() plus
// cfg.Env pairs, a 10s timeout, and capped output capture. Stdout must be
// either the HelperResult JSON object or a bare single-line token.
//
// All failures are returned as "apiKeyHelper failed: ..." so the CLI's
// auth-error path (exit 3) reads cleanly.
func runHelper(command string, extraEnv map[string]string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), helperTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", command)
	}

	env := os.Environ()
	for k, v := range extraEnv {
		// Later entries win on duplicates per os/exec semantics, so
		// settings-provided env overrides the inherited environment.
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	stdout := &capWriter{max: helperStdoutCap}
	stderr := &capWriter{max: helperStderrCap}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// After the context kills the shell, don't wait forever on pipes held
	// open by orphaned grandchildren.
	cmd.WaitDelay = helperWaitDelay

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("apiKeyHelper failed: timed out after %s%s", helperTimeout, stderrSummary(stderr.buf.String()))
	}
	if err != nil {
		return "", fmt.Errorf("apiKeyHelper failed: %v%s", err, stderrSummary(stderr.buf.String()))
	}

	tok, err := parseHelperOutput(stdout.buf.String())
	if err != nil {
		return "", fmt.Errorf("apiKeyHelper failed: %w", err)
	}
	return tok, nil
}

// parseHelperOutput extracts a token from helper stdout. Contract (§6.4):
//
//	{"token":"<value>","expiresInSeconds":3600}   JSON object, or
//	<bare token>                                  one non-empty line
//
// Output starting with "{" MUST parse as the JSON form (a lone "{garbage"
// line is never accepted as a token). ExpiresInSeconds is parsed but unused:
// helper results are not cached in v1 (STATE.md deviation).
func parseHelperOutput(out string) (string, error) {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return "", errors.New(`helper produced no output (expected {"token":...} JSON or a bare token)`)
	}
	if strings.HasPrefix(trimmed, "{") {
		var res HelperResult
		if err := json.Unmarshal([]byte(trimmed), &res); err != nil {
			return "", fmt.Errorf("helper stdout looks like JSON but does not parse: %v", err)
		}
		tok := strings.TrimSpace(res.Token)
		if tok == "" {
			return "", errors.New(`helper JSON result has an empty "token" field`)
		}
		return tok, nil
	}
	if strings.ContainsAny(trimmed, "\r\n") {
		return "", errors.New("helper stdout is not a single-line token and not JSON")
	}
	return trimmed, nil
}

// stderrSummary renders captured stderr as an error-message suffix.
func stderrSummary(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) > helperStderrSummaryMax {
		s = s[:helperStderrSummaryMax] + "..."
	}
	return ": " + s
}
