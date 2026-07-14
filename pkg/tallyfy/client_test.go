package tallyfy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testToken = "sekret-token-abc"
	testUA    = "tallyfy-cli-test/1.0 (test/amd64)"
)

// newTestClient builds a client against an httptest server.
func newTestClient(t *testing.T, maxRetries int, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(Options{
		BaseURL:    srv.URL,
		Token:      testToken,
		UserAgent:  testUA,
		MaxRetries: maxRetries,
	})
}

// shrinkBackoff makes exponential-backoff sleeps test-fast.
func shrinkBackoff(t *testing.T) {
	t.Helper()
	saved := defaultRetry
	defaultRetry.BaseDelay = time.Millisecond
	defaultRetry.MaxDelay = 5 * time.Millisecond
	t.Cleanup(func() { defaultRetry = saved })
}

// growBackoff makes backoff huge so a test can prove Retry-After was honored
// instead (the request would visibly stall otherwise).
func growBackoff(t *testing.T) {
	t.Helper()
	saved := defaultRetry
	defaultRetry.BaseDelay = 2 * time.Second
	defaultRetry.MaxDelay = 8 * time.Second
	t.Cleanup(func() { defaultRetry = saved })
}

func TestNewDefaults(t *testing.T) {
	c := New(Options{})
	if c.opts.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q, want %q", c.opts.BaseURL, DefaultBaseURL)
	}
	if c.opts.MaxRetries != 4 {
		t.Errorf("MaxRetries = %d, want 4", c.opts.MaxRetries)
	}
	if c.hc == http.DefaultClient {
		t.Error("client must not reuse http.DefaultClient")
	}
	if c.hc.Timeout != 60*time.Second {
		t.Errorf("timeout = %v, want 60s", c.hc.Timeout)
	}
	if neg := New(Options{MaxRetries: -1}); neg.opts.MaxRetries != 0 {
		t.Errorf("negative MaxRetries = %d, want 0", neg.opts.MaxRetries)
	}
}

func TestHeadersOnEveryRequest(t *testing.T) {
	var gotGet, gotPost http.Header
	c := newTestClient(t, -1, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			gotGet = r.Header.Clone()
		case http.MethodPost:
			gotPost = r.Header.Clone()
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	})

	if _, err := c.Do(context.Background(), http.MethodGet, "me", nil, nil, nil); err != nil {
		t.Fatalf("GET: %v", err)
	}
	if _, err := c.Do(context.Background(), http.MethodPost, "organizations/o/runs", nil, json.RawMessage(`{"x":1}`), nil); err != nil {
		t.Fatalf("POST: %v", err)
	}

	for name, h := range map[string]http.Header{"GET": gotGet, "POST": gotPost} {
		if got := h.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("%s Authorization = %q", name, got)
		}
		if got := h.Get("Accept"); got != "application/json" {
			t.Errorf("%s Accept = %q", name, got)
		}
		if got := h.Get("X-Tallyfy-Client"); got != ClientHeader {
			t.Errorf("%s X-Tallyfy-Client = %q, want %q", name, got, ClientHeader)
		}
		if got := h.Get("User-Agent"); got != testUA {
			t.Errorf("%s User-Agent = %q, want %q", name, got, testUA)
		}
	}
	if got := gotGet.Get("Content-Type"); got != "" {
		t.Errorf("GET Content-Type = %q, want empty (no body)", got)
	}
	if got := gotPost.Get("Content-Type"); got != "application/json" {
		t.Errorf("POST Content-Type = %q", got)
	}
}

