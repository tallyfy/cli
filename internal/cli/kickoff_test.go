package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/tallyfy/cli/pkg/tallyfy"
)

// kickoffFixture mirrors a real GET /organizations/{org}/checklists/{id}
// "prerun" array (shapes captured from the live API): ids are timeline_ids,
// aliases are slugs, and option ids are numbers.
func kickoffFixture() []tallyfy.KickoffField {
	return []tallyfy.KickoffField{
		{
			ID: "bbd6a81fbb1da0b90610bf9560da339d", Alias: "stf-8308338",
			Label: "STF KO", FieldType: "text",
		},
		{
			ID: "84fd4089e8b739537ce22e63ee78f8fe", Alias: "dd-ko-8308340",
			Label: "DD KO", FieldType: "dropdown",
			Options: []tallyfy.KickoffOption{
				{ID: json.RawMessage("1"), Text: "YES"},
				{ID: json.RawMessage("2"), Text: "NO"},
			},
		},
		{
			ID: "30a8310b03cd142a9cf339359e56833d", Alias: "radio-8308342",
			Label: "RADIO KO", FieldType: "radio",
			Options: []tallyfy.KickoffOption{
				{ID: json.RawMessage("1"), Text: "RAD 1"},
				{ID: json.RawMessage("2"), Text: "RAD 2"},
			},
		},
		{
			ID: "98f376ec4b3c98349444bbb1664e61a8", Alias: "checklist-8308341",
			Label: "CHECKLIST KO", FieldType: "multiselect",
			Options: []tallyfy.KickoffOption{
				{ID: json.RawMessage("1"), Text: "CHK 1"},
				{ID: json.RawMessage("2"), Text: "CHK 2"},
			},
		},
		{
			ID: "2d16159369e95b1740e24b8d9aaff44b", Alias: "table-ko-8308345",
			Label: "TABLE KO", FieldType: "table",
			Columns: []tallyfy.KickoffOption{
				{ID: json.RawMessage("1"), Label: "T1"},
				{ID: json.RawMessage("2"), Label: "T2"},
			},
		},
		{
			ID: "8ed941292bf809e597e9e93be679342b", Alias: "assignee-picker-ko-8308346",
			Label: "ASSIGNEE PICKER KO", FieldType: "assignees_form",
		},
		{
			ID: "5c9a3f1e2d8b47a6913e05f8c2b71d4a", Alias: "file-ko-8308347",
			Label: "FILE KO", FieldType: "file",
		},
		// The remaining scalar types. api-v2's full set is text, textarea,
		// radio, dropdown, multiselect, date, email, file, table,
		// assignees_form (BaseCapture::$field_types); with these the fixture
		// covers all ten, so every type has encoder coverage.
		{
			ID: "c1f7b2e6a09d4358bb1e7f30d5a86c92", Alias: "ltf-8308348",
			Label: "LTF KO", FieldType: "textarea",
		},
		{
			ID: "e4d0c8a51b6f49329a7c2e18f0b3d76e", Alias: "email-ko-8308349",
			Label: "EMAIL KO", FieldType: "email",
		},
		{
			ID: "f8a25e93c7d14b06821f9a4e6c05b3d7", Alias: "date-ko-8308350",
			Label: "DATE KO", FieldType: "date",
		},
	}
}

// kickoffField returns the fixture field with the given label.
func kickoffField(t *testing.T, label string) tallyfy.KickoffField {
	t.Helper()
	for _, f := range kickoffFixture() {
		if f.Label == label {
			return f
		}
	}
	t.Fatalf("fixture has no field labelled %q", label)
	return tallyfy.KickoffField{}
}

func TestResolveKickoffKey(t *testing.T) {
	fields := kickoffFixture()
	tests := []struct {
		name   string
		key    string
		wantID string
	}{
		{
			name: "a timeline_id passes through unchanged",
			key:  "84fd4089e8b739537ce22e63ee78f8fe", wantID: "84fd4089e8b739537ce22e63ee78f8fe",
		},
		{name: "exact alias", key: "dd-ko-8308340", wantID: "84fd4089e8b739537ce22e63ee78f8fe"},
		{name: "exact label", key: "DD KO", wantID: "84fd4089e8b739537ce22e63ee78f8fe"},
		{name: "label, case-insensitive", key: "dd ko", wantID: "84fd4089e8b739537ce22e63ee78f8fe"},
		{name: "alias, case-insensitive", key: "STF-8308338", wantID: "bbd6a81fbb1da0b90610bf9560da339d"},
		{name: "surrounding whitespace is trimmed", key: "  TABLE KO  ", wantID: "2d16159369e95b1740e24b8d9aaff44b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveKickoffKey(fields, "bp-123", tc.key)
			if err != nil {
				t.Fatalf("resolveKickoffKey(%q) error: %v", tc.key, err)
			}
			if got.ID != tc.wantID {
				t.Errorf("resolveKickoffKey(%q) = %q, want %q", tc.key, got.ID, tc.wantID)
			}
		})
	}
}

