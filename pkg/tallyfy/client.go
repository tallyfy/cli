package tallyfy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTimeout = 60 * time.Second
	// maxRetryAfter is a sanity cap on server-provided Retry-After sleeps so a
	// misbehaving proxy cannot hang an interactive CLI for minutes.
	maxRetryAfter = 30 * time.Second
	// errBodyCap bounds APIError.Body.
	errBodyCap = 8 << 10 // 8 KiB
)

// defaultRetry holds the backoff shape. Package-level (rather than a Client
// field) because the Client struct is part of the frozen contract; tests in
// this package may shrink the delays.
var defaultRetry = RetryConfig{
	MaxRetries: 4,
	BaseDelay:  500 * time.Millisecond,
	MaxDelay:   8 * time.Second,
}

// New builds a Client. Zero-value options get production defaults:
// BaseURL https://api.tallyfy.com, MaxRetries 4, a dedicated *http.Client
// with a 60s timeout (http.DefaultClient is never mutated). A negative
// MaxRetries disables retries entirely.
func New(opts Options) *Client {
	if opts.BaseURL == "" {
		opts.BaseURL = DefaultBaseURL
	}
	switch {
	case opts.MaxRetries == 0:
		opts.MaxRetries = defaultRetry.MaxRetries
	case opts.MaxRetries < 0:
		opts.MaxRetries = 0
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{opts: opts, hc: hc}
}

// Unwrap exposes the wrapped *APIError to errors.As/errors.Is chains.
// (Defined here, not in the frozen errors.go.)
func (e *RateLimitError) Unwrap() error { return e.APIError }

// Do performs one API request. path is relative to BaseURL with or without a
// leading slash ("organizations/abc/checklists"). body is marshaled to JSON
// once (json.RawMessage and []byte pass through untouched) so retries replay
// identical bytes. A 2xx JSON body is decoded into out when out != nil; 204 /
// empty bodies decode to nothing. Non-2xx responses return *APIError (or
// *RateLimitError for a 429 that survives the retry budget).
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any, out any) (*http.Response, error) {
	bodyBytes, err := marshalBody(body)
	if err != nil {
		return nil, fmt.Errorf("marshal %s %s body: %w", method, path, err)
	}
	resp, raw, err := c.do(ctx, method, path, query, bodyBytes)
	if err != nil {
		return resp, err
	}
	if out != nil && resp.StatusCode != http.StatusNoContent && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return resp, nil
}

// marshalBody serializes a request body exactly once.
func marshalBody(body any) ([]byte, error) {
	switch b := body.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		return b, nil
	case []byte:
		return b, nil
	default:
		return json.Marshal(body)
	}
}

// do is the retrying core shared by Do and Raw. It returns the final
// response (body already fully read; resp.Body is a replayable reader over
// the same bytes), the raw body bytes, and the mapped error for non-2xx.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body []byte) (*http.Response, []byte, error) {
	cleanPath := strings.TrimLeft(path, "/")
	u := strings.TrimRight(c.opts.BaseURL, "/") + "/" + cleanPath
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	maxAttempts := c.opts.MaxRetries + 1
	var lastResp *http.Response

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, retryDelay(attempt-1, lastResp)); err != nil {
				return lastResp, nil, err
			}
		}

		req, err := c.newRequest(ctx, method, u, body)
		if err != nil {
			return nil, nil, err
		}

		c.verbosef("-> %s %s\n", method, "/"+cleanPath)
		start := time.Now()
		resp, err := c.hc.Do(req)
		elapsed := time.Since(start).Round(time.Millisecond)
		if err != nil {
			c.verbosef("<- error %s: %v\n", elapsed, redact(err, c.opts.Token))
			lastResp = nil
			if idempotent(method) && attempt < maxAttempts-1 {
				continue
			}
			return nil, nil, err
		}

		raw, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			c.verbosef("<- error %s: %v\n", elapsed, readErr)
			lastResp = nil
			if idempotent(method) && attempt < maxAttempts-1 {
				continue
			}
			return nil, nil, fmt.Errorf("read %s %s response: %w", method, path, readErr)
		}
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		c.verbosef("<- %d %s\n", resp.StatusCode, elapsed)

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, raw, nil
		}

		apiErr := newAPIError(method, cleanPath, resp, raw)
		lastResp = resp
		if retryableStatus(method, resp.StatusCode) && attempt < maxAttempts-1 {
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return resp, raw, &RateLimitError{APIError: apiErr, Attempts: attempt + 1}
		}
		return resp, raw, apiErr
	}
	// Unreachable: the loop always returns on its final attempt.
	return nil, nil, fmt.Errorf("%s %s: retry loop exhausted", method, path)
}

