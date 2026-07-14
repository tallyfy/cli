package config

import "errors"

// Load merges all scopes into a Resolved config. REPLACED BY LANE L1.
func Load(o Overrides) (*Resolved, error) {
	return nil, errors.New("config.Load not implemented yet (lane L1)")
}

// WorkspaceTrusted reports whether projectDir was trusted via `tallyfy trust`.
// REPLACED BY LANE L1.
func WorkspaceTrusted(projectDir string) bool { return false }
