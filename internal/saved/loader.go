package saved

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// UserHomeDir resolves the user's home directory. Package variable so tests
// can point the user layer at a fixture directory.
var UserHomeDir = os.UserHomeDir

// LastWarnings holds non-fatal per-file problems (unreadable file, invalid
// YAML, missing "command" field, duplicate names) from the most recent
// LoadAll call. It is reset at the start of every LoadAll. Callers may print
// these to stderr; LoadAll only returns an error when nothing loaded at all.
var LastWarnings []string

// LoadAll reads saved commands from ~/.tallyfy/commands/*.yaml (Source
// "user") and then <projectDir>/.tallyfy/commands/*.yaml (Source "project").
// A project command replaces a user command with the same effective name
// (explicit `name:` field, or the filename without .yaml when omitted).
// Missing directories are fine. Invalid files are skipped with a note in
// LastWarnings; an error is returned only when files existed but nothing
// could be loaded.
func LoadAll(projectDir string) ([]Command, error) {
	LastWarnings = nil
	byName := map[string]Command{}

	if home, err := UserHomeDir(); err == nil && home != "" {
		loadDir(filepath.Join(home, ".tallyfy", "commands"), "user", byName)
	} else if err != nil {
		LastWarnings = append(LastWarnings, "cannot resolve home directory: "+err.Error())
	}
	if projectDir != "" {
		loadDir(filepath.Join(projectDir, ".tallyfy", "commands"), "project", byName)
	}

	out := make([]Command, 0, len(byName))
	for _, c := range byName {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	if len(out) == 0 && len(LastWarnings) > 0 {
		return nil, errors.New("no saved commands loaded: " + strings.Join(LastWarnings, "; "))
	}
	return out, nil
}

// loadDir loads every *.yaml in dir into byName, stamping Source and Path.
// Entries from a later-loaded source replace same-named earlier ones.
func loadDir(dir, source string, byName map[string]Command) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return // ErrBadPattern cannot happen with a literal suffix
	}
	sort.Strings(matches)
	for _, path := range matches {
		data, err := os.ReadFile(path) //nolint:gosec // G304: reads the user's own saved-command *.yaml files from resolved config dirs
		if err != nil {
			LastWarnings = append(LastWarnings, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		var c Command
		if err := yaml.Unmarshal(data, &c); err != nil {
			LastWarnings = append(LastWarnings, fmt.Sprintf("%s: invalid YAML: %v", path, err))
			continue
		}
		if strings.TrimSpace(c.Name) == "" {
			c.Name = strings.TrimSuffix(filepath.Base(path), ".yaml")
		}
		if strings.TrimSpace(c.Command) == "" {
			LastWarnings = append(LastWarnings, fmt.Sprintf("%s: missing required %q field", path, "command"))
			continue
		}
		c.Source = source
		c.Path = path
		if prev, ok := byName[c.Name]; ok && prev.Source == source {
			LastWarnings = append(LastWarnings, fmt.Sprintf("duplicate saved command %q: %s overrides %s", c.Name, path, prev.Path))
		}
		byName[c.Name] = c
	}
}

// Find returns the saved command with the given name.
func Find(cmds []Command, name string) (*Command, bool) {
	for i := range cmds {
		if cmds[i].Name == name {
			return &cmds[i], true
		}
	}
	return nil, false
}

// placeholderRe matches {{param}} placeholders (optional inner whitespace).
var placeholderRe = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}`)

// Expand substitutes {{param}} placeholders in c.Command with values from
// params, then splits the result into argv with a minimal quote-aware
// splitter (double and single quotes; backslash escapes for \" and \\ inside
// double quotes). No shell is ever invoked.
//
// Every name in c.Params is required: any missing value is an error naming
// all missing parameters. Any {{placeholder}} not declared in c.Params is
// also an error. Values containing spaces need a quoted placeholder in the
// template (e.g. --field name="{{name}}") because substitution happens
// before splitting.
func Expand(c Command, params map[string]string) ([]string, error) {
	declared := make(map[string]bool, len(c.Params))
	for _, p := range c.Params {
		declared[p] = true
	}

	var missing []string
	for _, p := range c.Params {
		if _, ok := params[p]; !ok {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("saved command %q: missing required parameter(s): %s (pass --%s <value>)",
			c.Name, strings.Join(missing, ", "), strings.Join(missing, " <value> --"))
	}

	var unknown []string
	seen := map[string]bool{}
	expanded := placeholderRe.ReplaceAllStringFunc(c.Command, func(m string) string {
		name := placeholderRe.FindStringSubmatch(m)[1]
		if !declared[name] {
			if !seen[name] {
				unknown = append(unknown, name)
				seen[name] = true
			}
			return m
		}
		return params[name]
	})
	if len(unknown) > 0 {
		return nil, fmt.Errorf("saved command %q: placeholder(s) not declared in params: %s",
			c.Name, "{{"+strings.Join(unknown, "}}, {{")+"}}")
	}

	return splitArgs(expanded)
}

// splitArgs splits a command line into argv. Double-quoted segments may
// contain \" and \\ escapes; single-quoted segments are literal; adjacent
// quoted/unquoted segments concatenate into one token. Backslash outside
// double quotes is a literal character. Unterminated quotes are an error.
func splitArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inToken := false
	i := 0
	for i < len(s) {
		switch ch := s[i]; ch {
		case ' ', '\t', '\n', '\r':
			if inToken {
				args = append(args, cur.String())
				cur.Reset()
				inToken = false
			}
			i++
		case '\'':
			inToken = true
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, fmt.Errorf("unterminated single quote in command: %s", s)
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 2
		case '"':
			inToken = true
			i++
			closed := false
			for i < len(s) {
				c := s[i]
				if c == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
					cur.WriteByte(s[i+1])
					i += 2
					continue
				}
				if c == '"' {
					closed = true
					i++
					break
				}
				cur.WriteByte(c)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated double quote in command: %s", s)
			}
		default:
			inToken = true
			cur.WriteByte(ch)
			i++
		}
	}
	if inToken {
		args = append(args, cur.String())
	}
	return args, nil
}
