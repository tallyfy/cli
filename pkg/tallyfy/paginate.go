package tallyfy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// allPerPage is the page size used when following every page and the caller
// did not pick one — the server cap, minimizing round trips.
const allPerPage = 100

// Paginate GETs one or more pages of a list endpoint and hands each to
// onPage. Iteration stops when onPage returns false or errors, after the
// first page when opts.All is false, or when the server signals no further
// pages (current_page >= total_pages; when total_pages is absent, an empty
// links.next). opts.Extra is merged into the query first so the iterator's
// own page counter always wins.
func (c *Client) Paginate(ctx context.Context, path string, opts ListOptions, onPage func(Page) (bool, error)) error {
	page := opts.Page
	if page < 1 {
		page = 1
	}
	perPage := opts.PerPage
	if opts.All && perPage == 0 {
		perPage = allPerPage
	}

	for {
		q := url.Values{}
		for k, v := range opts.Extra {
			q.Set(k, v)
		}
		if len(opts.With) > 0 {
			q.Set("with", strings.Join(opts.With, ","))
		}
		if perPage > 0 {
			q.Set("per_page", strconv.Itoa(perPage))
		}
		q.Set("page", strconv.Itoa(page))

		var env struct {
			Data json.RawMessage `json:"data"`
			Meta *Meta           `json:"meta"`
		}
		if _, err := c.Do(ctx, http.MethodGet, path, q, nil, &env); err != nil {
			return err
		}

		cont, err := onPage(Page{Data: env.Data, Meta: env.Meta})
		if err != nil {
			return err
		}
		if !cont || !opts.All {
			return nil
		}

		if env.Meta == nil || env.Meta.Pagination == nil {
			return nil // no pagination metadata: single page
		}
		p := env.Meta.Pagination
		switch {
		case p.TotalPages > 0:
			if p.CurrentPage >= p.TotalPages {
				return nil
			}
		case p.Links.Next == "":
			return nil // fallback signal: no next link
		}
		if p.CurrentPage > 0 {
			page = p.CurrentPage + 1
		} else {
			page++
		}
	}
}

// CollectAll pages through path, unmarshaling every page's data array into
// []T. opts.Limit truncates the result and stops fetching early. The returned
// *Pagination is the last page's metadata (nil when the endpoint sent none).
func CollectAll[T any](ctx context.Context, c *Client, path string, opts ListOptions) ([]T, *Pagination, error) {
	var (
		items []T
		last  *Pagination
	)
	err := c.Paginate(ctx, path, opts, func(pg Page) (bool, error) {
		if pg.Meta != nil && pg.Meta.Pagination != nil {
			last = pg.Meta.Pagination
		}
		if len(pg.Data) > 0 {
			var batch []T
			if err := json.Unmarshal(pg.Data, &batch); err != nil {
				return false, fmt.Errorf("decode %s page: %w", path, err)
			}
			items = append(items, batch...)
		}
		if opts.Limit > 0 && len(items) >= opts.Limit {
			items = items[:opts.Limit]
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return nil, last, err
	}
	return items, last, nil
}
