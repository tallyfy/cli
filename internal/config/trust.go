package config

import (
	"fmt"
	"path/filepath"
)

// canonicalWorkspace returns the cleaned absolute form of projectDir used for
// trust comparisons.
func canonicalWorkspace(projectDir string) (string, error) {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// WorkspaceTrusted reports whether projectDir was trusted via `tallyfy trust`
// (an exact absolute-path match against state.json). Empty, unknown, or
// unreadable state all resolve to untrusted.
func WorkspaceTrusted(projectDir string) bool {
	if projectDir == "" {
		return false
	}
	want, err := canonicalWorkspace(projectDir)
	if err != nil {
		return false
	}
	st, err := LoadState()
	if err != nil {
		return false
	}
	for _, w := range st.TrustedWorkspaces {
		if filepath.Clean(w) == want {
			return true
		}
	}
	return false
}

// TrustWorkspace records projectDir (absolute) in state.json. Idempotent.
func TrustWorkspace(projectDir string) error {
	if projectDir == "" {
		return fmt.Errorf("no project directory to trust (no .tallyfy directory found)")
	}
	want, err := canonicalWorkspace(projectDir)
	if err != nil {
		return err
	}
	st, err := LoadState()
	if err != nil {
		return err
	}
	for _, w := range st.TrustedWorkspaces {
		if filepath.Clean(w) == want {
			return nil // already trusted
		}
	}
	st.TrustedWorkspaces = append(st.TrustedWorkspaces, want)
	return SaveState(st)
}

// UntrustWorkspace removes projectDir from state.json. Removing a workspace
// that was never trusted is not an error.
func UntrustWorkspace(projectDir string) error {
	if projectDir == "" {
		return fmt.Errorf("no project directory to untrust")
	}
	want, err := canonicalWorkspace(projectDir)
	if err != nil {
		return err
	}
	st, err := LoadState()
	if err != nil {
		return err
	}
	kept := st.TrustedWorkspaces[:0]
	changed := false
	for _, w := range st.TrustedWorkspaces {
		if filepath.Clean(w) == want {
			changed = true
			continue
		}
		kept = append(kept, w)
	}
	if !changed {
		return nil
	}
	st.TrustedWorkspaces = kept
	return SaveState(st)
}
