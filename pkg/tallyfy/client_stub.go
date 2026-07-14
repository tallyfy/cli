package tallyfy

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// New builds a Client. REPLACED BY LANE L2 (client.go).
func New(opts Options) *Client {
	if opts.BaseURL == "" {
		opts.BaseURL = DefaultBaseURL
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{opts: opts, hc: hc}
}

// Do performs one API request. REPLACED BY LANE L2.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any, out any) (*http.Response, error) {
	return nil, errors.New("tallyfy client not implemented (L2)")
}

// Paginate iterates list pages. REPLACED BY LANE L2.
func (c *Client) Paginate(ctx context.Context, path string, opts ListOptions, onPage func(Page) (bool, error)) error {
	return errors.New("tallyfy client not implemented (L2)")
}