func TestNoAuthorizationWhenTokenEmpty(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	c := New(Options{BaseURL: srv.URL, MaxRetries: -1})
	if _, err := c.Do(context.Background(), http.MethodGet, "utils/config", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["Authorization"]; ok {
		t.Error("Authorization header sent despite empty token")
	}
}

func TestURLJoining(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("a")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	// Trailing slash on BaseURL + leading slash on path must not double up.
	c := New(Options{BaseURL: srv.URL + "/", Token: "x", MaxRetries: -1})
	q := url.Values{"a": []string{"b c"}}
	if _, err := c.Do(context.Background(), http.MethodGet, "/me", q, nil, nil); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/me" {
		t.Errorf("path = %q, want /me", gotPath)
	}
	if gotQuery != "b c" {
		t.Errorf("query a = %q, want %q", gotQuery, "b c")
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		status   int
		body     string
		wantCat  ErrorCategory
		wantMsg  string
		wantText string
	}{
		{401, `{"error":true,"message":"unauthenticated"}`, CategoryAuth, "unauthenticated", "401"},
		{403, `{"message":"forbidden"}`, CategoryAuth, "forbidden", "403"},
		{404, `{"message":"Endpoint not found"}`, CategoryNotFound, "Endpoint not found", "404"},
		{422, `{"message":"invalid","errors":{"title":["required"]}}`, CategoryValidation, "invalid", "422"},
		{400, `{"message":"bad request"}`, CategoryValidation, "bad request", "400"},
		{500, `{"message":"boom"}`, CategoryGeneric, "boom", "500"},
		{500, `not json at all`, CategoryGeneric, "", "500"},
	}
	for _, tc := range cases {
		c := newTestClient(t, -1, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Request-Id", "req-123")
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		// POST so no status is ever retried here.
		_, err := c.Do(context.Background(), http.MethodPost, "organizations/o/thing", nil, json.RawMessage(`{}`), nil)
		if err == nil {
			t.Fatalf("status %d: want error", tc.status)
		}
		if got := CategoryOf(err); got != tc.wantCat {
			t.Errorf("status %d: category = %v, want %v", tc.status, got, tc.wantCat)
		}
		var ae *APIError
		if !errors.As(err, &ae) {
			t.Fatalf("status %d: error %T is not *APIError", tc.status, err)
		}
		if ae.StatusCode != tc.status {
			t.Errorf("StatusCode = %d, want %d", ae.StatusCode, tc.status)
		}
		if ae.Message != tc.wantMsg {
			t.Errorf("status %d: Message = %q, want %q", tc.status, ae.Message, tc.wantMsg)
		}
		if ae.RequestID != "req-123" {
			t.Errorf("RequestID = %q", ae.RequestID)
		}
		if ae.Method != http.MethodPost || ae.Path != "organizations/o/thing" {
			t.Errorf("Method/Path = %s %s", ae.Method, ae.Path)
		}
		if !bytes.Equal(ae.Body, []byte(tc.body)) {
			t.Errorf("Body = %q", ae.Body)
		}
		if !strings.Contains(err.Error(), tc.wantText) {
			t.Errorf("Error() = %q missing %q", err.Error(), tc.wantText)
		}
	}
}

func TestErrorBodyCappedAt8KiB(t *testing.T) {
	big := strings.Repeat("x", errBodyCap+1000)
	c := newTestClient(t, -1, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = io.WriteString(w, big)
	})
	_, err := c.Do(context.Background(), http.MethodPost, "p", nil, json.RawMessage(`{}`), nil)
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("got %T", err)
	}
	if len(ae.Body) != errBodyCap {
		t.Errorf("Body len = %d, want %d", len(ae.Body), errBodyCap)
	}
}

func TestRetry429HonorsRetryAfterSeconds(t *testing.T) {
	growBackoff(t) // backoff would stall ≥3s; Retry-After: 0 must win
	var hits atomic.Int32
	c := newTestClient(t, 4, func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) <= 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"slow down"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	})
	start := time.Now()
	if _, err := c.Do(context.Background(), http.MethodGet, "me", nil, nil, nil); err != nil {
		t.Fatalf("want success after retries, got %v", err)
	}
	if got := hits.Load(); got != 3 {
		t.Errorf("hits = %d, want 3", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("elapsed %v: Retry-After: 0 not honored (backoff used instead)", elapsed)
	}
}

