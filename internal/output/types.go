// Package output renders command results as table (human), json, csv, or
// ndjson (spec §6.8). Table is the default for TTYs; scripts select a machine
// format via -o/--output or the `output` setting.
//
// THIS FILE IS THE FROZEN CONTRACT consumed by internal/cli.
package output

// Mode is an output format.
type Mode string

const (
	ModeTable  Mode = "table"
	ModeJSON   Mode = "json"
	ModeCSV    Mode = "csv"
	ModeNDJSON Mode = "ndjson"
)

// ValidModes lists accepted -o values.
var ValidModes = []Mode{ModeTable, ModeJSON, ModeCSV, ModeNDJSON}

// Parse validates and normalizes a mode string ("" -> ModeTable).
// Implemented in render.go.

// Table is renderable tabular data. Rows are pre-stringified by commands;
// JSONItems carries the original objects so json/ndjson output is lossless.
type Table struct {
	Columns []string
	Rows    [][]string
	// JSONItems, when non-nil, is used for json/csv/ndjson modes instead of
	// Rows (one element per row; typically the raw API objects).
	JSONItems []any
}
