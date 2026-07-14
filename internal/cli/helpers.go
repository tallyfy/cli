package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tallyfy/cli/internal/output"
)

// Stdout is the writer commands render results to. Indirected for tests.
var Stdout io.Writer = os.Stdout

// Stderr is the writer commands log diagnostics to. Indirected for tests.
var Stderr io.Writer = os.Stderr

// RenderList renders a collection in the context's output mode. columns and
// rows drive table/csv output; items carries the original API objects so
// json/ndjson output is lossless. len(items) should equal len(rows).
func (c *Context) RenderList(columns []string, rows [][]string, items []any) error {
	return output.Render(Stdout, c.OutputMode, output.Table{
		Columns:   columns,
		Rows:      rows,
		JSONItems: items,
	})
}

// RenderItem renders a single object. In table mode it prints a two-column
// field/value view from columns+row; in json it prints item verbatim.
func (c *Context) RenderItem(columns []string, row []string, item any) error {
	if c.OutputMode == output.ModeTable {
		var b strings.Builder
		for i, col := range columns {
			val := ""
			if i < len(row) {
				val = row[i]
			}
			fmt.Fprintf(&b, "%s\t%s\n", col, val)
		}
		_, err := io.WriteString(Stdout, b.String())
		return err
	}
	return output.Render(Stdout, c.OutputMode, output.Table{
		Columns:   columns,
		Rows:      [][]string{row},
		JSONItems: []any{item},
	})
}

// Printf writes to the command's stdout unless --quiet.
func (c *Context) Printf(format string, a ...any) {
	if c.Quiet {
		return
	}
	fmt.Fprintf(Stdout, format, a...)
}

// Infof writes a status line to stderr unless --quiet (keeps stdout clean for
// piping).
func (c *Context) Infof(format string, a ...any) {
	if c.Quiet {
		return
	}
	fmt.Fprintf(Stderr, format, a...)
}

// DryRunf prints an intended-but-not-executed API action to stdout, prefixed
// so scripts can detect dry-run output.
func (c *Context) DryRunf(format string, a ...any) {
	fmt.Fprintf(Stdout, "[dry-run] "+format+"\n", a...)
}

// readInput reads a JSON/CSV payload from a file path, or from stdin when the
// argument is "-" or empty.
func readInput(fileArg string) ([]byte, error) {
	if fileArg == "" || fileArg == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(fileArg) //nolint:gosec // user-provided input path is intentional
}

// deref returns the pointed-to string or "" for a nil pointer (table cells).
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// truncate shortens s to n runes with an ellipsis marker, for table cells.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
