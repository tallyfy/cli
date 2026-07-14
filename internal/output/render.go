package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// ParseMode validates and normalizes an output mode string. The empty string
// selects ModeTable (the default); anything not in ValidModes is an error
// that lists the accepted values.
func ParseMode(s string) (Mode, error) {
	if s == "" {
		return ModeTable, nil
	}
	for _, m := range ValidModes {
		if string(m) == s {
			return m, nil
		}
	}
	return "", fmt.Errorf("invalid output mode %q (valid: %s)", s, modeList())
}

func modeList() string {
	names := make([]string, len(ValidModes))
	for i, m := range ValidModes {
		names[i] = string(m)
	}
	return strings.Join(names, ", ")
}

// Fields is a convenience constructor for Table.
func Fields(cols []string, rows [][]string, items []any) Table {
	return Table{Columns: cols, Rows: rows, JSONItems: items}
}

// Render writes t to w in the given mode. All modes end their output with a
// trailing newline (when they emit anything at all).
//
//   - table: tabwriter-aligned columns, UPPERCASE headers, no borders.
//   - json: JSONItems pretty-printed with 2-space indent; a single element is
//     emitted as that object, multiple elements as an array, none as [].
//   - csv: RFC 4180 via encoding/csv - header row from Columns, then Rows
//     (the pre-stringified table cells, not JSONItems).
//   - ndjson: one compact JSON document per JSONItems element per line.
func Render(w io.Writer, mode Mode, t Table) error {
	m, err := ParseMode(string(mode))
	if err != nil {
		return err
	}
	switch m {
	case ModeJSON:
		return renderJSON(w, t)
	case ModeCSV:
		return renderCSV(w, t)
	case ModeNDJSON:
		return renderNDJSON(w, t)
	default:
		return renderTable(w, t)
	}
}

func renderTable(w io.Writer, t Table) error {
	if len(t.Columns) == 0 && len(t.Rows) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
	if len(t.Columns) > 0 {
		upper := make([]string, len(t.Columns))
		for i, c := range t.Columns {
			upper[i] = strings.ToUpper(c)
		}
		if _, err := fmt.Fprintln(tw, strings.Join(upper, "\t")); err != nil {
			return err
		}
	}
	for _, row := range t.Rows {
		if _, err := fmt.Fprintln(tw, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func renderJSON(w io.Writer, t Table) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if len(t.JSONItems) == 1 {
		return enc.Encode(t.JSONItems[0])
	}
	items := t.JSONItems
	if items == nil {
		items = []any{}
	}
	return enc.Encode(items)
}

func renderCSV(w io.Writer, t Table) error {
	cw := csv.NewWriter(w)
	if len(t.Columns) > 0 {
		if err := cw.Write(t.Columns); err != nil {
			return err
		}
	}
	for _, row := range t.Rows {
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func renderNDJSON(w io.Writer, t Table) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, item := range t.JSONItems {
		if err := enc.Encode(item); err != nil {
			return err
		}
	}
	return nil
}
