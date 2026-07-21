package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tallyfy/cli/pkg/tallyfy"
)

// Kick-off ("prerun") field resolution and value encoding.
//
// Two api-v2 rules drive everything here, and getting either wrong loses data
// silently rather than loudly:
//
//  1. The launch body's "prerun" object is keyed by each field's timeline_id.
//     RunRequestValidator::validatePrerun looks every supplied key up with
//     allKickOffFields()->firstWhere('timeline_id', $key) and `continue`s past
//     a miss, so a key that is a label or an alias is DROPPED without error -
//     and a required field then 422s with "<label> is required". So a human
//     name has to be resolved to the field's id before the request is sent.
//
//  2. Values are shape-checked per field type by FormValuesValidator. A
//     dropdown needs a {id, text} object whose text matches its option
//     exactly, a radio needs that same text as a bare scalar, a multiselect
//     needs a list of {id, text}, and a table needs one entry per column.
//     Sending a bare string where an object is required fails validation.
//
//     A file field is the exception that bites hardest: FormValuesValidator
//     has NO file arm, so a wrong shape is not caught here at all. It reaches
//     Task::updateCaptureValues, which foreachs the value, and a bare string
//     becomes a 500 rather than a 422. See encodeKickoffFile.

// kickoffMatchRank ranks how a user-supplied key matched a field. Lower is a
// stronger match; exact beats case-insensitive, and id beats alias beats label.
type kickoffMatchRank int

const (
	matchID kickoffMatchRank = iota
	matchAlias
	matchLabel
	matchAliasFold
	matchLabelFold
	matchNone
)

// rankKickoffMatch scores one field against one key.
func rankKickoffMatch(f tallyfy.KickoffField, key string) kickoffMatchRank {
	switch {
	case f.ID != "" && f.ID == key:
		return matchID
	case f.Alias != "" && f.Alias == key:
		return matchAlias
	case f.Label != "" && f.Label == key:
		return matchLabel
	case f.Alias != "" && strings.EqualFold(f.Alias, key):
		return matchAliasFold
	case f.Label != "" && strings.EqualFold(f.Label, key):
		return matchLabelFold
	}
	return matchNone
}

// resolveKickoffKey maps one user-supplied key to the kick-off field it names.
// A field id passes through unchanged; a label or alias is looked up. Exact
// matches win over case-insensitive ones, and a key that matches two different
// fields equally well is an error rather than a coin toss.
func resolveKickoffKey(fields []tallyfy.KickoffField, blueprintID, key string) (tallyfy.KickoffField, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return tallyfy.KickoffField{}, &UsageError{Msg: "empty kick-off field name"}
	}
	var (
		best     tallyfy.KickoffField
		bestRank = matchNone
		tied     []string
	)
	for _, f := range fields {
		switch r := rankKickoffMatch(f, key); {
		case r < bestRank:
			best, bestRank, tied = f, r, []string{kickoffFieldName(f)}
		case r == bestRank && r != matchNone:
			tied = append(tied, kickoffFieldName(f))
		}
	}
	switch {
	case bestRank == matchNone:
		return tallyfy.KickoffField{}, &UsageError{Msg: unknownKickoffFieldMsg(fields, blueprintID, key)}
	case len(tied) > 1:
		return tallyfy.KickoffField{}, &UsageError{Msg: fmt.Sprintf(
			"ambiguous kick-off field %q on blueprint %s: it matches %d fields (%s) - use the field ID instead",
			key, blueprintID, len(tied), strings.Join(tied, ", "))}
	}
	return best, nil
}

// unknownKickoffFieldMsg is what a user sees when a key names no kick-off
// field. Naming the key is half the fix; listing what does exist is the rest.
func unknownKickoffFieldMsg(fields []tallyfy.KickoffField, blueprintID, key string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "unknown kick-off field %q on blueprint %s", key, blueprintID)
	if len(fields) == 0 {
		b.WriteString("\n  this blueprint has no kick-off form fields")
		return b.String()
	}
	b.WriteString("\n  available fields:")
	for _, f := range fields {
		fmt.Fprintf(&b, "\n    %-28s %-16s id=%s", kickoffFieldName(f), "("+f.FieldType+")", f.ID)
		if f.Alias != "" && f.Alias != kickoffFieldName(f) {
			fmt.Fprintf(&b, "  alias=%s", f.Alias)
		}
	}
	return b.String()
}

