package update

import (
	"io"

	"github.com/tallyfy/cli/internal/config"
)

// MaybeNotice prints the passive once-per-24h update notice. REPLACED BY LANE L5.
func MaybeNotice(cfg *config.Resolved, w io.Writer, outputMode string, quiet bool) {}
