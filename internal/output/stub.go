package output

import (
	"errors"
	"io"
)

// ParseMode validates an output mode string. REPLACED BY LANE L5.
func ParseMode(s string) (Mode, error) {
	if s == "" {
		return ModeTable, nil
	}
	for _, m := range ValidModes {
		if string(m) == s {
			return m, nil
		}
	}
	return "", errors.New("invalid output mode: " + s)
}

// Render writes t in the given mode. REPLACED BY LANE L5.
func Render(w io.Writer, mode Mode, t Table) error {
	return errors.New("output.Render not implemented (L5)")
}