// kickoffFieldName is the friendliest identifier for a field in messages.
func kickoffFieldName(f tallyfy.KickoffField) string {
	switch {
	case f.Label != "":
		return f.Label
	case f.Alias != "":
		return f.Alias
	}
	return f.ID
}

// resolveKickoffKeys maps every supplied key to its kick-off field. Resolving
// the whole key set up front is deliberate: a CSV whose header names a field
// that does not exist fails before a single process is launched, instead of
// launching every row with a column silently missing.
func resolveKickoffKeys(fields []tallyfy.KickoffField, blueprintID string, keys []string) (map[string]tallyfy.KickoffField, error) {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted) // deterministic: the same bad key is reported every run
	out := make(map[string]tallyfy.KickoffField, len(sorted))
	claimed := make(map[string]string, len(sorted))
	for _, k := range sorted {
		f, err := resolveKickoffKey(fields, blueprintID, k)
		if err != nil {
			return nil, err
		}
		if prev, dup := claimed[f.ID]; dup {
			return nil, &UsageError{Msg: fmt.Sprintf(
				"kick-off fields %q and %q both name %q (id=%s) - keep only one",
				prev, k, kickoffFieldName(f), f.ID)}
		}
		claimed[f.ID] = k
		out[k] = f
	}
	return out, nil
}

// kickoffNeedsMembers reports whether any resolved field is an assignees_form,
// the only type whose encoding needs the org's member list.
func kickoffNeedsMembers(resolved map[string]tallyfy.KickoffField) bool {
	for _, f := range resolved {
		if f.FieldType == "assignees_form" {
			return true
		}
	}
	return false
}

// encodePrerun turns one set of raw values into the "prerun" object: keyed by
// timeline_id, with each value encoded for its field's type.
func encodePrerun(resolved map[string]tallyfy.KickoffField, values map[string]string, members map[string]json.Number) (map[string]any, error) {
	if len(values) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]any, len(values))
	for _, k := range keys {
		f, ok := resolved[k]
		if !ok {
			return nil, &UsageError{Msg: fmt.Sprintf("kick-off field %q was never resolved to a field ID", k)}
		}
		v, err := encodeKickoffValue(f, values[k], members)
		if err != nil {
			return nil, err
		}
		out[f.ID] = v
	}
	return out, nil
}

// encodeKickoffValue converts a raw CLI/CSV string into the JSON value shape
// FormValuesValidator requires for the field's type. members maps a lowercased
// email to that member's id and is consulted only for assignees_form fields.
func encodeKickoffValue(f tallyfy.KickoffField, raw string, members map[string]json.Number) (any, error) {
	// An empty cell clears the field. api-v2 accepts an empty value for every
	// type, and a required field still fails its own required check, so pass
	// it straight through rather than inventing an empty object or list.
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	switch f.FieldType {
	case "dropdown":
		opt, err := matchKickoffOption(f, raw)
		if err != nil {
			return nil, err
		}
		return kickoffOptionValue(opt, false), nil

	case "multiselect":
		// Only the chosen options are sent, each flagged selected, matching
		// the Zapier connector. The flag is load-bearing beyond validation:
		// VariableReplacement::replaceVariableForMultiSelect renders only
		// options where `selected` is truthy, so a value without it stores
		// fine and then renders as nothing wherever the field is used as a
		// variable. (Dropdown needs no flag - replaceVariableForDropdown
		// reads `text` directly.)
		parts := splitKickoffList(raw)
		out := make([]map[string]any, 0, len(parts))
		for _, p := range parts {
			opt, err := matchKickoffOption(f, p)
			if err != nil {
				return nil, err
			}
			out = append(out, kickoffOptionValue(opt, true))
		}
		return out, nil

	case "radio":
		// Asymmetric with dropdown by design: FormValuesValidator checks a
		// radio with in_array($values, $id_text_array), so it wants the
		// option's text as a bare scalar, not the {id, text} object.
		opt, err := matchKickoffOption(f, raw)
		if err != nil {
			return nil, err
		}
		return opt.Text, nil

	case "table":
		return encodeKickoffTable(f, raw)

	case "file":
		return encodeKickoffFile(f, raw)

	case "assignees_form":
		return encodeKickoffAssignees(f, raw, members)
	}
	// text, textarea, email, date and any type added later go through as the
	// scalar the user typed. api-v2's full set is text, textarea, radio,
	// dropdown, multiselect, date, email, file, table, assignees_form
	// (BaseCapture::$field_types), so every non-scalar type is handled above.
	return raw, nil
}