func TestRetry429HonorsRetryAfterHTTPDate(t *testing.T) {
	growBackoff(t)
	var hits atomic.Int32
	c := newTestClient(t, 2, func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			// A date in the past clamps to zero delay.
			w.Header().Set("Retry-After", time.Now().Add(-time.Second).UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	})
	start := time.Now()
	if _, err := c.Do(context.Background(), http.MethodGet, "me", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("hits = %d, want 2", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("elapsed %v: HTTP-date Retry-After not honored", elapsed)
	}
}

func TestRateLimitErrorAfterBudget(t *testing.T) {
	var hits atomic.Int32
	c := newTestClient(t, 2, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"rate limited"}`))
	})
	_, err := c.Do(context.Background(), http.MethodGet, "me", nil, nil, nil)
	if err == nil {
		t.Fatal("want error")
	}
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("got %T, want *RateLimitError", err)
	}
	if rl.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3 (1 + 2 retries)", rl.Attempts)
	}
	if got := hits.Load(); got != 3 {
		t.Errorf("hits = %d, want 3", got)
	}
	if CategoryOf(err) != CategoryRateLimited {
		t.Errorf("category = %v", CategoryOf(err))
	}
	// Unwrap chain reaches the inner *APIError.
	var ae *APIError
	if !errors.As(err, &ae) || ae.StatusCode != 429 {
		t.Error("errors.As to *APIError via Unwrap failed")
	}
}

func Test5xxRetriedForGETOnly(t *testing.T) {
	shrinkBackoff(t)
	for _, status := range []int{502, 503, 504} {
		var hits atomic.Int32
		c := newTestClient(t, 3, func(w http.ResponseWriter, _ *http.Request) {
			if hits.Add(1) == 1 {
				w.WriteHeader(status)
				return
			}
			_, _ = w.Write([]byte(`{"data":{}}`))
		})
		if _, err := c.Do(context.Background(), http.MethodGet, "me", nil, nil, nil); err != nil {
			t.Fatalf("GET after %d: %v", status, err)
		}
		if got := hits.Load(); got != 2 {
			t.Errorf("status %d: hits = %d, want 2", status, got)
		}
	}
}

func Test5xxNotRetriedForPOST(t *testing.T) {
	shrinkBackoff(t)
	var hits atomic.Int32
	c := newTestClient(t, 3, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(502)
		_, _ = w.Write([]byte(`{"message":"bad gateway"}`))
	})
	_, err := c.Do(context.Background(), http.MethodPost, "organizations/o/runs", nil, json.RawMessage(`{}`), nil)
	var ae *APIError
	if !errors.As(err, &ae) || ae.StatusCode != 502 {
		t.Fatalf("want 502 APIError, got %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("hits = %d, want 1 (no 5xx retry for POST)", got)
	}
}

func TestPOSTRetriedOn429WithBodyReplay(t *testing.T) {
	const payload = `{"task_id":"t1"}`
	var hits atomic.Int32
	var secondBody []byte
	c := newTestClient(t, 2, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		switch hits.Add(1) {
		case 1:
			if string(b) != payload {
				t.Errorf("attempt 1 body = %q", b)
			}
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			secondBody = b
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{}}`))
		}
	})
	if _, err := c.Do(context.Background(), http.MethodPost, "p", nil, json.RawMessage(payload), nil); err != nil {
		t.Fatal(err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("hits = %d, want 2", got)
	}
	if string(secondBody) != payload {
		t.Errorf("replayed body = %q, want %q (body must replay on retry)", secondBody, payload)
	}
}

