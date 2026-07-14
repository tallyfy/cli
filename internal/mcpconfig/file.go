package mcpconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SupportedClients are the snippet targets accepted by Snippet.
var SupportedClients = []string{"claude-code", "claude-desktop", "cursor"}

// Path returns the mcp.json location for a project directory.
func Path(projectDir string) string {
	return filepath.Join(projectDir, ".tallyfy", "mcp.json")
}

// Load reads <projectDir>/.tallyfy/mcp.json. A missing file yields an empty
// File with an initialized MCPServers map; invalid JSON is an error.
func Load(projectDir string) (*File, error) {
	f := &File{MCPServers: map[string]Server{}}
	data, err := os.ReadFile(Path(projectDir))
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, f); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON: %w", Path(projectDir), err)
	}
	if f.MCPServers == nil {
		f.MCPServers = map[string]Server{}
	}
	return f, nil
}

// Save writes mcp.json atomically (temp file + rename, mode 0644), creating
// the .tallyfy directory when needed.
func Save(projectDir string, f *File) error {
	if f == nil {
		return errors.New("mcpconfig: nil file")
	}
	if f.MCPServers == nil {
		f.MCPServers = map[string]Server{}
	}
	dir := filepath.Join(projectDir, ".tallyfy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".mcp-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, Path(projectDir)); err != nil {
		cleanup()
		return err
	}
	return nil
}

// Add registers (or replaces) a server entry.
func Add(f *File, name string, s Server) {
	if f.MCPServers == nil {
		f.MCPServers = map[string]Server{}
	}
	f.MCPServers[name] = s
}

// Remove deletes a server entry, reporting whether it existed.
func Remove(f *File, name string) bool {
	if f == nil || f.MCPServers == nil {
		return false
	}
	if _, ok := f.MCPServers[name]; !ok {
		return false
	}
	delete(f.MCPServers, name)
	return true
}

// Snippet renders a ready-to-paste registration for a given MCP client:
//
//   - "claude-code": a `claude mcp add` one-liner
//   - "claude-desktop", "cursor": a pretty {"mcpServers": {...}} JSON fragment
//
// Unknown clients are an error listing the supported ones.
func Snippet(client string, name string, s Server) (string, error) {
	switch client {
	case "claude-code":
		if s.Type == "stdio" {
			parts := append([]string{"claude", "mcp", "add", "--transport", "stdio", name, "--", s.Cmd}, s.Args...)
			return strings.Join(parts, " "), nil
		}
		return fmt.Sprintf("claude mcp add --transport http %s %s", name, s.URL), nil
	case "claude-desktop", "cursor":
		fragment := File{MCPServers: map[string]Server{name: s}}
		data, err := json.MarshalIndent(fragment, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	default:
		return "", fmt.Errorf("unsupported MCP client %q (supported: %s)", client, strings.Join(SupportedClients, ", "))
	}
}
