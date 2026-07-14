package hooks

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/tallyfy/cli/internal/config"
)

func TestHostAllowed(t *testing.T) {
	cases := []struct {
		host      string
		allowlist []string
		want      bool
	}{
		{"api.example.com", []string{"*.example.com"}, true},
		{"sub.a.example.com", []string{"*.example.com"}, true},
		// The bare suffix itself is NOT matched by the wildcard.
		{"example.com", []string{"*.example.com"}, false},
		// A hyphenated lookalike must not slip through.
		{"evil-example.com", []string{"*.example.com"}, false},
		{"api.example.com.evil.io", []string{"*.example.com"}, false},
		{"EXAMPLE.COM", []string{"example.com"}, true},
		{"example.com", []string{"EXAMPLE.com"}, true},
		{"hooks.slack.com", []string{"hooks.slack.com", "*.tallyfy.com"}, true},
		{"api.tallyfy.com", []string{"hooks.slack.com", "*.tallyfy.com"}, true},
		{"other.com", []string{"hooks.slack.com", "*.tallyfy.com"}, false},
		// Blank and bare-star entries are ignored, never match-all.
		{"example.com", []string{""}, false},
		{"example.com", []string{"*"}, false},
		{"example.com", nil, false},
	}
	for _, tc := range cases {
		if got := hostAllowed(tc.host, tc.allowlist); got != tc.want {
			t.Errorf("hostAllowed(%q, %v) = %v, want %v", tc.host, tc.allowlist, got, tc.want)
		}
	}
}

func TestHookHost(t *testing.T) {
	if h, err := hookHost("https://hooks.slack.com/services/XXX"); err != nil || h != "hooks.slack.com" {
		t.Errorf("hookHost(slack URL) = %q, %v", h, err)
	}
	if h, err := hookHost("http://127.0.0.1:8080/x"); err != nil || h != "127.0.0.1" {
		t.Errorf("hookHost with port = %q, %v; want port stripped", h, err)
	}
	for _, bad := range []string{"", "ftp://example.com/x", "not-a-url", "http://"} {
		if _, err := hookHost(bad); err == nil {
			t.Errorf("hookHost(%q) succeeded, want error", bad)
		}
	}
}

// captureServer records POSTs and answers with the given status.
type captureServer struct {
	*httptest.Server
	mu   sync.Mutex
	hits int
	body []byte
	ct   string
}

func newCaptureServer(t *testing.T, status int) *captureServer {
	t.Helper()
	cs := &captureServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		cs.mu.Lock()
		cs.hits++
		cs.body = b
		cs.ct = r.Header.Get("Content-Type")
		cs.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(cs.Close)
	return cs
}

func (cs *captureServer) snapshot() (int, []byte, string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.hits, cs.body, cs.ct
}

func (cs *captureServer) host(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(cs.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname()
}

func TestHTTPHookPosts2xx(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK)
	hs := []config.Hook{{Type: "http", URL: cs.URL}}
	opts := Options{AllowedHTTPHosts: []string{cs.host(t)}}

	warns, err := NewRunner(opts).Fire(PreComplete, hs, testPayload())
	if err != nil || len(warns) != 0 {
		t.Fatalf("Fire: err=%v warns=%v", err, warns)
	}
	hits, body, ct := cs.snapshot()
	if hits != 1 {
		t.Fatalf("server hits = %d, want 1", hits)
	}
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got Payload
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("posted body is not the payload JSON: %v", err)
	}
	if got.Event != "PreDelete" || got.ID != "bp_123" || got.Org != "org_abc" {
		t.Errorf("posted payload = %+v, want the testPayload values", got)
	}
}

func TestHTTPHookNon2xxBlocksPre(t *testing.T) {
	cs := newCaptureServer(t, http.StatusInternalServerError)
	hs := []config.Hook{{Type: "http", URL: cs.URL}}
	opts := Options{AllowedHTTPHosts: []string{cs.host(t)}}

	_, err := NewRunner(opts).Fire(PreComplete, hs, testPayload())
	var be *BlockError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want *BlockError", err)
	}
	if be.HookDesc != cs.URL {
		t.Errorf("HookDesc = %q, want the hook URL %q", be.HookDesc, cs.URL)
	}
	if be.Stderr != "HTTP 500" {
		t.Errorf("Stderr = %q, want \"HTTP 500\"", be.Stderr)
	}
}