func TestTransportErrorRetriedForGETNotPOST(t *testing.T) {
	shrinkBackoff(t)
	newFlaky := func() (http.HandlerFunc, *atomic.Int32) {
		var hits atomic.Int32
		return func(w http.ResponseWriter, _ *http.Request) {
			if hits.Add(1) == 1 {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close() // slam the connection: transport error
				}
				return
			}
			_, _ = w.Write([]byte(`{"data":{}}`))
		}, &hits
	}

	h, hits := newFlaky()
	c := newTestClient(t, 2, h)
	if _, err := c.Do(context.Background(), http.MethodGet, "me", nil, nil, nil); err != nil {
		t.Fatalf("GET should retry transport errors: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("GET hits = %d, want 2", got)
	}

	h2, hits2 := newFlaky()
	c2 := newTestClient(t, 2, h2)
	if _, err := c2.Do(context.Background(), http.MethodPost, "p", nil, json.RawMessage(`{}`), nil); err == nil {
		t.Fatal("POST must not retry transport errors")
	}
	if got := hits2.Load(); got != 1 {
		t.Errorf("POST hits = %d, want 1", got)
	}
}

func Test204NoContent(t *testing.T) {
	c := newTestClient(t, -1, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	var out struct {
		Data json.RawMessage `json:"data"`
	}
	resp, err := c.Do(context.Background(), http.MethodDelete, "organizations/o/tags/t1", nil, nil, &out)
	if err != nil {
		t.Fatalf("204 should not error: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if out.Data != nil {
		t.Errorf("out decoded from empty body: %q", out.Data)
	}
}

func TestEmptyBody200WithOut(t *testing.T) {
	c := newTestClient(t, -1, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK) // 200 with zero-length body
	})
	var out map[string]any
	if _, err := c.Do(context.Background(), http.MethodGet, "me", nil, nil, &out); err != nil {
		t.Fatalf("empty 200 body should skip decode: %v", err)
	}
}

func TestContextCancellationDuringRetrySleep(t *testing.T) {
	var hits atomic.Int32
	c := newTestClient(t, 4, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Do(ctx, http.MethodGet, "me", nil, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("cancellation not respected during sleep: %v", elapsed)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("hits = %d, want 1", got)
	}
}

func TestVerboseOutputRedactsToken(t *testing.T) {
	var buf bytes.Buffer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	t.Cleanup(srv.Close)
	c := New(Options{BaseURL: srv.URL, Token: testToken, MaxRetries: -1, Verbose: &buf})
	if _, err := c.Do(context.Background(), http.MethodGet, "me", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "-> GET /me") {
		t.Errorf("missing request line: %q", out)
	}
	if !strings.Contains(out, "<- 200 ") {
		t.Errorf("missing response line: %q", out)
	}
	if strings.Contains(out, testToken) {
		t.Errorf("verbose output leaked the token: %q", out)
	}
	if strings.Contains(strings.ToLower(out), "authorization") {
		t.Errorf("verbose output mentions headers: %q", out)
	}
}

func TestRawPassthrough(t *testing.T) {
	c := newTestClient(t, -1, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/organizations/o/export" {
			t.Errorf("path = %q", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"in":true}` {
			t.Errorf("body = %q", b)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"anything":["goes",1]}`))
	})
	status, body, err := c.Raw(context.Background(), http.MethodPost, "organizations/o/export", nil, []byte(`{"in":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusCreated {
		t.Errorf("status = %d", status)
	}
	if string(body) != `{"anything":["goes",1]}` {
		t.Errorf("body = %q (must be undecoded passthrough)", body)
	}
}

func TestRawNon2xxReturnsBodyAndAPIError(t *testing.T) {
	c := newTestClient(t, -1, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"message":"nope","errors":{}}`))
	})
	status, body, err := c.Raw(context.Background(), http.MethodPost, "p", nil, []byte(`{}`))
	if status != 422 {
		t.Errorf("status = %d", status)
	}
	if string(body) != `{"message":"nope","errors":{}}` {
		t.Errorf("body = %q", body)
	}
	if CategoryOf(err) != CategoryValidation {
		t.Errorf("category = %v, want validation", CategoryOf(err))
	}
}
