package tallyfy

import (
	"context"
	"net/url"
)

// Raw performs an arbitrary API request with the standard header set and
// retry policy but no envelope decoding — the passthrough behind
// `tallyfy api`. The full response body is returned even on non-2xx, where
// err is the mapped *APIError / *RateLimitError so callers keep exit-code
// category mapping. status is 0 when no response was received (transport
// failure or context cancellation).
func (c *Client) Raw(ctx context.Context, method, path string, query url.Values, body []byte) (int, []byte, error) {
	resp, respBody, err := c.do(ctx, method, path, query, body)
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	return status, respBody, err
}
