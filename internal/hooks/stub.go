package hooks

import "github.com/tallyfy/cli/internal/config"

type stubRunner struct{}

func (stubRunner) Fire(event string, hs []config.Hook, p Payload) ([]string, error) { return nil, nil }

// NewRunner returns the hook runner. REPLACED BY LANE L4.
func NewRunner(opts Options) Runner { return stubRunner{} }
