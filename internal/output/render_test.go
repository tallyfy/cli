package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseMode(t *testing.T) {
	cases := []struct {
		in      string
		want    Mode
		wantErr bool
	}{
		{"", ModeTable, false},
		{"table", ModeTable, false},
		{"json", ModeJSON, false},
		{"csv", ModeCSV, false},
		{"ndjson", ModeNDJSON, false},
		{"xml", "", true},
		{"JSON", "", true}, // case-sensitive
	}
	for _, tc := range cases {
		got, err := ParseMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseMode(%q): expected error, got %q", tc.in, got)
				continue
			}
			for _, m := range ValidModes {
				if !strings.Contains(err.Error(), string(m)) {
					t.Errorf("ParseMode(%q) error %q does not list valid mode %q", tc.in, err, m)
				}
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMode(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderTableGolden(t *testing.T) {
	var buf bytes.Buffer
	tbl := Fields([]string{"id", "name"}, [][]string{{"1", "alpha"}, {"22", "b"}}, nil)
	if err := Render(&buf, ModeTable, tbl); err != nil {
		t.Fatal(err)
	}
	want := "ID  NAME\n1   alpha\n22  b\n"
	if buf.String() != want {
		t.Errorf("table output:\n%q\nwant:\n%q", buf.String(), want)
	}
}

func TestRenderTableHeaderOnly(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, ModeTable, Table{Columns: []string{"id", "name"}}); err != nil {
		t.Fatal(err)
	}
	want := "ID  NAME\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestRenderTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, ModeTable, Table{}); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "" {
		t.Errorf("empty table rendered %q, want no output", buf.String())
	}
}

func TestRenderTableDefaultModeIsTable(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, "", Table{Columns: []string{"x"}, Rows: [][]string{{"1"}}}); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "X\n1\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderJSONSingleObject(t *testing.T) {
	var buf bytes.Buffer
	tbl := Table{JSONItems: []any{map[string]any{"id": "1", "name": "alpha"}}}
	if err := Render(&buf, ModeJSON, tbl); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"id\": \"1\",\n  \"name\": \"alpha\"\n}\n"
	if buf.String() != want {
		t.Errorf("json single:\n%q\nwant:\n%q", buf.String(), want)
	}
}

func TestRenderJSONList(t *testing.T) {
	var buf bytes.Buffer
	tbl := Table{JSONItems: []any{map[string]any{"id": "1"}, map[string]any{"id": "2"}}}
	if err := Render(&buf, ModeJSON, tbl); err != nil {
		t.Fatal(err)
	}
	want := "[\n  {\n    \"id\": \"1\"\n  },\n  {\n    \"id\": \"2\"\n  }\n]\n"
	if buf.String() != want {
		t.Errorf("json list:\n%q\nwant:\n%q", buf.String(), want)
	}
}

func TestRenderJSONEmpty(t *testing.T) {
	for _, items := range [][]any{nil, {}} {
		var buf bytes.Buffer
		if err := Render(&buf, ModeJSON, Table{JSONItems: items}); err != nil {
			t.Fatal(err)
		}
		if buf.String() != "[]\n" {
			t.Errorf("empty json rendered %q, want %q", buf.String(), "[]\n")
		}
	}
}

func TestRenderJSONDoesNotEscapeHTML(t *testing.T) {
	var buf bytes.Buffer
	tbl := Table{JSONItems: []any{map[string]any{"url": "https://x.test/?a=1&b=2"}}}
	if err := Render(&buf, ModeJSON, tbl); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "\\u0026") || !strings.Contains(buf.String(), "a=1&b=2") {
		t.Errorf("json escaped HTML: %q", buf.String())
	}
}

func TestRenderCSVQuoting(t *testing.T) {
	var buf bytes.Buffer
	tbl := Table{
		Columns: []string{"name", "note"},
		Rows: [][]string{
			{"Jo, Jr.", `said "hi"`},
			{"plain", "x"},
		},
	}
	if err := Render(&buf, ModeCSV, tbl); err != nil {
		t.Fatal(err)
	}
	want := "name,note\n\"Jo, Jr.\",\"said \"\"hi\"\"\"\nplain,x\n"
	if buf.String() != want {
		t.Errorf("csv:\n%q\nwant:\n%q", buf.String(), want)
	}
}

func TestRenderNDJSON(t *testing.T) {
	var buf bytes.Buffer
	tbl := Table{JSONItems: []any{map[string]any{"id": "1"}, map[string]any{"id": "2", "u": "a&b"}}}
	if err := Render(&buf, ModeNDJSON, tbl); err != nil {
		t.Fatal(err)
	}
	want := "{\"id\":\"1\"}\n{\"id\":\"2\",\"u\":\"a&b\"}\n"
	if buf.String() != want {
		t.Errorf("ndjson:\n%q\nwant:\n%q", buf.String(), want)
	}
}

func TestRenderNDJSONEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, ModeNDJSON, Table{}); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "" {
		t.Errorf("empty ndjson rendered %q, want no output", buf.String())
	}
}

func TestRenderInvalidMode(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, Mode("yaml"), Table{}); err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if buf.Len() != 0 {
		t.Errorf("invalid mode wrote output: %q", buf.String())
	}
}

func TestFieldsHelper(t *testing.T) {
	cols := []string{"a"}
	rows := [][]string{{"1"}}
	items := []any{map[string]any{"a": 1}}
	tbl := Fields(cols, rows, items)
	if len(tbl.Columns) != 1 || len(tbl.Rows) != 1 || len(tbl.JSONItems) != 1 {
		t.Errorf("Fields did not carry values through: %+v", tbl)
	}
}