// encodeKickoffFile builds the list-of-objects shape a file field requires.
//
// This is not cosmetic. Nothing validates a file value on the way in -
// FormValuesValidator has no `file` arm at all - so a wrong shape is not
// rejected with a clean 422, it reaches storage and throws. Task::
// updateCaptureValues does `foreach ($payload as $item)` over the value, so
// the URL a user naturally types is a hard 500 ("foreach() argument must be
// of type array|object, string given"), and RunsRepository::captureRunValue
// foreachs the same value again on the prerun path.
//
// The object mirrors the two shipped connectors (middleware processFieldValue,
// migrator prerun_encoder) and api-v2's own Swagger for form_value:
//
//   - filename is what every consumer reads - exports (RunsRepository),
//     rule evaluation (CaptureTypeFactory plucks it), and rendering
//     (replaceVariableForFile, is_image). A `name` key is read nowhere.
//   - url is what api-v2 expands into full_url on store.
//   - source marks the file as referenced by URL rather than uploaded; it
//     gates the file-updated-activity event in Task::updateCaptureValues.
//
// api-v2 adds full_url and uploaded_at itself, so neither is sent.
func encodeKickoffFile(f tallyfy.KickoffField, raw string) (any, error) {
	trimmed := strings.TrimSpace(raw)

	// A JSON array or object is taken as already being in the API's shape, so
	// a value read out of an export can be launched straight back in. Each
	// entry is still checked, because a list of bare strings would reach the
	// same foreach and fail on Arr::has() instead.
	if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{") {
		return encodeKickoffFileJSON(f, trimmed)
	}

	// Otherwise the value is one URL, or several separated by commas.
	urls := splitKickoffList(raw)
	if len(urls) == 0 {
		return "", nil
	}
	out := make([]map[string]any, 0, len(urls))
	for _, u := range urls {
		out = append(out, kickoffFileValue(u))
	}
	return out, nil
}

// encodeKickoffFileJSON handles a file value the user supplied as JSON: a list
// of file objects, a single object, or a list of URL strings.
func encodeKickoffFileJSON(f tallyfy.KickoffField, trimmed string) (any, error) {
	if strings.HasPrefix(trimmed, "{") {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
			return nil, &UsageError{Msg: fmt.Sprintf(
				"kick-off field %q (file) is not valid JSON: %v", kickoffFieldName(f), err)}
		}
		if err := checkKickoffFileObject(f, obj); err != nil {
			return nil, err
		}
		// Always a list: the API foreachs this value.
		return []map[string]json.RawMessage{obj}, nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
		return nil, &UsageError{Msg: fmt.Sprintf(
			"kick-off field %q (file) is not valid JSON: %v", kickoffFieldName(f), err)}
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		switch first := firstJSONByte(item); first {
		case '"':
			var u string
			if err := json.Unmarshal(item, &u); err != nil {
				return nil, &UsageError{Msg: fmt.Sprintf(
					"kick-off field %q (file) has an unreadable entry: %v", kickoffFieldName(f), err)}
			}
			out = append(out, kickoffFileValue(u))
		case '{':
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(item, &obj); err != nil {
				return nil, &UsageError{Msg: fmt.Sprintf(
					"kick-off field %q (file) has an unreadable entry: %v", kickoffFieldName(f), err)}
			}
			if err := checkKickoffFileObject(f, obj); err != nil {
				return nil, err
			}
			out = append(out, obj)
		default:
			return nil, &UsageError{Msg: fmt.Sprintf(
				"kick-off field %q (file) needs each entry to be a URL string or a file object, but got %s",
				kickoffFieldName(f), string(item))}
		}
	}
	return out, nil
}

// firstJSONByte is the first non-space byte of a JSON value, or 0 if empty.
func firstJSONByte(b json.RawMessage) byte {
	if t := strings.TrimSpace(string(b)); t != "" {
		return t[0]
	}
	return 0
}

// checkKickoffFileObject rejects a file object api-v2 could not use. url is
// what becomes full_url; an already-enriched object carrying full_url is
// passed through untouched by Task::updateCaptureValues, so either key alone
// is enough.
func checkKickoffFileObject(f tallyfy.KickoffField, obj map[string]json.RawMessage) error {
	if _, ok := obj["url"]; ok {
		return nil
	}
	if _, ok := obj["full_url"]; ok {
		return nil
	}
	return &UsageError{Msg: fmt.Sprintf(
		"kick-off field %q (file) needs a %q on every file object", kickoffFieldName(f), "url")}
}