func TestHTTPHookNon2xxOnlyWarnsOnPost(t *testing.T) {
	cs := newCaptureServer(t, http.StatusInternalServerError)
	hs := []config.Hook{{Type: "http", URL: cs.URL}}
	opts := Options{AllowedHTTPHosts: []string{cs.host(t)}}

	warns, err := NewRunner(opts).Fire(PostComplete, hs, testPayload())
	if err != nil {
		t.Fatalf("Post* http failure must be advisory, got %v", err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "HTTP 500") {
		t.Errorf("warnings = %v, want one HTTP 500 warning", warns)
	}
}

func TestHTTPHookEmptyAllowlistSkipsAll(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK)
	hs := []config.Hook{{Type: "http", URL: cs.URL}}

	warns, err := NewRunner(Options{}).Fire(PreComplete, hs, testPayload())
	if err != nil {
		t.Fatalf("empty allowlist must skip, not block: %v", err)
	}
	want := "http hooks disabled: no allowlist configured"
	if len(warns) != 1 || warns[0] != want {
		t.Errorf("warnings = %v, want exactly [%q]", warns, want)
	}
	if hits, _, _ := cs.snapshot(); hits != 0 {
		t.Errorf("server hits = %d, want 0", hits)
	}
}

func TestHTTPHookDisallowedHostSkips(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK)
	hs := []config.Hook{{Type: "http", URL: cs.URL}}
	opts := Options{AllowedHTTPHosts: []string{"hooks.slack.com"}}

	warns, err := NewRunner(opts).Fire(PreComplete, hs, testPayload())
	if err != nil {
		t.Fatalf("disallowed host must skip, not block: %v", err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "not in allowlist") {
		t.Errorf("warnings = %v, want one allowlist warning", warns)
	}
	if hits, _, _ := cs.snapshot(); hits != 0 {
		t.Errorf("server hits = %d, want 0", hits)
	}
}

func TestHTTPHookWildcardAllowlist(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK)
	host := cs.host(t) // 127.0.0.1 -> "*.0.1" wildcard-matches its ".0.1" tail
	if !strings.HasSuffix(host, ".0.1") {
		t.Skipf("unexpected httptest host %q", host)
	}
	hs := []config.Hook{{Type: "http", URL: cs.URL}}
	opts := Options{AllowedHTTPHosts: []string{"*.0.1"}}

	warns, err := NewRunner(opts).Fire(PreComplete, hs, testPayload())
	if err != nil || len(warns) != 0 {
		t.Fatalf("Fire: err=%v warns=%v", err, warns)
	}
	if hits, _, _ := cs.snapshot(); hits != 1 {
		t.Errorf("server hits = %d, want 1 (wildcard allowlist entry)", hits)
	}
}

func TestHTTPHookInvalidURLSkipsWithWarning(t *testing.T) {
	hs := []config.Hook{{Type: "http", URL: "ftp://example.com/x"}}
	opts := Options{AllowedHTTPHosts: []string{"example.com"}}

	warns, err := NewRunner(opts).Fire(PreComplete, hs, testPayload())
	if err != nil {
		t.Fatalf("invalid URL must skip, not block: %v", err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "unsupported URL scheme") {
		t.Errorf("warnings = %v, want one invalid-URL warning", warns)
	}
}

func TestHTTPHookTransportErrorBlocksPre(t *testing.T) {
	// Port 1 is essentially never listening; the POST fails at transport level.
	badURL := "http://127.0.0.1:1/hook"
	hs := []config.Hook{{Type: "http", URL: badURL}}
	opts := Options{AllowedHTTPHosts: []string{"127.0.0.1"}}

	_, err := NewRunner(opts).Fire(PreComplete, hs, testPayload())
	var be *BlockError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want *BlockError on transport failure", err)
	}
	if be.HookDesc != badURL {
		t.Errorf("HookDesc = %q, want %q", be.HookDesc, badURL)
	}
	if be.Stderr == "" {
		t.Error("Stderr should carry the transport error summary")
	}
}
