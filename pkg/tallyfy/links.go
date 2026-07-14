package tallyfy

import (
	"bytes"
	"encoding/json"
)

// UnmarshalJSON tolerates every shape the API emits for meta.pagination.links:
//
//	object:      {"previous": "https://...", "next": "https://..."}
//	object/null: {"previous": null, "next": "https://..."}
//	empty array: []            (PHP json_encode of an empty assoc array)
//	null:        null
//	missing:     (field absent — encoding/json never calls this)
func (l *PaginationLinks) UnmarshalJSON(b []byte) error {
	*l = PaginationLinks{}
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return nil
	}
	if b[0] == '[' {
		// Empty (or any) array form carries no usable link data.
		return nil
	}
	var obj struct {
		Previous *string `json:"previous"`
		Next     *string `json:"next"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	if obj.Previous != nil {
		l.Previous = *obj.Previous
	}
	if obj.Next != nil {
		l.Next = *obj.Next
	}
	return nil
}

// MarshalJSON emits the object form so round-trips stay stable.
func (l PaginationLinks) MarshalJSON() ([]byte, error) {
	obj := struct {
		Previous string `json:"previous,omitempty"`
		Next     string `json:"next,omitempty"`
	}{Previous: l.Previous, Next: l.Next}
	return json.Marshal(obj)
}
