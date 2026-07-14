package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// maxBackups is how many timestamped backups of a settings file are kept.
const maxBackups = 5

// nowUnix is indirected for deterministic backup names in tests.
var nowUnix = func() int64 { return time.Now().Unix() }

// EffectiveEntry is one row of `tallyfy config list --show-sources`.
type EffectiveEntry struct {
	Key   string
	Value string
	Scope Scope
	File  string
}

// Get returns the resolved value for a dotted settings key. List values
// render as JSON arrays; booleans as "true"/"false". The second return is
// false for unknown keys.
func Get(r *Resolved, key string) (string, bool) {
	switch key {
	case "org":
		return r.Org, true
	case "baseUrl":
		return r.BaseURL, true
	case "output":
		return r.Output, true
	case "permissions.defaultMode":
		return r.PermissionDefaultMode, true
	case "permissions.allowManagedRulesOnly":
		return strconv.FormatBool(r.AllowManagedRulesOnly), true
	case "permissions.allow":
		return renderRules(r.Allow), true
	case "permissions.ask":
		return renderRules(r.Ask), true
	case "permissions.deny":
		return renderRules(r.Deny), true
	case "mcp.enableAllProjectServers":
		return strconv.FormatBool(r.MCPEnableAllProjectServers), true
	case "mcp.enabledServers":
		return renderStrings(r.MCPEnabledServers), true
	case "mcp.disabledServers":
		return renderStrings(r.MCPDisabledServers), true
	case "allowedMcpServers":
		return renderStrings(r.AllowedMcpServers), true
	case "deniedMcpServers":
		return renderStrings(r.DeniedMcpServers), true
	case "hooks":
		return renderJSON(r.Hooks), true
	case "env":
		return renderJSON(r.Env), true
	case "auth.loginMethod":
		return r.AuthLoginMethod, true
	case "auth.apiKeyHelper":
		return r.APIKeyHelper, true
	case "auth.forceOrg":
		return r.ForceOrg, true
	case "telemetry.enabled":
		return strconv.FormatBool(r.TelemetryEnabled), true
	case "telemetry.endpoint":
		return r.TelemetryEndpoint, true
	case "update.channel":
		return r.UpdateChannel, true
	case "update.autoUpdate":
		return strconv.FormatBool(r.UpdateAutoUpdate), true
	case "requiredMinimumVersion":
		return r.RequiredMinimumVersion, true
	case "requiredMaximumVersion":
		return r.RequiredMaximumVersion, true
	}
	if name, ok := strings.CutPrefix(key, "env."); ok && name != "" {
		v, present := r.Env[name]
		return v, present
	}
	return "", false
}

func renderRules(rules []Rule) string {
	raws := make([]string, 0, len(rules))
	for _, ru := range rules {
		raws = append(raws, ru.Raw)
	}
	return renderStrings(raws)
}

func renderStrings(vals []string) string {
	if vals == nil {
		vals = []string{}
	}
	return renderJSON(vals)
}

func renderJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// ListEffective returns the ordered (key, value, scope) rows for
// `tallyfy config list --show-sources`: one row per resolved scalar key (its
// winning scope) and one row per merged list element (its contributing
// scope), sorted by key.
func ListEffective(r *Resolved) []EffectiveEntry {
	out := make([]EffectiveEntry, 0, len(r.Sources))
	for _, si := range r.Sources {
		out = append(out, EffectiveEntry(si))
	}
	return out
}

// keyKind types the value written by SetKey.
type keyKind int

const (
	kindString keyKind = iota
	kindBool
	kindStringList
)

// settableKeys maps every SetKey-writable dotted key to its JSON type.
var settableKeys = map[string]keyKind{
	"org":                         kindString,
	"baseUrl":                     kindString,
	"output":                      kindString,
	"permissions.defaultMode":     kindString,
	"permissions.allow":           kindStringList,
	"permissions.ask":             kindStringList,
	"permissions.deny":            kindStringList,
	"mcp.enableAllProjectServers": kindBool,
	"mcp.enabledServers":          kindStringList,
	"mcp.disabledServers":         kindStringList,
	"auth.loginMethod":            kindString,
	"auth.apiKeyHelper":           kindString,
	"telemetry.enabled":           kindBool,
	"telemetry.endpoint":          kindString,
	"update.channel":              kindString,
	"update.autoUpdate":           kindBool,
}

// managedOnlyKeys cannot be written by SetKey: they only take effect from
// managed-settings.json, so persisting them elsewhere would mislead.
var managedOnlyKeys = map[string]bool{
	"requiredMinimumVersion":            true,
	"requiredMaximumVersion":            true,
	"permissions.allowManagedRulesOnly": true,
	"auth.forceOrg":                     true,
	"allowedMcpServers":                 true,
	"deniedMcpServers":                  true,
}

// enumKeys validates enum-valued keys at write time.
var enumKeys = map[string]map[string]bool{
	"output":                  validOutputs,
	"permissions.defaultMode": validDefaultModes,
	"update.channel":          validChannels,
	"auth.loginMethod":        validLoginMethods,
}