// newRequest builds one attempt's request with the standard header set.
// The body reader is rebuilt per attempt so retries replay the same bytes.
func (c *Client) newRequest(ctx context.Context, method, u string, body []byte) (*http.Request, error) {
	var rd io.Reader
	if len(body) > 0 {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Tallyfy-Client", ClientHeader)
	if c.opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.opts.Token)
	}
	if c.opts.UserAgent != "" {
		req.Header.Set("User-Agent", c.opts.UserAgent)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// newAPIError maps a non-2xx response. Message comes from a top-level
// {"message": "..."} whether or not an "error" key is present.
func newAPIError(method, path string, resp *http.Response, raw []byte) *APIError {
	msg := ""
	var env struct {
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(raw, &env) == nil && len(env.Message) > 0 {
		var s string
		if json.Unmarshal(env.Message, &s) == nil {
			msg = s
		}
	}
	body := raw
	if len(body) > errBodyCap {
		body = body[:errBodyCap]
	}
	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    msg,
		Body:       body,
		RequestID:  resp.Header.Get("X-Request-Id"),
		Method:     method,
		Path:       path,
	}
}

// idempotent reports whether a method may be retried on 5xx and transport
// errors. Mutating methods are retried only on 429 (the request was never
// admitted) — never on 5xx, where it may have partially executed.
func idempotent(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// retryableStatus reports whether a status warrants another attempt for the
// given method: 429 always; 502/503/504 for idempotent methods only.
func retryableStatus(method string, status int) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	if !idempotent(method) {
		return false
	}
	switch status {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// retryDelay computes the sleep before retry N (0-based). A Retry-After
// header on the previous response wins (integer seconds or HTTP-date, capped
// at maxRetryAfter); otherwise exponential backoff base*2^n with ±20% jitter,
// capped at MaxDelay.
func retryDelay(retry int, prev *http.Response) time.Duration {
	if prev != nil {
		if ra := strings.TrimSpace(prev.Header.Get("Retry-After")); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 {
				return minDuration(time.Duration(secs)*time.Second, maxRetryAfter)
			}
			if t, err := http.ParseTime(ra); err == nil {
				d := time.Until(t)
				if d < 0 {
					d = 0
				}
				return minDuration(d, maxRetryAfter)
			}
		}
	}
	base := defaultRetry.BaseDelay
	if retry > 6 {
		retry = 6 // avoid pointless shifts; capped below anyway
	}
	d := base << uint(retry)
	if d > defaultRetry.MaxDelay {
		d = defaultRetry.MaxDelay
	}
	d = time.Duration(float64(d) * (0.8 + 0.4*rand.Float64())) // ±20% jitter
	return minDuration(d, defaultRetry.MaxDelay)
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// sleepCtx sleeps for d, aborting early when ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// verbosef writes one trace line when Options.Verbose is set. The bearer
// token and headers are never written.
func (c *Client) verbosef(format string, args ...any) {
	if c.opts.Verbose == nil {
		return
	}
	fmt.Fprintf(c.opts.Verbose, format, args...)
}

// redact scrubs the bearer token from transport error strings (URL errors
// echo the request URL, never the token, but belt-and-braces).
func redact(err error, token string) string {
	s := err.Error()
	if token != "" {
		s = strings.ReplaceAll(s, token, "[REDACTED]")
	}
	return s
}
