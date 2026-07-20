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
	}
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
			// api-v2 accepts an empty value for every type, and a required
			// field still fails its own required check.
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