func TestResolveKickoffKeyUnknown(t *testing.T) {
	// The whole point of the fix: an unrecognised key must fail loudly and
	// say what IS available, instead of being dropped by api-v2 in silence.
	_, err := resolveKickoffKey(kickoffFixture(), "bp-123", "manager")
	msg := wantUsageError(t, err).Error()

	for _, want := range []string{
		`unknown kick-off field "manager" on blueprint bp-123`,
		"available fields:",
		"DD KO",
		"(dropdown)",
		"id=84fd4089e8b739537ce22e63ee78f8fe",
		"alias=dd-ko-8308340",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message is missing %q:\n%s", want, msg)
		}
	}
}

func TestResolveKickoffKeyNoFields(t *testing.T) {
	_, err := resolveKickoffKey(nil, "bp-123", "manager")
	if msg := wantUsageError(t, err).Error(); !strings.Contains(msg, "no kick-off form fields") {
		t.Errorf("error message = %q, want it to say the blueprint has no kick-off fields", msg)
	}
}

func TestResolveKickoffKeyAmbiguous(t *testing.T) {
	// Two fields sharing a label: guessing would silently write the wrong one.
	fields := []tallyfy.KickoffField{
		{ID: "id-one", Alias: "owner-1", Label: "Owner", FieldType: "text"},
		{ID: "id-two", Alias: "owner-2", Label: "Owner", FieldType: "text"},
	}
	_, err := resolveKickoffKey(fields, "bp-123", "Owner")
	if msg := wantUsageError(t, err).Error(); !strings.Contains(msg, "ambiguous") || !strings.Contains(msg, "use the field ID") {
		t.Errorf("error message = %q, want an ambiguity error pointing at the field ID", msg)
	}
}

func TestResolveKickoffKeyExactBeatsFold(t *testing.T) {
	// An exact alias hit must win over another field's case-insensitive one,
	// rather than tripping the ambiguity guard.
	fields := []tallyfy.KickoffField{
		{ID: "id-fold", Alias: "PRIORITY", Label: "Escalation", FieldType: "text"},
		{ID: "id-exact", Alias: "priority", Label: "Priority level", FieldType: "text"},
	}
	got, err := resolveKickoffKey(fields, "bp-123", "priority")
	if err != nil {
		t.Fatalf("resolveKickoffKey error: %v", err)
	}
	if got.ID != "id-exact" {
		t.Errorf("resolved to %q, want the exact-match field id-exact", got.ID)
	}
}

func TestResolveKickoffKeysDuplicate(t *testing.T) {
	// A CSV carrying both the label column and the alias column for one field
	// would otherwise let one value overwrite the other without a word.
	_, err := resolveKickoffKeys(kickoffFixture(), "bp-123", []string{"DD KO", "dd-ko-8308340"})
	if msg := wantUsageError(t, err).Error(); !strings.Contains(msg, "both name") {
		t.Errorf("error message = %q, want a duplicate-field error", msg)
	}
}

