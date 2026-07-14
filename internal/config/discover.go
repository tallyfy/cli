package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

// Package-level indirections so tests can redirect every filesystem root the
// loader touches (real ~/.tallyfy, the working dir, /etc/tallyfy) into
// t.TempDir(). Production code never overrides these.
var (
	userHomeDir = os.UserHomeDir
	workingDir  = os.Getwd
	managedDirs = defaultManagedDirs
)

// defaultManagedDirs returns the OS system paths searched for managed
// settings: /etc/tallyfy (Linux/macOS) or %ProgramData%\Tallyfy (Windows).
func defaultManagedDirs() []string {
	if runtime.GOOS == "windows" {
		pd := os.Getenv("ProgramData")
		if pd == "" {
			return nil
		}
		return []string{filepath.Join(pd, "Tallyfy")}
	}
	return []string{"/etc/tallyfy"}
}

// userSettingsPath returns ~/.tallyfy/settings.json, or "" when the home
// directory cannot be determined.
func userSettingsPath() string {
	home, err := userHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".tallyfy", "settings.json")
}

// discoverProjectDir walks up from cwd looking for the nearest directory that
// contains a .tallyfy directory and returns it as the project dir. The walk
// stops (exclusive) at $HOME — ~/.tallyfy is the user scope, never a project —
// and at the filesystem root.
func discoverProjectDir(cwd, home string) string {
	dir := filepath.Clean(cwd)
	if home != "" {
		home = filepath.Clean(home)
	}
	for {
		if home != "" && dir == home {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir { // filesystem root reached; the root itself is not a project
			return ""
		}
		if fi, err := os.Stat(filepath.Join(dir, ".tallyfy")); err == nil && fi.IsDir() {
			return dir
		}
		dir = parent
	}
}

// managedSettingsFiles returns managed-settings.json plus the
// managed-settings.d/*.json drop-ins (sorted alphabetically) for every managed
// dir, in ascending apply order. Missing files and dirs are simply absent.
func managedSettingsFiles() []string {
	var files []string
	for _, dir := range managedDirs() {
		main := filepath.Join(dir, "managed-settings.json")
		if fi, err := os.Stat(main); err == nil && !fi.IsDir() {
			files = append(files, main)
		}
		dropDir := filepath.Join(dir, "managed-settings.d")
		entries, err := os.ReadDir(dropDir)
		if err != nil {
			continue
		}
		var drops []string
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
				continue
			}
			drops = append(drops, filepath.Join(dropDir, e.Name()))
		}
		sort.Strings(drops)
		files = append(files, drops...)
	}
	return files
}
