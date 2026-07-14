package hooks

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHTTPHookRedirectStaysInAllowlist is a regression test for the
// open-redirect SSRF: an allowlisted host that answers with a redirect must
// not be able to relay the payload to a host outside the allowlist.
func TestHTTPHookRedirectStaysInAllowlist(t *testing.T) {
	var leaked bool
	// The exfiltration target - NOT in the allowlist.
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = true
		w.WriteHeader(http.StatusOK)
	}))
	defer evil.Close()

	// The allowlisted redirector points at the evil host.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	// Allowlist only the redirector's host (127.0.0.1 for both httptest
	// servers share the loopback host, so use the full host:port match path:
	// hostAllowed ignores the port, so we must instead point the allowlist at
	// a host the evil server does NOT share. Use a hostname alias.)
	redirHost := hostOf(t, redirector.URL)
	r := &runner{opts: Options{AllowedHTTPHosts: []string{redirHost}}}

	// Because httptest binds both servers to 127.0.0.1, port-insensitive host
	// matching would treat them as the same host and this test could not
	// distinguish a leak. Guard: only run the strict assertion when the two
	// servers have different hostnames; otherwise assert the redirect is at
	// least re-checked (covered by the unit test below).
	if redirHost == hostOf(t, evil.URL) {
		t.Skip("both httptest servers share loopback host; see TestRedirectHostRechecked")
	}

	payload, _ := json.Marshal(Payload{Event: "PostComplete"})
	_, _ = r.runHTTP(redirector.URL, payload, r.opts.AllowedHTTPHosts)
	if leaked {
		t.Fatal("payload leaked to non-allowlisted host via redirect")
	}
}

// TestRedirectHostRechecked verifies the CheckRedirect closure rejects a hop
// whose host is not in the allowlist, independent of httptest host sharing.
func TestRedirectHostRechecked(t *testing.T) {
	r := &runner{opts: Options{AllowedHTTPHosts: []string{"hooks.example.com"}}}
	// A server that redirects to a disallowed absolute URL.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "https://evil.invalid/collect", http.StatusFound)
	}))
	defer srv.Close()

	// The initial request host (127.0.0.1) is not in the allowlist either, so
	// runHTTP's caller would normally skip it; here we exercise runHTTP
	// directly with the allowlist including the initial host to reach the
	// redirect path.
	host := hostOf(t, srv.URL)
	summary, err := r.runHTTP(srv.URL, []byte(`{}`), []string{host}) // allow only the initial host
	if err == nil {
		t.Fatal("expected redirect to evil.invalid to be refused, got success")
	}
	if !strings.Contains(err.Error(), "allowlist") && !strings.Contains(summary, "allowlist") {
		t.Fatalf("expected an allowlist rejection, got err=%v summary=%q", err, summary)
	}
}

// TestHTTPHookLeadingWhitespaceURL is a regression test for the minor bug: a
// hook URL with leading whitespace passed the (trimmed) allowlist check but
// was requested raw, producing a confusing parse error that blocked Pre*
// commands. The trimmed URL must now be both validated and requested.
func TestHTTPHookLeadingWhitespaceURL(t *testing.T) {
	var got bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host := hostOf(t, srv.URL)
	r := &runner{opts: Options{AllowedHTTPHosts: []string{host}}}
	_, err := r.runHTTP("  "+srv.URL, []byte(`{}`), r.opts.AllowedHTTPHosts)
	if err != nil {
		t.Fatalf("leading-whitespace URL should be trimmed and succeed, got %v", err)
	}
	if !got {
		t.Fatal("request never reached the server")
	}
}

func hostOf(t *testing.T, rawURL string) string {
	t.Helper()
	h, err := hookHost(rawURL)
	if err != nil {
		t.Fatalf("hookHost(%q): %v", rawURL, err)
	}
	return h
}
