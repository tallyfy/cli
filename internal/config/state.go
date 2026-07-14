package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// State is the persisted session/auth context (~/.tallyfy/state.json):
// current org and per-project trust decisions. Actual secrets live in the
// keychain, never here (spec §6.2).
type State struct {
	CurrentOrg        string   `json:"current_org"`
	TrustedWorkspaces []string `json:"trusted_workspaces"`
}

// statePath returns the state.json location. It derives from the
// test-overridable userHomeDir, so tests redirect it via that hook.
func statePath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".tallyfy", "state.json"), nil
}

// LoadState reads ~/.tallyfy/state.json. A missing file yields an empty state
// and no error; a corrupt file yields an empty state and an error.
func LoadState() (*State, error) {
	p, err := statePath()
	if err != nil {
		return &State{}, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return &State{}, err
	}
	st := &State{}
	if err := json.Unmarshal(data, st); err != nil {
		return &State{}, fmt.Errorf("%s: invalid JSON: %v", p, err)
	}
	return st, nil
}

// SaveState writes state.json atomically (temp file + rename) with 0600
// permissions, creating ~/.tallyfy (0700) as needed.
func SaveState(s *State) error {
	p, err := statePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(p, append(data, '\n'), 0o600)
}

// writeFileAtomic writes data to path via a same-directory temp file followed
// by rename, creating the parent directory (0700) as needed.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
