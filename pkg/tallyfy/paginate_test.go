package tallyfy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
)

// pagedHandler serves /items with totalPages pages of itemsPerPage sequential
// ints, using current_page/total_pages metadata and links in the shapes the
// live API emits (object when a next page exists, empty array on the last).
func pagedHandler(t *testing.T, totalPages, itemsPerPage int, calls *[]string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.URL.RawQuery)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		items := make([]int, 0, itemsPerPage)
		for i := 0; i < itemsPerPage; i++ {
			items = append(items, (page-1)*itemsPerPage+i+1)
		}
		links := `[]`
		if page < totalPages {
			links = fmt.Sprintf(`{"next":"https://x/items?page=%d"}`, page+1)
		}
		data, _ := json.Marshal(items)
		_, _ = fmt.Fprintf(w, `{"data":%s,"meta":{"pagination":{"total":%d,"count":%d,"per_page":%d,"current_page":%d,"total_pages":%d,"links":%s}}}`,
			data, totalPages*itemsPerPage, len(items), itemsPerPage, page, totalPages, links)
	}
}

func TestPaginateSinglePageWhenNotAll(t *testing.T) {
	var calls []string
	c := newTestClient(t, -1, pagedHandler(t, 3, 2, &calls))
	var pages int
	err := c.Paginate(context.Background(), "items", ListOptions{}, func(pg Page) (bool, error) {
		pages++
		if pg.Meta == nil || pg.Meta.Pagination == nil {
			t.Error("page missing pagination meta")
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if pages != 1 || len(calls) != 1 {
		t.Errorf("pages = %d, requests = %d; want 1 page and 1 request without All", pages, len(calls))
	}
}

func TestPaginateAllFollowsTotalPages(t *testing.T) {
	var calls []string
	c := newTestClient(t, -1, pagedHandler(t, 3, 2, &calls))
	var seen []int
	err := c.Paginate(context.Background(), "items", ListOptions{All: true}, func(pg Page) (bool, error) {
		var batch []int
		if err := json.Unmarshal(pg.Data, &batch); err != nil {
			return false, err
		}
		seen = append(seen, batch...)
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("requests = %d, want 3: %v", len(calls), calls)
	}
	want := []int{1, 2, 3, 4, 5, 6}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("items = %v, want %v", seen, want)
	}
	// All && PerPage==0 must request the server max page size.
	for i, q := range calls {
		if want, got := "per_page=100", q; !containsParam(got, "per_page", "100") {
			t.Errorf("request %d query %q missing %s", i+1, got, want)
		}
		if !containsParam(q, "page", strconv.Itoa(i+1)) {
			t.Errorf("request %d query %q wrong page", i+1, q)
		}
	}
}

func TestPaginateLinksFallbackWhenNoTotalPages(t *testing.T) {
	var calls []string
	c := newTestClient(t, -1, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.RawQuery)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		links := `[]`
		if page < 2 {
			links = `{"next":"https://x/items?page=2"}`
		}
		// No total/total_pages fields at all — only links signal more pages.
		_, _ = fmt.Fprintf(w, `{"data":[%d],"meta":{"pagination":{"current_page":%d,"links":%s}}}`, page, page, links)
	})
	var pages int
	err := c.Paginate(context.Background(), "items", ListOptions{All: true}, func(_ Page) (bool, error) {
		pages++
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if pages != 2 || len(calls) != 2 {
		t.Errorf("pages = %d, requests = %d; want 2 via links fallback", pages, len(calls))
	}
}

func TestPaginateStopsWhenOnPageReturnsFalse(t *testing.T) {
	var calls []string
	c := newTestClient(t, -1, pagedHandler(t, 5, 2, &calls))
	err := c.Paginate(context.Background(), "items", ListOptions{All: true}, func(_ Page) (bool, error) {
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Errorf("requests = %d, want 1 after onPage returned false", len(calls))
	}
}

func TestPaginatePropagatesOnPageError(t *testing.T) {
	var calls []string
	c := newTestClient(t, -1, pagedHandler(t, 5, 2, &calls))
	boom := errors.New("boom")
	err := c.Paginate(context.Background(), "items", ListOptions{All: true}, func(_ Page) (bool, error) {
		return true, boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
}

func TestPaginateQueryParams(t *testing.T) {
	var query string
	c := newTestClient(t, -1, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = fmt.Fprint(w, `{"data":[]}`)
	})
	opts := ListOptions{
		Page:    2,
		PerPage: 5,
		With:    []string{"steps", "tags"},
		Extra:   map[string]string{"status": "active", "page": "99"}, // page must not be overridable
	}
	if err := c.Paginate(context.Background(), "items", opts, func(Page) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"page": "2", "per_page": "5", "with": "steps,tags", "status": "active"} {
		if !containsParam(query, k, v) {
			t.Errorf("query %q missing %s=%s", query, k, v)
		}
	}
}

func TestCollectAllAppliesLimit(t *testing.T) {
	var calls []string
	c := newTestClient(t, -1, pagedHandler(t, 5, 2, &calls))
	items, pg, err := CollectAll[int](context.Background(), c, "items", ListOptions{All: true, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Errorf("items = %v, want exactly 3 (limit truncation)", items)
	}
	if len(calls) != 2 {
		t.Errorf("requests = %d, want 2 (early stop at limit)", len(calls))
	}
	if pg == nil || pg.CurrentPage != 2 {
		t.Errorf("last pagination = %+v, want current_page 2", pg)
	}
}

func TestCollectAllTypedDecode(t *testing.T) {
	c := newTestClient(t, -1, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[{"id":"b1","title":"Onboard"},{"id":"b2","title":"Offboard"}],`+
			`"meta":{"pagination":{"total":2,"count":2,"per_page":10,"current_page":1,"total_pages":1,"links":[]}}}`)
	})
	bps, pg, err := CollectAll[Blueprint](context.Background(), c, "organizations/o/checklists", ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(bps) != 2 || bps[0].ID != "b1" || bps[1].Title != "Offboard" {
		t.Errorf("decoded blueprints wrong: %+v", bps)
	}
	if pg == nil || pg.Total != 2 {
		t.Errorf("pagination = %+v", pg)
	}
}

// containsParam reports whether rawQuery contains key=value once decoded.
func containsParam(rawQuery, key, value string) bool {
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return false
	}
	return q.Get(key) == value
}
