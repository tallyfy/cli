package hooks

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
)

// stderrCap bounds captured hook stderr; it is surfaced on Pre* blocks.
const stderrCap = 4 * 1024

// runExec runs one exec hook through the platform shell with the payload
// JSON on stdin. It returns captured stderr (trimmed, capped at 4KiB) plus
// an error on non-zero exit, start failure, or timeout. Stdout is discarded.
func (r *runner) runExec(command string, payload []byte) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.opts.Timeout)
	defer cancel()

	name, args := shellCommand(command)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stdout = io.Discard
	errBuf := &capWriter{max: stderrCap}
	cmd.Stderr = errBuf
	cmd.Env = mergedEnv(r.opts.Env)
	// Don't let grandchildren holding the stderr pipe stall Wait after the
	// context kills the hook.
	cmd.WaitDelay = time.Second

	err := cmd.Run()
	stderr := strings.TrimSpace(errBuf.String())
	if err == nil {
		return stderr, nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		terr := fmt.Errorf("hook timed out after %s", r.opts.Timeout)
		if stderr == "" {
			stderr = terr.Error()
		}
		return stderr, terr
	}
	return stderr, err
}

// shellCommand returns the platform shell invocation for one hook command:
// /bin/sh -c on unix, cmd /C on windows.
func shellCommand(command string) (name string, args []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", command}
	}
	return "/bin/sh", []string{"-c", command}
}

// mergedEnv is os.Environ() plus the settings `env` map. Settings entries
// are appended last (in sorted order, for determinism) so they win on
// duplicate keys: exec uses the last value for a repeated key.
func mergedEnv(extra map[string]string) []string {
	env := os.Environ()
	if len(extra) == 0 {
		return env
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+extra[k])
	}
	return env
}

// capWriter keeps the first max bytes written and silently drops the rest,
// always reporting a full write so the child never sees a pipe error.
type capWriter struct {
	buf bytes.Buffer
	max int
}

func (w *capWriter) Write(p []byte) (int, error) {
	n := len(p)
	if room := w.max - w.buf.Len(); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		w.buf.Write(p)
	}
	return n, nil
}

func (w *capWriter) String() string { return w.buf.String() }