func TestEncodeKickoffValue(t *testing.T) {
	byLabel := map[string]tallyfy.KickoffField{}
	for _, f := range kickoffFixture() {
		byLabel[f.Label] = f
	}
	members := map[string]json.Number{"jo@example.com": json.Number("42")}

	tests := []struct {
		name  string
		field string
		raw   string
		want  any
	}{
		{
			name:  "text passes through as a scalar",
			field: "STF KO", raw: "hello", want: "hello",
		},
		{
			// api-v2 accepts an empty value for every type EXCEPT table, and a
			// required field still fails its own required check. The table
			// exception is pinned by TestEncodeKickoffBlankTableIsOmitted.
			name:  "empty value stays an empty scalar",
			field: "DD KO", raw: "", want: "",
		},
		{
			// FormValuesValidator wants BOTH keys, with text matching exactly.
			name:  "dropdown becomes an {id, text} object",
			field: "DD KO", raw: "YES",
			want: map[string]any{"id": json.RawMessage("1"), "text": "YES"},
		},
		{
			name:  "dropdown matches an option case-insensitively but sends the option's own text",
			field: "DD KO", raw: "yes",
			want: map[string]any{"id": json.RawMessage("1"), "text": "YES"},
		},
		{
			// Deliberately asymmetric with dropdown: radio is checked with
			// in_array($values, $id_text_array), so it wants a bare scalar.
			name:  "radio becomes the option's bare text",
			field: "RADIO KO", raw: "RAD 2", want: "RAD 2",
		},
		{
			// selected is load-bearing: replaceVariableForMultiSelect renders
			// only options carrying it, so without it the value stores but
			// renders as nothing wherever the field is used as a variable.
			name:  "multiselect becomes a list of selected {id, text}",
			field: "CHECKLIST KO", raw: "CHK 1, CHK 2",
			want: []map[string]any{
				{"id": json.RawMessage("1"), "text": "CHK 1", "selected": true},
				{"id": json.RawMessage("2"), "text": "CHK 2", "selected": true},
			},
		},
		{
			name:  "multiselect accepts a JSON array for texts containing commas",
			field: "CHECKLIST KO", raw: `["CHK 2"]`,
			want: []map[string]any{{"id": json.RawMessage("2"), "text": "CHK 2", "selected": true}},
		},
		{
			name:  "table passes through as a JSON array",
			field: "TABLE KO", raw: `[{"T1":"a"},{"T2":"b"}]`,
			want: []any{
				map[string]any{"T1": "a"},
				map[string]any{"T2": "b"},
			},
		},
		{
			name:  "assignees_form splits emails into members and guests",
			field: "ASSIGNEE PICKER KO", raw: "jo@example.com, outsider@acme.example",
			want: map[string]any{
				"users":  []json.Number{json.Number("42")},
				"guests": []string{"outsider@acme.example"},
				"groups": []string{},
			},
		},
		{
			name:  "assignees_form accepts a JSON object verbatim",
			field: "ASSIGNEE PICKER KO", raw: `{"groups":["grp-1"]}`,
			want: map[string]json.RawMessage{"groups": json.RawMessage(`["grp-1"]`)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := encodeKickoffValue(byLabel[tc.field], tc.raw, members)
			if err != nil {
				t.Fatalf("encodeKickoffValue error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("encoded = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestEncodeKickoffValueLengthCap pins the text/textarea length caps
// (kickoffTextMaxBytes=200, kickoffTextareaMaxBytes=30000), measured in
// BYTES to match api-v2's FormValuesValidator, which enforces both with raw
// PHP strlen() rather than a multi-byte-aware rule (see the comment on
// kickoffLengthCap in kickoff.go for how that was established).
func TestEncodeKickoffValueLengthCap(t *testing.T) {
	byLabel := map[string]tallyfy.KickoffField{}
	for _, f := range kickoffFixture() {
		byLabel[f.Label] = f
	}

	tests := []struct {
		name     string
		field    string
		raw      string
		wantErr  bool
		wantText []string // substrings the error must contain: field, cap, actual length
	}{
		{
			name:  "text exactly at the 200-byte cap is accepted",
			field: "STF KO", raw: strings.Repeat("a", 200),
		},
		{
			name:  "text one byte over the cap is rejected",
			field: "STF KO", raw: strings.Repeat("a", 201), wantErr: true,
			wantText: []string{`"STF KO"`, "200-byte limit", "by 1", "201 bytes total"},
		},
		{
			name:  "textarea exactly at the 30000-byte cap is accepted",
			field: "LTF KO", raw: strings.Repeat("a", 30000),
		},
		{
			name:  "textarea one byte over the cap is rejected",
			field: "LTF KO", raw: strings.Repeat("a", 30001), wantErr: true,
			wantText: []string{`"LTF KO"`, "30000-byte limit", "by 1", "30001 bytes total"},
		},
		{
			// "e" with an acute accent is 2 bytes in UTF-8 (U+00E9), so 100 of
			// them is 100 runes but exactly 200 bytes: at the byte cap, and
			// far under a 200-rune cap. Proves the boundary is measured in
			// bytes, not runes - if this test used a 200-RUNE reading of the
			// same cap it would also pass, so it alone would not catch a
			// regression to utf8.RuneCountInString. The next case does.
			name:  "a multi-byte value exactly at the 200-byte cap (100 two-byte runes) is accepted",
			field: "STF KO", raw: strings.Repeat("é", 100),
		},
		{
			// 200 runes of the same 2-byte character is 400 bytes: exactly at
			// a 200-RUNE cap, but 200 bytes over the real 200-BYTE one. If the
			// implementation ever regressed to utf8.RuneCountInString, this
			// value would be wrongly ACCEPTED - which is the direction the
			// issue explicitly bans ("never reject a value the server would
			// accept" implies the converse must hold too: never accept
			// locally what the server would 422).
			name:  "a multi-byte value at an at-cap RUNE count but over-cap BYTE count is rejected",
			field: "STF KO", raw: strings.Repeat("é", 200), wantErr: true,
			wantText: []string{`"STF KO"`, "200-byte limit", "by 200", "400 bytes total"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := encodeKickoffValue(byLabel[tc.field], tc.raw, nil)
			if tc.wantErr {
				msg := wantUsageError(t, err).Error()
				for _, want := range tc.wantText {
					if !strings.Contains(msg, want) {
						t.Errorf("error message %q is missing %q", msg, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("encodeKickoffValue(%d bytes) error: %v", len(tc.raw), err)
			}
			if got != any(tc.raw) {
				t.Errorf("encoded = %#v, want the scalar unchanged", got)
			}
		})
	}
}

// TestCheckKickoffLengthsBulk pins the bulk-mode guarantee from AC3: a length
// violation anywhere in the CSV is caught before processLaunchBulk's row loop
// would launch a single process, exactly like resolveKickoffKeys already does
// for an unknown header.
func TestCheckKickoffLengthsBulk(t *testing.T) {
	header := []string{"name", "STF KO"}
	resolved, err := resolveKickoffKeys(kickoffFixture(), "bp-123", csvFieldHeaders(header, 0))
	if err != nil {
		t.Fatalf("resolveKickoffKeys error: %v", err)
	}

	t.Run("every row within the cap passes", func(t *testing.T) {
		dataRows := [][]string{
			{"Row A", "fine"},
			{"Row B", strings.Repeat("a", 200)},
			{"Row C", ""},
		}
		if err := checkKickoffLengthsBulk(header, dataRows, 0, resolved); err != nil {
			t.Errorf("checkKickoffLengthsBulk error: %v", err)
		}
	})

	t.Run("a violation in a LATER row is still caught before any row would launch", func(t *testing.T) {
		// Row indices 0,1,2 map to CSV row numbers 2,3,4 (header is row 1) -
		// processLaunchBulk's own 1-based, header-inclusive numbering.
		dataRows := [][]string{
			{"Row A", "fine"},
			{"Row B", "also fine"},
			{"Row C", strings.Repeat("a", 201)},
		}
		err := checkKickoffLengthsBulk(header, dataRows, 0, resolved)
		msg := wantUsageError(t, err).Error()
		if !strings.Contains(msg, "row 4") {
			t.Errorf("error message = %q, want it to name row 4 (1-based, header counted)", msg)
		}
	})

	t.Run("an over-cap value in the FIRST row is caught too", func(t *testing.T) {
		dataRows := [][]string{
			{"Row A", strings.Repeat("a", 201)},
			{"Row B", "fine"},
		}
		err := checkKickoffLengthsBulk(header, dataRows, 0, resolved)
		msg := wantUsageError(t, err).Error()
		if !strings.Contains(msg, "row 2") {
			t.Errorf("error message = %q, want it to name row 2", msg)
		}
	})
}

func TestEncodeKickoffValueErrors(t *testing.T) {
	byLabel := map[string]tallyfy.KickoffField{}
	for _, f := range kickoffFixture() {
		byLabel[f.Label] = f
	}
	tests := []struct {
		name     string
		field    string
		raw      string
		wantText string
	}{
		{
			name: "dropdown value that is not an option", field: "DD KO", raw: "MAYBE",
			wantText: `choose one of "YES", "NO"`,
		},
		{
			name: "radio value that is not an option", field: "RADIO KO", raw: "RAD 9",
			wantText: `choose one of "RAD 1", "RAD 2"`,
		},
		{
			name: "table value that is not JSON", field: "TABLE KO", raw: "a,b",
			wantText: "needs a JSON array value",
		},
		{
			name: "table with the wrong number of entries", field: "TABLE KO", raw: `[{"T1":"a"}]`,
			wantText: "needs exactly 2 entries, one per column, but got 1",
		},
		{
			name: "assignees_form with an unknown key", field: "ASSIGNEE PICKER KO", raw: `{"people":[]}`,
			wantText: `unknown key "people"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := encodeKickoffValue(byLabel[tc.field], tc.raw, nil)
			if msg := wantUsageError(t, err).Error(); !strings.Contains(msg, tc.wantText) {
				t.Errorf("error message = %q, want it to contain %q", msg, tc.wantText)
			}
		})
	}
}

// TestEncodeKickoffFile pins the wire shape of a file value. It asserts on
// marshalled JSON rather than the Go value because the wire shape is the whole
// contract: api-v2 foreachs this value, so what matters is that a list of
// objects leaves the CLI, whatever Go type produced it.
func TestEncodeKickoffFile(t *testing.T) {
	f := kickoffField(t, "FILE KO")
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "a bare path becomes a one-entry list",
			raw:  "uploads/report.pdf",
			want: `[{"filename":"report.pdf","source":"url","url":"uploads/report.pdf"}]`,
		},
		{
			name: "an absolute URL keeps its last segment as the filename",
			raw:  "https://cdn.example.com/docs/q3.pdf",
			want: `[{"filename":"q3.pdf","source":"url","url":"https://cdn.example.com/docs/q3.pdf"}]`,
		},
		{
			// A signed URL is the common real case. The query string is not
			// part of the name a user should see, but it IS part of the URL
			// that has to be fetched, so only the filename is trimmed.
			name: "a signed URL keeps the query but not in the filename",
			raw:  "https://cdn.example.com/docs/q3.pdf?X-Amz-Signature=abc123",
			want: `[{"filename":"q3.pdf","source":"url","url":"https://cdn.example.com/docs/q3.pdf?X-Amz-Signature=abc123"}]`,
		},
		{
			name: "several comma-separated URLs become several entries",
			raw:  "uploads/a.pdf, uploads/b.png",
			want: `[{"filename":"a.pdf","source":"url","url":"uploads/a.pdf"},` +
				`{"filename":"b.png","source":"url","url":"uploads/b.png"}]`,
		},
		{
			name: "a JSON array of URLs becomes one entry each",
			raw:  `["uploads/a.pdf","uploads/b.png"]`,
			want: `[{"filename":"a.pdf","source":"url","url":"uploads/a.pdf"},` +
				`{"filename":"b.png","source":"url","url":"uploads/b.png"}]`,
		},
		{
			// So a value read out of an export can be launched straight back
			// in without being rewritten. Every key survives; only their
			// order changes, which JSON does not ascribe meaning to.
			name: "a JSON array of file objects keeps every key",
			raw:  `[{"url":"uploads/a.pdf","filename":"a.pdf","source":"url"}]`,
			want: `[{"filename":"a.pdf","source":"url","url":"uploads/a.pdf"}]`,
		},
		{
			name: "a single JSON object is still wrapped in a list",
			raw:  `{"url":"uploads/a.pdf","filename":"a.pdf"}`,
			want: `[{"filename":"a.pdf","url":"uploads/a.pdf"}]`,
		},
		{
			// Task::updateCaptureValues passes an already-enriched entry
			// through untouched when it carries full_url, so url is not
			// required in that case.
			name: "an already-enriched object with full_url is accepted",
			raw:  `[{"full_url":"https://api.example.com/uploads/a.pdf","filename":"a.pdf"}]`,
			want: `[{"filename":"a.pdf","full_url":"https://api.example.com/uploads/a.pdf"}]`,
		},
		{
			// The file arm of FormValuesValidator breaks on empty($values), so
			// an empty cell clears the field and a required field still fails
			// its own required check server-side. (table is the one type where
			// this is NOT true - see TestEncodeKickoffBlankTableIsOmitted.)
			name: "an empty value stays an empty scalar",
			raw:  "",
			want: `""`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := encodeKickoffValue(f, tc.raw, nil)
			if err != nil {
				t.Fatalf("encodeKickoffValue error: %v", err)
			}
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			if string(b) != tc.want {
				t.Errorf("encoded JSON = %s, want %s", b, tc.want)
			}
		})
	}
}

// TestEncodeKickoffFileNeverBareScalar is the regression guard for the defect
// this encoding exists to fix. A file value sent as a bare string is not a
// clean 422 - FormValuesValidator has no file arm, so nothing rejects it - it
// reaches Task::updateCaptureValues, which does `foreach ($payload as $item)`
// and dies with "foreach() argument must be of type array|object, string
// given". RunsRepository::captureRunValue foreachs the same value again on the
// prerun path. So whatever a user types, a non-empty file value must leave as
// a JSON array whose every entry is an object.
func TestEncodeKickoffFileNeverBareScalar(t *testing.T) {
	f := kickoffField(t, "FILE KO")
	raws := []string{
		"uploads/report.pdf",
		"https://cdn.example.com/docs/q3.pdf",
		"https://cdn.example.com/docs/q3.pdf?X-Amz-Signature=abc123",
		"https://cdn.example.com/docs/q3.pdf#page=2",
		"uploads/a.pdf, uploads/b.png",
		`["uploads/a.pdf","uploads/b.png"]`,
		`[{"url":"uploads/a.pdf","filename":"a.pdf"}]`,
		`{"url":"uploads/a.pdf","filename":"a.pdf"}`,
		"  uploads/spaced.pdf  ",
		"no-extension",
		"/leading/slash.pdf",
		"trailing/slash/",
		"https://cdn.example.com",
	}
	for _, raw := range raws {
		t.Run(raw, func(t *testing.T) {
			got, err := encodeKickoffValue(f, raw, nil)
			if err != nil {
				t.Fatalf("encodeKickoffValue(%q) error: %v", raw, err)
			}
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			var entries []json.RawMessage
			if err := json.Unmarshal(b, &entries); err != nil {
				t.Fatalf("file value %q encoded as %s, which is not a JSON array - api-v2 foreachs this and 500s", raw, b)
			}
			if len(entries) == 0 {
				t.Fatalf("file value %q encoded as an empty array", raw)
			}
			for i, e := range entries {
				if s := strings.TrimSpace(string(e)); !strings.HasPrefix(s, "{") {
					t.Errorf("entry %d of %q is %s, want a file object - Arr::has() needs an array, not a scalar", i, raw, e)
				}
			}
		})
	}
}

func TestEncodeKickoffFileErrors(t *testing.T) {
	f := kickoffField(t, "FILE KO")
	tests := []struct {
		name     string
		raw      string
		wantText string
	}{
		{
			name: "a file object with no url at all", raw: `[{"filename":"a.pdf"}]`,
			wantText: `needs a "url" on every file object`,
		},
		{
			name: "a single object with no url", raw: `{"filename":"a.pdf"}`,
			wantText: `needs a "url" on every file object`,
		},
		{
			name: "an entry that is neither a URL nor an object", raw: `[42]`,
			wantText: "needs each entry to be a URL string or a file object",
		},
		{
			name: "malformed JSON", raw: `[{"url":`,
			wantText: "is not valid JSON",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := encodeKickoffValue(f, tc.raw, nil)
			if msg := wantUsageError(t, err).Error(); !strings.Contains(msg, tc.wantText) {
				t.Errorf("error message = %q, want it to contain %q", msg, tc.wantText)
			}
		})
	}
}

// TestEncodeKickoffScalarTypes covers the types that deliberately pass through
// untouched, so that a future type added to the switch cannot quietly start
// wrapping them the way file needed to be wrapped.
func TestEncodeKickoffScalarTypes(t *testing.T) {
	cases := map[string]string{
		"STF KO":   "hello",
		"LTF KO":   "a longer answer\nwith a newline",
		"EMAIL KO": "jo@example.com",
		"DATE KO":  "2026-07-01",
	}
	for label, raw := range cases {
		t.Run(label, func(t *testing.T) {
			got, err := encodeKickoffValue(kickoffField(t, label), raw, nil)
			if err != nil {
				t.Fatalf("encodeKickoffValue error: %v", err)
			}
			if got != any(raw) {
				t.Errorf("encoded = %#v, want the scalar %q unchanged", got, raw)
			}
		})
	}
}

// TestEncodeKickoffOptionIDStaysNumeric pins the wire type of an option id.
// api-v2 assigns integer option ids and stores whatever it is sent verbatim,
// so emitting "1" instead of 1 would store a type that does not match the
// blueprint's own options. (FormValuesValidator itself accepts either, because
// PHP casts numeric string array keys, so nothing server-side would complain.)
func TestEncodeKickoffOptionIDStaysNumeric(t *testing.T) {
	for label, raw := range map[string]string{"DD KO": "YES", "CHECKLIST KO": "CHK 1"} {
		t.Run(label, func(t *testing.T) {
			got, err := encodeKickoffValue(kickoffField(t, label), raw, nil)
			if err != nil {
				t.Fatalf("encodeKickoffValue error: %v", err)
			}
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			if strings.Contains(string(b), `"id":"`) {
				t.Errorf("encoded JSON = %s, want id as a number, not a quoted string", b)
			}
			if !strings.Contains(string(b), `"id":1`) {
				t.Errorf("encoded JSON = %s, want it to carry the numeric option id", b)
			}
		})
	}
}

// matchOptionFixture is a dropdown built to exercise every arm of
// matchKickoffOption at once, so one field serves the whole table.
//
// The two collisions are deliberately built on UNPADDED options, so that a
// case failing tells you which arm broke rather than implicating two at once:
//
//   - " Gold " is the only padded option, exactly as a blueprint edited in the
//     web UI can store it. Nothing about it is reachable unless BOTH sides of
//     the comparison are trimmed, and no other case depends on trimming.
//   - "Copper" carries the string id "silver", which collides with the text of
//     "Silver". Selecting "silver" must return "Silver", proving the id pass
//     runs after the case-insensitive text pass. It also exercises a quoted id.
//   - "Bronze" carries the numeric id 2, which collides with the text of the
//     last option, "2". Selecting "2" must return the option whose TEXT is "2",
//     proving the id pass also runs after the exact text pass.
func matchOptionFixture() tallyfy.KickoffField {
	return tallyfy.KickoffField{
		ID: "0f2a", Alias: "metal", Label: "METAL KO", FieldType: "dropdown",
		Options: []tallyfy.KickoffOption{
			{ID: json.RawMessage("1"), Text: " Gold "},
			{ID: json.RawMessage(`"silver"`), Text: "Copper"},
			{ID: json.RawMessage("7"), Text: "Silver"},
			{ID: json.RawMessage("2"), Text: "Bronze"},
			{ID: json.RawMessage("9"), Text: "2"},
		},
	}
}

// TestMatchKickoffOption is the whole contract of matchKickoffOption, asserted
// on the RETURNED OPTION rather than on any error string, because the returned
// option is what kickoffOptionValue puts on the wire.
//
// Each case names the arm it is testing, so reverting one arm reddens one case
// and the attribution is unambiguous.
func TestMatchKickoffOption(t *testing.T) {
	f := matchOptionFixture()
	tests := []struct {
		name     string
		raw      string
		wantText string // the option's OWN text, never the caller's input
		wantID   string // the option's id, as raw JSON
	}{
		{
			// Arm: both-sides trim. Red if only the input is trimmed.
			name: "an option whose stored text has surrounding whitespace is selectable",
			raw:  "Gold", wantText: " Gold ", wantID: "1",
		},
		{
			// Arm: both-sides trim, reached case-insensitively.
			name: "a padded option matches case-insensitively too",
			raw:  "  gOlD  ", wantText: " Gold ", wantID: "1",
		},
		{
			// Arm: id pass, numeric id. Red only if there is no id arm.
			name: "an option id selects that option",
			raw:  "7", wantText: "Silver", wantID: "7",
		},
		{
			// Ordering: exact text must beat an id match on a DIFFERENT option.
			// "2" is the text of the id-9 option and the id of "Bronze".
			name: "an exact text match beats an id collision",
			raw:  "2", wantText: "2", wantID: "9",
		},
		{
			// Ordering: the id pass runs after BOTH text passes, so even a
			// case-insensitive text match outranks an id match elsewhere.
			// "silver" is the id of "Copper" and folds to the text of "Silver".
			// Neither option is padded, so this stays green without the trim.
			name: "a case-insensitive text match beats an id collision",
			raw:  "silver", wantText: "Silver", wantID: "7",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matchKickoffOption(f, tc.raw)
			if err != nil {
				t.Fatalf("matchKickoffOption(%q) error: %v", tc.raw, err)
			}
			if got.Text != tc.wantText {
				t.Errorf("matchKickoffOption(%q).Text = %q, want %q", tc.raw, got.Text, tc.wantText)
			}
			if string(got.ID) != tc.wantID {
				t.Errorf("matchKickoffOption(%q).ID = %s, want %s", tc.raw, got.ID, tc.wantID)
			}
		})
	}
}

// TestMatchKickoffOptionErrors pins that a value matching neither a text nor an
// id still fails loud and names every valid choice. Unchanged by the trim and
// id arms, and deliberately so: adding ways to match must not quietly widen
// what is accepted past the option list.
func TestMatchKickoffOptionErrors(t *testing.T) {
	tests := []struct {
		name     string
		field    tallyfy.KickoffField
		raw      string
		wantText string
	}{
		{
			name:  "a value matching no text and no id lists the choices",
			field: matchOptionFixture(), raw: "Platinum",
			wantText: `choose one of " Gold ", "Copper", "Silver", "Bronze", "2"`,
		},
		{
			name:  "an id that belongs to no option is still rejected",
			field: matchOptionFixture(), raw: "999",
			wantText: `invalid value "999" for kick-off field "METAL KO"`,
		},
		{
			name: "a blank value cannot select an option that has no id",
			field: tallyfy.KickoffField{
				ID: "0f2b", Label: "NO IDS", FieldType: "dropdown",
				Options: []tallyfy.KickoffOption{{Text: "Alpha"}},
			},
			raw:      "",
			wantText: `invalid value "" for kick-off field "NO IDS"`,
		},
		{
			name: "a field with no options at all says so",
			field: tallyfy.KickoffField{
				ID: "0f2c", Label: "EMPTY KO", FieldType: "dropdown",
			},
			raw:      "anything",
			wantText: `defines no options, so "anything" cannot be matched`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matchKickoffOption(tc.field, tc.raw)
			if msg := wantUsageError(t, err).Error(); !strings.Contains(msg, tc.wantText) {
				t.Errorf("error message = %q, want it to contain %q", msg, tc.wantText)
			}
			if !reflect.DeepEqual(got, tallyfy.KickoffOption{}) {
				t.Errorf("returned option = %#v, want the zero option on error", got)
			}
		})
	}
}

// TestKickoffOptionID pins how a raw-JSON option id is rendered for comparison.
// The empty results are the load-bearing ones: matchKickoffOption skips an
// empty id, so anything that renders empty here can never be selected by a
// blank value.
func TestKickoffOptionID(t *testing.T) {
	tests := []struct {
		name string
		id   json.RawMessage
		want string
	}{
		{name: "a numeric id is its digits", id: json.RawMessage("2"), want: "2"},
		{name: "a string id loses its quotes", id: json.RawMessage(`"2"`), want: "2"},
		{name: "a padded string id is trimmed", id: json.RawMessage(`" a7 "`), want: "a7"},
		{name: "an absent id renders empty", id: nil, want: ""},
		{name: "an empty id renders empty", id: json.RawMessage(""), want: ""},
		{name: "a null id renders empty", id: json.RawMessage("null"), want: ""},
		{name: "an unreadable string id renders empty", id: json.RawMessage(`"oops`), want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := kickoffOptionID(tallyfy.KickoffOption{ID: tc.id}); got != tc.want {
				t.Errorf("kickoffOptionID(%s) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestEncodePrerunKeysByTimelineID(t *testing.T) {
	fields := kickoffFixture()
	resolved, err := resolveKickoffKeys(fields, "bp-123", []string{"STF KO", "dd-ko-8308340"})
	if err != nil {
		t.Fatalf("resolveKickoffKeys error: %v", err)
	}
	got, err := encodePrerun(resolved, map[string]string{"STF KO": "hello", "dd-ko-8308340": "NO"}, nil)
	if err != nil {
		t.Fatalf("encodePrerun error: %v", err)
	}
	want := map[string]any{
		"bbd6a81fbb1da0b90610bf9560da339d": "hello",
		"84fd4089e8b739537ce22e63ee78f8fe": map[string]any{"id": json.RawMessage("2"), "text": "NO"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("prerun = %#v, want %#v", got, want)
	}
}

// A blank cell against a table field must be OMITTED from prerun, not sent as
// "". FormValuesValidator's table arm is the only one without an
// empty($values) early-break, so `! is_array("")` is true and api-v2 answers
// 422 "Invalid table field". An empty list is no better: it fails the
// count($values) !== count($capture->columns) check instead.
func TestEncodeKickoffBlankTableIsOmitted(t *testing.T) {
	var table tallyfy.KickoffField
	for _, f := range kickoffFixture() {
		if f.FieldType == "table" {
			table = f
		}
	}
	if table.ID == "" {
		t.Fatal("fixture has no table field")
	}
	if len(table.Columns) == 0 {
		t.Fatal("the fixture's table field needs columns, or api-v2 skips the check this pins")
	}

	for _, raw := range []string{"", "   ", "\t"} {
		got, err := encodeKickoffValue(table, raw, nil)
		if err != nil {
			t.Fatalf("encodeKickoffValue(%q) error: %v", raw, err)
		}
		if _, ok := got.(kickoffOmitted); !ok {
			t.Errorf("encodeKickoffValue(%q) = %#v, want kickoffOmitted so the key is dropped", raw, got)
		}
	}
}

// The omission has to survive to the payload, and it must not take other
// fields on the same row with it.
func TestEncodePrerunDropsBlankTableKeepsOthers(t *testing.T) {
	fields := kickoffFixture()
	resolved, err := resolveKickoffKeys(fields, "bp-123", []string{"TABLE KO", "STF KO"})
	if err != nil {
		t.Fatalf("resolveKickoffKeys error: %v", err)
	}
	got, err := encodePrerun(resolved, map[string]string{"TABLE KO": "", "STF KO": "hello"}, nil)
	if err != nil {
		t.Fatalf("encodePrerun error: %v", err)
	}
	want := map[string]any{"bbd6a81fbb1da0b90610bf9560da339d": "hello"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("prerun = %#v, want %#v (blank table key dropped, text key kept)", got, want)
	}
	if _, present := got["2d16159369e95b1740e24b8d9aaff44b"]; present {
		t.Error("blank table field is still in the payload; api-v2 answers 422 Invalid table field")
	}
}

// A row whose only supplied field is a blank table cell must send no prerun at
// all, rather than an empty object.
func TestEncodePrerunAllOmittedIsNil(t *testing.T) {
	fields := kickoffFixture()
	resolved, err := resolveKickoffKeys(fields, "bp-123", []string{"TABLE KO"})
	if err != nil {
		t.Fatalf("resolveKickoffKeys error: %v", err)
	}
	got, err := encodePrerun(resolved, map[string]string{"TABLE KO": ""}, nil)
	if err != nil {
		t.Fatalf("encodePrerun error: %v", err)
	}
	if got != nil {
		t.Errorf("prerun = %#v, want nil so the body omits it entirely", got)
	}
}

// A non-blank table value must still be encoded and validated as before - the
// blank short-circuit must not have widened into a general table bypass.
func TestEncodePrerunKeepsNonBlankTable(t *testing.T) {
	fields := kickoffFixture()
	resolved, err := resolveKickoffKeys(fields, "bp-123", []string{"TABLE KO"})
	if err != nil {
		t.Fatalf("resolveKickoffKeys error: %v", err)
	}
	got, err := encodePrerun(resolved, map[string]string{"TABLE KO": `[{"T1":"a"},{"T2":"b"}]`}, nil)
	if err != nil {
		t.Fatalf("encodePrerun error: %v", err)
	}
	want := map[string]any{
		"2d16159369e95b1740e24b8d9aaff44b": []any{
			map[string]any{"T1": "a"},
			map[string]any{"T2": "b"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("prerun = %#v, want %#v", got, want)
	}
	if _, err := encodePrerun(resolved, map[string]string{"TABLE KO": `[{"T1":"a"}]`}, nil); err == nil {
		t.Error("a table value with the wrong column count must still be rejected client-side")
	}
}

func TestEncodePrerunEmpty(t *testing.T) {
	got, err := encodePrerun(nil, nil, nil)
	if err != nil {
		t.Fatalf("encodePrerun error: %v", err)
	}
	if got != nil {
		t.Errorf("prerun = %#v, want nil so the body omits it entirely", got)
	}
}

func TestKickoffNeedsMembers(t *testing.T) {
	fields := kickoffFixture()
	plain, err := resolveKickoffKeys(fields, "bp-123", []string{"STF KO"})
	if err != nil {
		t.Fatalf("resolveKickoffKeys error: %v", err)
	}
	if kickoffNeedsMembers(plain) {
		t.Error("a text-only launch must not trigger a member lookup")
	}
	withAssignees, err := resolveKickoffKeys(fields, "bp-123", []string{"ASSIGNEE PICKER KO"})
	if err != nil {
		t.Fatalf("resolveKickoffKeys error: %v", err)
	}
	if !kickoffNeedsMembers(withAssignees) {
		t.Error("an assignees_form field must trigger a member lookup")
	}
}

func TestCSVFieldHeaders(t *testing.T) {
	// Must agree with csvRowFields on which columns are field keys.
	header := []string{"name", " DD KO ", "", "STF KO"}
	got := csvFieldHeaders(header, 0)
	want := []string{"DD KO", "STF KO"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("csvFieldHeaders = %#v, want %#v", got, want)
	}
	_, rowFields := csvRowFields(header, []string{"Jo", "YES", "x", "hi"}, 0)
	for _, k := range want {
		if _, ok := rowFields[k]; !ok {
			t.Errorf("csvRowFields did not produce key %q that csvFieldHeaders resolved", k)
		}
	}
	if len(rowFields) != len(want) {
		t.Errorf("csvRowFields produced %d keys, csvFieldHeaders produced %d", len(rowFields), len(want))
	}
}

func TestCSVRoundTripToLaunchBody(t *testing.T) {
	// Header resolution happens once, then each row is encoded against it -
	// exactly what processLaunchBulk does.
	header := []string{"name", "DD KO", "STF KO"}
	rows := [][]string{
		{"Onboard ACME Corp", "YES", "first"},
		{"Onboard Beta LLC", "NO", ""},
	}
	resolved, err := resolveKickoffKeys(kickoffFixture(), "bp-123", csvFieldHeaders(header, 0))
	if err != nil {
		t.Fatalf("resolveKickoffKeys error: %v", err)
	}

	want := []map[string]any{
		{
			"checklist_id": "bp-123",
			"name":         "Onboard ACME Corp",
			"prerun": map[string]any{
				"84fd4089e8b739537ce22e63ee78f8fe": map[string]any{"id": float64(1), "text": "YES"},
				"bbd6a81fbb1da0b90610bf9560da339d": "first",
			},
		},
		{
			"checklist_id": "bp-123",
			"name":         "Onboard Beta LLC",
			"prerun": map[string]any{
				"84fd4089e8b739537ce22e63ee78f8fe": map[string]any{"id": float64(2), "text": "NO"},
				"bbd6a81fbb1da0b90610bf9560da339d": "",
			},
		},
	}
	for i, rec := range rows {
		name, values := csvRowFields(header, rec, 0)
		raw, err := kickoffLaunchPayload("bp-123", name, values, resolved, nil)
		if err != nil {
			t.Fatalf("row %d: kickoffLaunchPayload error: %v", i, err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("row %d: body is not valid JSON: %v", i, err)
		}
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("row %d body = %#v, want %#v", i, got, want[i])
		}
	}
}

func TestCSVRoundTripRejectsUnknownHeader(t *testing.T) {
	// A bad header aborts before any row launches, rather than launching every
	// row with that column silently dropped.
	header := []string{"name", "customer_email"}
	_, err := resolveKickoffKeys(kickoffFixture(), "bp-123", csvFieldHeaders(header, 0))
	if msg := wantUsageError(t, err).Error(); !strings.Contains(msg, `unknown kick-off field "customer_email"`) {
		t.Errorf("error message = %q, want it to name the bad CSV header", msg)
	}
}