// kickoffFileValue wraps one URL in the object api-v2 stores.
func kickoffFileValue(url string) map[string]any {
	return map[string]any{
		"filename": kickoffFileName(url),
		"source":   "url",
		"url":      url,
	}
}

// kickoffFileName is the last path segment of a URL, matching the connectors'
// value.split('/').pop(). Deliberately stricter than they are in one respect:
// a query string or fragment is trimmed first, so a signed URL yields
// "report.pdf" rather than "report.pdf?X-Amz-Signature=...". That name is what
// users see wherever the field is rendered or exported.
func kickoffFileName(url string) string {
	s := url
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return url
	}
	return s
}

// kickoffOptionValue is the {id, text} pair api-v2 requires for dropdown and
// multiselect values: both keys must be present and text must equal the
// option's own text exactly. selected adds the flag multiselect rendering
// depends on.
func kickoffOptionValue(o tallyfy.KickoffOption, selected bool) map[string]any {
	v := map[string]any{"id": o.ID, "text": o.Text}
	if selected {
		v["selected"] = true
	}
	return v
}

// matchKickoffOption finds the option whose text is what the user typed.
// Exact wins; a case-insensitive match is accepted as a convenience, and the
// option's own text is always what gets sent.
func matchKickoffOption(f tallyfy.KickoffField, raw string) (tallyfy.KickoffOption, error) {
	raw = strings.TrimSpace(raw)
	for _, o := range f.Options {
		if o.Text == raw {
			return o, nil
		}
	}
	for _, o := range f.Options {
		if strings.EqualFold(o.Text, raw) {
			return o, nil
		}
	}
	if len(f.Options) == 0 {
		return tallyfy.KickoffOption{}, &UsageError{Msg: fmt.Sprintf(
			"kick-off field %q (%s) defines no options, so %q cannot be matched",
			kickoffFieldName(f), f.FieldType, raw)}
	}
	texts := make([]string, 0, len(f.Options))
	for _, o := range f.Options {
		texts = append(texts, strconv.Quote(o.Text))
	}
	return tallyfy.KickoffOption{}, &UsageError{Msg: fmt.Sprintf(
		"invalid value %q for kick-off field %q (%s): choose one of %s",
		raw, kickoffFieldName(f), f.FieldType, strings.Join(texts, ", "))}
}

// splitKickoffList splits a multi-value cell on commas. A JSON array is
// accepted too, for option texts that contain a comma themselves.
func splitKickoffList(raw string) []string {
	if trimmed := strings.TrimSpace(raw); strings.HasPrefix(trimmed, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
			return arr
		}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// encodeKickoffTable passes a table value through as JSON. api-v2 requires an
// array holding exactly one entry per defined column.
func encodeKickoffTable(f tallyfy.KickoffField, raw string) (any, error) {
	var rows []any
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, &UsageError{Msg: fmt.Sprintf(
			"kick-off field %q (table) needs a JSON array value: %v", kickoffFieldName(f), err)}
	}
	if n := len(f.Columns); n > 0 && len(rows) != n {
		return nil, &UsageError{Msg: fmt.Sprintf(
			"kick-off field %q (table) needs exactly %d entries, one per column, but got %d",
			kickoffFieldName(f), n, len(rows))}
	}
	return rows, nil
}

// encodeKickoffAssignees builds the {users, guests, groups} object api-v2
// requires. A JSON object passes through (after a key check); otherwise the
// value is read as a comma-separated email list and each address is classified
// as an org member (users) or an outsider (guests), mirroring the Zapier
// connector's processFieldValue / transformAssigneesFormValue.
func encodeKickoffAssignees(f tallyfy.KickoffField, raw string, members map[string]json.Number) (any, error) {
	if trimmed := strings.TrimSpace(raw); strings.HasPrefix(trimmed, "{") {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
			return nil, &UsageError{Msg: fmt.Sprintf(
				"kick-off field %q (assignees_form) is not valid JSON: %v", kickoffFieldName(f), err)}
		}
		for k := range obj {
			if k != "users" && k != "guests" && k != "groups" {
				return nil, &UsageError{Msg: fmt.Sprintf(
					"kick-off field %q (assignees_form) has unknown key %q (want users, guests or groups)",
					kickoffFieldName(f), k)}
			}
		}
		return obj, nil
	}
	users := []json.Number{}
	guests := []string{}
	for _, email := range splitKickoffList(raw) {
		if id, ok := members[strings.ToLower(email)]; ok {
			users = append(users, id)
			continue
		}
		guests = append(guests, email)
	}
	return map[string]any{"users": users, "guests": guests, "groups": []string{}}, nil
}
