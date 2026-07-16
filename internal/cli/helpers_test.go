package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/tallyfy/cli/pkg/tallyfy"
)

// strPtr returns a pointer to s, for building *string fields in fixtures.
func strPtr(s string) *string { return &s }

// wantUsageError fails the test unless err is (or wraps) a *UsageError, and
// returns it so callers can assert on the message.
func wantUsageError(t *testing.T, err error) *UsageError {
	t.Helper()
	if err == nil {
		t.Fatal("expected a *UsageError, got nil")
	}
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("expected a *UsageError, got %T: %v", err, err)
	}
	return ue
}

// --- helpers.go ---------------------------------------------------------------

func TestDeref(t *testing.T) {
	tests := []struct {
		name string
		in   *string
		want string
	}{
		{name: "nil pointer", in: nil, want: ""},
		{name: "non-nil pointer", in: strPtr("2026-07-01T09:00:00Z"), want: "2026-07-01T09:00:00Z"},
		{name: "pointer to empty string", in: strPtr(""), want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := deref(tc.in); got != tc.want {
				t.Errorf("deref(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{name: "shorter than limit unchanged", s: "Approve invoice", n: 50, want: "Approve invoice"},
		{name: "exactly at limit unchanged", s: "abcde", n: 5, want: "abcde"},
		{name: "ascii truncated with ellipsis", s: "abcdefgh", n: 5, want: "abcd…"},
		// 7 runes truncated to 4: three runes kept + ellipsis. Byte-slicing
		// would have cut through a 3-byte rune and produced garbage.
		{name: "multibyte runes not byte-sliced", s: "日本語のテスト", n: 4, want: "日本語…"},
		{name: "two-byte rune kept whole at the cut", s: "héllo wörld", n: 3, want: "hé…"},
		{name: "n=1 keeps one rune with no ellipsis", s: "abc", n: 1, want: "a"},
		{name: "n=0 gives empty string", s: "abc", n: 0, want: ""},
		{name: "empty string unchanged", s: "", n: 10, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncate(tc.s, tc.n); got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
			}
		})
	}
}

func TestReadInput(t *testing.T) {
	t.Run("reads a real file path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "payload.json")
		want := `{"name":"Q3 onboarding"}`
		if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := readInput(path)
		if err != nil {
			t.Fatalf("readInput(%q) error: %v", path, err)
		}
		if string(got) != want {
			t.Errorf("readInput(%q) = %q, want %q", path, got, want)
		}
	})

	t.Run("missing file returns error", func(t *testing.T) {
		if _, err := readInput(filepath.Join(t.TempDir(), "does-not-exist.json")); err == nil {
			t.Fatal("expected error for missing file, got nil")
		}
	})

	stdinCases := []struct {
		name string
		arg  string
	}{
		{name: "dash reads stdin", arg: "-"},
		{name: "empty arg reads stdin", arg: ""},
	}
	for _, tc := range stdinCases {
		t.Run(tc.name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			oldStdin := os.Stdin
			os.Stdin = r
			t.Cleanup(func() {
				os.Stdin = oldStdin
				_ = r.Close()
			})
			want := "piped payload"
			if _, err := w.WriteString(want); err != nil {
				t.Fatal(err)
			}
			_ = w.Close()
			got, err := readInput(tc.arg)
			if err != nil {
				t.Fatalf("readInput(%q) error: %v", tc.arg, err)
			}
			if string(got) != want {
				t.Errorf("readInput(%q) = %q, want %q", tc.arg, got, want)
			}
		})
	}
}

// --- process.go ----------------------------------------------------------------

