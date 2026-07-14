package update

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// State is the persisted passive-check state (~/.tallyfy/update-check.json).
type State struct {
	LastCheckUnix int64  `json:"last_check_unix"`
	LatestSeen    string `json:"latest_seen"`
}

// StatePath resolves the state file location; package variable so tests can
// redirect it to a temp file.
var StatePath = defaultStatePath

func defaultStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tallyfy", "update-check.json"), nil
}

// readState loads the state file; any error yields the zero State (a missing
// or corrupt file simply means "never checked").
func readState() State {
	var st State
	path, err := StatePath()
	if err != nil {
		return st
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(data, &st)
	return st
}

// writeState persists st atomically (temp file in the same directory +
// rename), creating ~/.tallyfy when needed.
func writeState(st State) error {
	path, err := StatePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".update-check-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
