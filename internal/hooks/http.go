package hooks

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// httpTimeout is fixed for HTTP hooks (spec §6.7), independent of the exec
// hook timeout in Options.
const httpTimeout = 10 * time.Second

// runHTTP POSTs the payload JSON to the hook URL. On non-2xx or a transport
// error it returns a summary ("HTTP <status>" or the error text) plus an
// error; the response body is drained and discarded.
//
// The allowlist is re-checked on EVERY redirect hop, not just the initial URL.
// Without this, an allowlisted host answering 307/308 would relay the full
// lifecycle payload to an arbitrary host, walking around the egress control
// (open-redirect SSRF). The same trimmed URL that passed the caller's
// allowlist check is the one requested, so the string validated always equals
// the string fetched.
func (r *runner) runHTTP(rawURL string, payload []byte, allowlist []string) (string, error) {
	client := &http.Client{
		Timeout: httpTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			host := req.URL.Hostname()
			if !hostAllowed(host, allowlist) {
				return fmt.Errorf("redirect to host %q not in allowlist", host)
			}
			return nil
		},
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimSpace(rawURL), bytes.NewReader(payload))
	if err != nil {
		return err.Error(), err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err.Error(), err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		summary := fmt.Sprintf("HTTP %d", resp.StatusCode)
		return summary, fmt.Errorf("hook endpoint returned %s", summary)
	}
	return "", nil
}

// hookHost extracts the hostname of an HTTP hook URL, rejecting URLs that
// cannot be safely checked against the allowlist.
func hookHost(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("invalid URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("URL has no host")
	}
	return host, nil
}

// hostAllowed applies the HTTP hook allowlist: an entry matches on exact
// hostname (case-insensitive, port ignored) or, for "*.suffix" entries, on
// any host ending in ".suffix" — never the bare suffix itself. Blank entries
// and a bare "*" are ignored: the allowlist for code-triggering endpoints is
// deliberately never match-all.
func hostAllowed(host string, allowlist []string) bool {
	h := strings.ToLower(host)
	for _, entry := range allowlist {
		e := strings.ToLower(strings.TrimSpace(entry))
		if e == "" {
			continue
		}
		if suffix, isWild := strings.CutPrefix(e, "*"); isWild {
			if strings.HasPrefix(suffix, ".") && strings.HasSuffix(h, suffix) {
				return true
			}
			continue
		}
		if h == e {
			return true
		}
	}
	return false
}