func TestParseKeyValues(t *testing.T) {
	tests := []struct {
		name    string
		pairs   []string
		want    map[string]string
		wantErr bool
	}{
		{name: "nil input gives empty map", pairs: nil, want: map[string]string{}},
		{name: "single pair", pairs: []string{"manager=jo@example.com"}, want: map[string]string{"manager": "jo@example.com"}},
		{name: "multiple pairs", pairs: []string{"a=1", "b=2"}, want: map[string]string{"a": "1", "b": "2"}},
		{name: "empty value kept", pairs: []string{"note="}, want: map[string]string{"note": ""}},
		{name: "value containing equals split on first only", pairs: []string{"expr=x=y"}, want: map[string]string{"expr": "x=y"}},
		{name: "last duplicate key wins", pairs: []string{"a=1", "a=2"}, want: map[string]string{"a": "2"}},
		{name: "pair without equals is usage error", pairs: []string{"noequals"}, wantErr: true},
		{name: "empty key is usage error", pairs: []string{"=value"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseKeyValues(tc.pairs)
			if tc.wantErr {
				_ = wantUsageError(t, err)
				return
			}
			if err != nil {
				t.Fatalf("parseKeyValues(%v) error: %v", tc.pairs, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseKeyValues(%v) = %v, want %v", tc.pairs, got, tc.want)
			}
		})
	}
}

func TestCSVRowFields(t *testing.T) {
	tests := []struct {
		name       string
		header     []string
		rec        []string
		nameIdx    int
		wantName   string
		wantFields map[string]string
	}{
		{
			name:       "no name column",
			header:     []string{"start_date", "manager"},
			rec:        []string{"2026-07-01", "jo@example.com"},
			nameIdx:    -1,
			wantName:   "",
			wantFields: map[string]string{"start_date": "2026-07-01", "manager": "jo@example.com"},
		},
		{
			name:       "name column extracted and excluded from fields",
			header:     []string{"name", "dept"},
			rec:        []string{"Jo Lee", "Sales"},
			nameIdx:    0,
			wantName:   "Jo Lee",
			wantFields: map[string]string{"dept": "Sales"},
		},
		{
			name:       "short record pads missing cells with empty strings",
			header:     []string{"name", "dept", "region"},
			rec:        []string{"Jo Lee", "Sales"},
			nameIdx:    0,
			wantName:   "Jo Lee",
			wantFields: map[string]string{"dept": "Sales", "region": ""},
		},
		{
			name:       "header keys trimmed and blank header cells dropped",
			header:     []string{" dept ", "", "name"},
			rec:        []string{"Sales", "ignored", "Jo"},
			nameIdx:    2,
			wantName:   "Jo",
			wantFields: map[string]string{"dept": "Sales"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotFields := csvRowFields(tc.header, tc.rec, tc.nameIdx)
			if gotName != tc.wantName {
				t.Errorf("name = %q, want %q", gotName, tc.wantName)
			}
			if !reflect.DeepEqual(gotFields, tc.wantFields) {
				t.Errorf("fields = %v, want %v", gotFields, tc.wantFields)
			}
		})
	}
}

func TestProcessLaunchPayload(t *testing.T) {
	tests := []struct {
		name        string
		blueprintID string
		procName    string
		fields      map[string]string
		want        map[string]any
	}{
		{
			name:        "checklist_id only",
			blueprintID: "bp-123",
			want:        map[string]any{"checklist_id": "bp-123"},
		},
		{
			name:        "empty fields map omits prerun",
			blueprintID: "bp-123",
			fields:      map[string]string{},
			want:        map[string]any{"checklist_id": "bp-123"},
		},
		{
			name:        "name included when non-empty",
			blueprintID: "bp-123",
			procName:    "Q3 onboarding",
			want:        map[string]any{"checklist_id": "bp-123", "name": "Q3 onboarding"},
		},
		{
			name:        "fields nested under prerun",
			blueprintID: "bp-123",
			procName:    "Q3 onboarding",
			fields:      map[string]string{"start_date": "2026-07-01", "manager": ""},
			want: map[string]any{
				"checklist_id": "bp-123",
				"name":         "Q3 onboarding",
				"prerun": map[string]any{
					"start_date": "2026-07-01",
					"manager":    "",
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := processLaunchPayload(tc.blueprintID, tc.procName, tc.fields)
			if err != nil {
				t.Fatalf("processLaunchPayload error: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("result is not valid JSON: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("payload = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// --- blueprint.go ---------------------------------------------------------------

func TestL6SplitCSV(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "trims and drops empty parts", in: "a, b ,,c", want: []string{"a", "b", "c"}},
		{name: "empty string gives empty slice", in: "", want: []string{}},
		{name: "only separators and spaces", in: " , ,", want: []string{}},
		{name: "single value", in: "tasks", want: []string{"tasks"}},
		{name: "internal spaces preserved after trim", in: " first task , second ", want: []string{"first task", "second"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := l6SplitCSV(tc.in); !slices.Equal(got, tc.want) {
				t.Errorf("l6SplitCSV(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

// --- task.go ---------------------------------------------------------------------

func TestFindTask(t *testing.T) {
	fixture := []tallyfy.Task{
		{ID: "task-aaa", Title: "Approve deploy", Status: "active"},
		{ID: "task-bbb", Title: "Sign contract", Status: "completed"},
	}
	tests := []struct {
		name      string
		tasks     []tallyfy.Task
		sel       string
		wantID    string
		wantFound bool
	}{
		{name: "match by exact ID", tasks: fixture, sel: "task-bbb", wantID: "task-bbb", wantFound: true},
		{name: "match by title case-insensitively", tasks: fixture, sel: "APPROVE DEPLOY", wantID: "task-aaa", wantFound: true},
		{name: "ID match is case-sensitive", tasks: fixture, sel: "TASK-AAA", wantFound: false},
		{name: "no match", tasks: fixture, sel: "does-not-exist", wantFound: false},
		{name: "empty task list", tasks: nil, sel: "task-aaa", wantFound: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, found := findTask(tc.tasks, tc.sel)
			if found != tc.wantFound {
				t.Fatalf("found = %v, want %v", found, tc.wantFound)
			}
			if tc.wantFound && got.ID != tc.wantID {
				t.Errorf("got task %q, want %q", got.ID, tc.wantID)
			}
			if !tc.wantFound && (got.ID != "" || got.Title != "") {
				t.Errorf("no-match should return the zero task, got %+v", got)
			}
		})
	}
}

func TestTaskIsComplete(t *testing.T) {
	tests := []struct {
		name string
		task tallyfy.Task
		want bool
	}{
		{name: "completed_at set", task: tallyfy.Task{Status: "active", CompletedAt: strPtr("2026-07-01T10:00:00Z")}, want: true},
		{name: "blank completed_at does not count", task: tallyfy.Task{Status: "active", CompletedAt: strPtr("   ")}, want: false},
		{name: "status completed", task: tallyfy.Task{Status: "completed"}, want: true},
		{name: "status complete mixed case", task: tallyfy.Task{Status: "Complete"}, want: true},
		{name: "status completed with surrounding spaces", task: tallyfy.Task{Status: "  COMPLETED  "}, want: true},
		{name: "active task with nil completed_at", task: tallyfy.Task{Status: "active"}, want: false},
		{name: "zero task", task: tallyfy.Task{}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := taskIsComplete(tc.task); got != tc.want {
				t.Errorf("taskIsComplete(%+v) = %v, want %v", tc.task, got, tc.want)
			}
		})
	}
}

// --- run.go ----------------------------------------------------------------------

func TestParseRunArgs(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		wantName        string
		wantParams      map[string]string
		wantPassthrough []string
		wantErr         bool
	}{
		{
			name:       "empty args is the list case",
			args:       nil,
			wantName:   "",
			wantParams: map[string]string{},
		},
		{
			name:       "name only",
			args:       []string{"onboard"},
			wantName:   "onboard",
			wantParams: map[string]string{},
		},
		{
			name:            "name with param and passthrough",
			args:            []string{"foo", "--param", "a=1", "extra"},
			wantName:        "foo",
			wantParams:      map[string]string{"a": "1"},
			wantPassthrough: []string{"extra"},
		},
		{
			name:       "param equals form",
			args:       []string{"foo", "--param=a=1"},
			wantName:   "foo",
			wantParams: map[string]string{"a": "1"},
		},
		{
			name:       "short -p form",
			args:       []string{"foo", "-p", "b=2"},
			wantName:   "foo",
			wantParams: map[string]string{"b": "2"},
		},
		{
			name:            "param value with equals, unknown flags passed through",
			args:            []string{"foo", "--param", "expr=x=y", "--verbose", "bar"},
			wantName:        "foo",
			wantParams:      map[string]string{"expr": "x=y"},
			wantPassthrough: []string{"--verbose", "bar"},
		},
		{name: "leading flag before name is usage error", args: []string{"--param", "a=1"}, wantErr: true},
		{name: "param missing its value argument", args: []string{"foo", "--param"}, wantErr: true},
		{name: "param value without equals", args: []string{"foo", "--param", "noequals"}, wantErr: true},
		{name: "param with empty key", args: []string{"foo", "--param", "=v"}, wantErr: true},
		{name: "param equals form without inner equals", args: []string{"foo", "--param=noequals"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, params, passthrough, err := parseRunArgs(tc.args)
			if tc.wantErr {
				_ = wantUsageError(t, err)
				return
			}
			if err != nil {
				t.Fatalf("parseRunArgs(%v) error: %v", tc.args, err)
			}
			if name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
			if !reflect.DeepEqual(params, tc.wantParams) {
				t.Errorf("params = %v, want %v", params, tc.wantParams)
			}
			if !slices.Equal(passthrough, tc.wantPassthrough) {
				t.Errorf("passthrough = %#v, want %#v", passthrough, tc.wantPassthrough)
			}
		})
	}
}

// --- user.go ---------------------------------------------------------------------

func TestUserInviteBody(t *testing.T) {
	tests := []struct {
		name    string
		email   string
		first   string
		last    string
		role    string
		want    map[string]string
		wantErr bool
	}{
		{name: "no email and no file is usage error", wantErr: true},
		{name: "whitespace email is usage error even with other fields", email: "   ", first: "Jo", role: "admin", wantErr: true},
		{name: "email only", email: "jo@example.com", want: map[string]string{"email": "jo@example.com"}},
		{
			name: "all fields present", email: "jo@example.com", first: "Jo", last: "Lee", role: "light",
			want: map[string]string{"email": "jo@example.com", "first_name": "Jo", "last_name": "Lee", "role": "light"},
		},
		{
			name: "empty optionals omitted", email: "jo@example.com", role: "admin",
			want: map[string]string{"email": "jo@example.com", "role": "admin"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := userInviteBody("", tc.email, tc.first, tc.last, tc.role)
			if tc.wantErr {
				_ = wantUsageError(t, err)
				return
			}
			if err != nil {
				t.Fatalf("userInviteBody error: %v", err)
			}
			var got map[string]string
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("result is not valid JSON: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("body = %v, want %v", got, tc.want)
			}
		})
	}
}

// --- guest.go --------------------------------------------------------------------

func TestGuestBody(t *testing.T) {
	const missingMsg = "guest create requires --email or --from-file"
	tests := []struct {
		name    string
		email   string
		first   string
		last    string
		want    map[string]string
		wantErr bool
	}{
		{name: "all empty is usage error with caller message", wantErr: true},
		{name: "email only", email: "guest@example.com", want: map[string]string{"email": "guest@example.com"}},
		{name: "first name alone is enough", first: "Jo", want: map[string]string{"first_name": "Jo"}},
		{
			name: "all three fields", email: "guest@example.com", first: "Jo", last: "Lee",
			want: map[string]string{"email": "guest@example.com", "first_name": "Jo", "last_name": "Lee"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := guestBody("", tc.email, tc.first, tc.last, missingMsg)
			if tc.wantErr {
				ue := wantUsageError(t, err)
				if ue.Msg != missingMsg {
					t.Errorf("error message = %q, want %q", ue.Msg, missingMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("guestBody error: %v", err)
			}
			var got map[string]string
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("result is not valid JSON: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("body = %v, want %v", got, tc.want)
			}
		})
	}
}

// --- group.go --------------------------------------------------------------------

func TestNameOrFileBody(t *testing.T) {
	const missingMsg = "group create requires --name or --from-file"

	t.Run("empty value and no file is usage error with caller message", func(t *testing.T) {
		_, err := nameOrFileBody("", "name", "", missingMsg)
		ue := wantUsageError(t, err)
		if ue.Msg != missingMsg {
			t.Errorf("error message = %q, want %q", ue.Msg, missingMsg)
		}
	})

	t.Run("value set builds a single-field object", func(t *testing.T) {
		raw, err := nameOrFileBody("", "name", "Ops team", missingMsg)
		if err != nil {
			t.Fatalf("nameOrFileBody error: %v", err)
		}
		var got map[string]string
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("result is not valid JSON: %v", err)
		}
		want := map[string]string{"name": "Ops team"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("body = %v, want %v", got, want)
		}
	})

	t.Run("from-file returns file contents verbatim", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "group.json")
		content := `{"name":"From File","description":"x"}`
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		raw, err := nameOrFileBody(path, "name", "ignored", missingMsg)
		if err != nil {
			t.Fatalf("nameOrFileBody error: %v", err)
		}
		if string(raw) != content {
			t.Errorf("body = %q, want file contents %q", raw, content)
		}
	})

	t.Run("from-file with invalid JSON is usage error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(path, []byte("not json {"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := nameOrFileBody(path, "name", "", missingMsg)
		_ = wantUsageError(t, err)
	})
}
