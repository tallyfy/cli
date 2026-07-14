package tallyfy

import (
	"encoding/json"
	"testing"
)

func TestPaginationLinksTolerantUnmarshal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want PaginationLinks
	}{
		{"object both", `{"previous":"https://api/prev","next":"https://api/next"}`,
			PaginationLinks{Previous: "https://api/prev", Next: "https://api/next"}},
		{"object next only", `{"next":"https://api/next"}`, PaginationLinks{Next: "https://api/next"}},
		{"object with nulls", `{"previous":null,"next":null}`, PaginationLinks{}},
		{"empty array", `[]`, PaginationLinks{}},
		{"null", `null`, PaginationLinks{}},
		{"empty object", `{}`, PaginationLinks{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Seed with junk to prove UnmarshalJSON resets state.
			l := PaginationLinks{Previous: "junk", Next: "junk"}
			if err := json.Unmarshal([]byte(tc.in), &l); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.in, err)
			}
			if l != tc.want {
				t.Errorf("got %+v, want %+v", l, tc.want)
			}
		})
	}
}

func TestPaginationDecodeMissingLinks(t *testing.T) {
	var p Pagination
	in := `{"total":57,"count":10,"per_page":10,"current_page":2,"total_pages":6}`
	if err := json.Unmarshal([]byte(in), &p); err != nil {
		t.Fatal(err)
	}
	if p.Links != (PaginationLinks{}) {
		t.Errorf("missing links should stay zero: %+v", p.Links)
	}
	if p.Total != 57 || p.CurrentPage != 2 || p.TotalPages != 6 {
		t.Errorf("pagination fields wrong: %+v", p)
	}
}

func TestPaginationDecodeRealisticMeta(t *testing.T) {
	// Shape captured from the live API (api-support Backup Blueprints scripts).
	in := `{"pagination":{"total":25,"count":10,"per_page":10,"current_page":1,` +
		`"total_pages":3,"links":{"next":"https://api.tallyfy.com/organizations/o/checklists?page=2"}}}`
	var m Meta
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatal(err)
	}
	if m.Pagination == nil || m.Pagination.Links.Next == "" {
		t.Fatalf("pagination not decoded: %+v", m.Pagination)
	}
	if m.Pagination.Links.Previous != "" {
		t.Errorf("previous should be empty, got %q", m.Pagination.Links.Previous)
	}
}

func TestPaginationLinksMarshalRoundTrip(t *testing.T) {
	l := PaginationLinks{Next: "https://api/next"}
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	var back PaginationLinks
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back != l {
		t.Errorf("round trip: got %+v, want %+v", back, l)
	}
}