// SetKey writes one dotted key into the settings file for scope ("user",
// "project", or "local"), creating intermediate objects as needed. Before
// each write the existing file is copied to <file>.bak.<unix-ts> and only
// the 5 newest backups are kept; the write itself is atomic.
func SetKey(scope, projectDir, key, value string) error {
	path, err := settingsFileForScope(scope, projectDir)
	if err != nil {
		return err
	}
	if managedOnlyKeys[key] {
		return fmt.Errorf("%q is a managed-only setting; set it in managed-settings.json under %s",
			key, strings.Join(managedDirs(), " or "))
	}
	typed, err := coerceValue(key, value)
	if err != nil {
		return err
	}
	m := map[string]any{}
	if data, rerr := os.ReadFile(path); rerr == nil { //nolint:gosec // G304: reads a Tallyfy settings file the CLI manages
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("%s: invalid JSON (%v); fix or remove the file before writing to it", path, err)
		}
	} else if !os.IsNotExist(rerr) {
		return rerr
	}
	if err := setDotted(m, key, typed); err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	return writeSettingsWithBackup(path, m)
}

// settingsFileForScope maps a write scope name to its settings file path.
func settingsFileForScope(scope, projectDir string) (string, error) {
	switch scope {
	case "user":
		home, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		return filepath.Join(home, ".tallyfy", "settings.json"), nil
	case "project":
		if projectDir == "" {
			return "", fmt.Errorf("not inside a project (no .tallyfy directory found); cannot write project settings")
		}
		return filepath.Join(projectDir, ".tallyfy", "settings.json"), nil
	case "local":
		if projectDir == "" {
			return "", fmt.Errorf("not inside a project (no .tallyfy directory found); cannot write local settings")
		}
		return filepath.Join(projectDir, ".tallyfy", "settings.local.json"), nil
	default:
		return "", fmt.Errorf("unknown settings scope %q (valid: user, project, local)", scope)
	}
}

// coerceValue converts the string value from the command line into the JSON
// type the key expects, validating enums.
func coerceValue(key, value string) (any, error) {
	if ev := enumKeys[key]; ev != nil && !ev[value] {
		valid := make([]string, 0, len(ev))
		for v := range ev {
			valid = append(valid, v)
		}
		sort.Strings(valid)
		return nil, fmt.Errorf("invalid value %q for %s (valid: %s)", value, key, strings.Join(valid, ", "))
	}
	kind, known := settableKeys[key]
	if !known {
		if name, ok := strings.CutPrefix(key, "env."); ok && name != "" {
			return value, nil
		}
		return nil, fmt.Errorf("unknown or unsupported settings key %q", key)
	}
	switch kind {
	case kindBool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("%s expects true or false, got %q", key, value)
		}
		return b, nil
	case kindStringList:
		if strings.HasPrefix(strings.TrimSpace(value), "[") {
			var list []string
			if err := json.Unmarshal([]byte(value), &list); err != nil {
				return nil, fmt.Errorf("%s expects a JSON string array or comma-separated values: %v", key, err)
			}
			return list, nil
		}
		var list []string
		for _, part := range strings.Split(value, ",") {
			if p := strings.TrimSpace(part); p != "" {
				list = append(list, p)
			}
		}
		if list == nil {
			list = []string{}
		}
		return list, nil
	default:
		return value, nil
	}
}

// setDotted sets a dotted key inside a decoded JSON object, creating
// intermediate objects. It refuses to overwrite a non-object intermediate.
func setDotted(m map[string]any, key string, value any) error {
	parts := strings.Split(key, ".")
	cur := m
	for i, p := range parts[:len(parts)-1] {
		next, ok := cur[p]
		if !ok || next == nil {
			nm := map[string]any{}
			cur[p] = nm
			cur = nm
			continue
		}
		nm, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("cannot set %q: %q is not a JSON object", key, strings.Join(parts[:i+1], "."))
		}
		cur = nm
	}
	cur[parts[len(parts)-1]] = value
	return nil
}

// writeSettingsWithBackup backs up the existing file to <file>.bak.<unix-ts>,
// rotates to the 5 newest backups, then writes the new content atomically.
func writeSettingsWithBackup(path string, m map[string]any) error {
	if data, err := os.ReadFile(path); err == nil { //nolint:gosec // G304: reads the CLI's own settings file to back it up
		bak := fmt.Sprintf("%s.bak.%d", path, nowUnix())
		if err := os.WriteFile(bak, data, 0o600); err != nil { //nolint:gosec // G703: bak is derived from the CLI's own settings path (path + ".bak.<ts>")
			return fmt.Errorf("cannot write backup %s: %v", bak, err)
		}
		if err := rotateBackups(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(out, '\n'), 0o600)
}

// rotateBackups removes all but the maxBackups newest <path>.bak.<unix-ts>
// files. Files whose suffix is not a unix timestamp are left alone.
func rotateBackups(path string) error {
	matches, err := filepath.Glob(path + ".bak.*")
	if err != nil {
		return err
	}
	type backup struct {
		name string
		ts   int64
	}
	var backups []backup
	for _, name := range matches {
		suffix := strings.TrimPrefix(name, path+".bak.")
		ts, err := strconv.ParseInt(suffix, 10, 64)
		if err != nil {
			continue // not one of ours; never delete what we don't understand
		}
		backups = append(backups, backup{name: name, ts: ts})
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].ts > backups[j].ts })
	for _, old := range backups[min(maxBackups, len(backups)):] {
		if err := os.Remove(old.name); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
