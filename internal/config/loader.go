package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// EnvBaseURL is the environment variable consulted at flags scope when
// --base-url is not passed.
const EnvBaseURL = "TALLYFY_BASE_URL"

var (
	validOutputs      = map[string]bool{"table": true, "json": true, "csv": true, "ndjson": true}
	validDefaultModes = map[string]bool{"allow": true, "ask": true, "deny": true}
	validChannels     = map[string]bool{"stable": true, "latest": true}
	validLoginMethods = map[string]bool{"token": true, "oauth": true}
)

// layer is one settings source about to be merged into a Resolved.
type layer struct {
	scope Scope
	file  string // absolute path, "(--settings inline)", or "(flag)"
	s     *Settings
	// trusted reports workspace trust; only consulted for ScopeProject
	// (gates auth.apiKeyHelper).
	trusted bool
}

// Load merges all scopes (Defaults < User < Project < Local < Flags < Managed)
// into a Resolved config per spec §6.2: scalars override, list keys merge.
func Load(o Overrides) (*Resolved, error) {
	def := Defaults()
	r := &def
	tr := newSourceTracker()
	seedDefaultSources(tr, r)
	var warnings []string

	home := ""
	if h, err := userHomeDir(); err == nil {
		home = h
	}

	// 1. User scope: ~/.tallyfy/settings.json.
	if p := userSettingsPath(); p != "" {
		s, err := readSettingsLayer(p, false, &warnings)
		if err != nil {
			return nil, err
		}
		if err := applyLayer(r, layer{scope: ScopeUser, file: p, s: s}, tr, &warnings); err != nil {
			return nil, err
		}
	}

	// 2 + 3. Project and Local scopes, from the nearest .tallyfy walking up.
	if cwd, err := workingDir(); err == nil {
		r.ProjectDir = discoverProjectDir(cwd, home)
	}
	if r.ProjectDir != "" {
		trusted := WorkspaceTrusted(r.ProjectDir)
		pj := filepath.Join(r.ProjectDir, ".tallyfy", "settings.json")
		s, err := readSettingsLayer(pj, false, &warnings)
		if err != nil {
			return nil, err
		}
		if err := applyLayer(r, layer{scope: ScopeProject, file: pj, s: s, trusted: trusted}, tr, &warnings); err != nil {
			return nil, err
		}
		lc := filepath.Join(r.ProjectDir, ".tallyfy", "settings.local.json")
		s, err = readSettingsLayer(lc, false, &warnings)
		if err != nil {
			return nil, err
		}
		if err := applyLayer(r, layer{scope: ScopeLocal, file: lc, s: s}, tr, &warnings); err != nil {
			return nil, err
		}
	}

	// 4. Flags scope: the --settings layer, then individual flag overrides.
	if arg := strings.TrimSpace(o.SettingsArg); arg != "" {
		var s *Settings
		var err error
		file := arg
		if strings.HasPrefix(arg, "{") {
			file = "(--settings inline)"
			s, err = parseSettingsJSON([]byte(arg), file, true, &warnings)
		} else {
			s, err = readSettingsLayer(arg, true, &warnings)
		}
		if err != nil {
			return nil, err
		}
		if err := applyLayer(r, layer{scope: ScopeFlags, file: file, s: s}, tr, &warnings); err != nil {
			return nil, err
		}
	}
	if o.Org != "" {
		r.Org = o.Org
		tr.setScalar("org", o.Org, ScopeFlags, "(flag)")
	}
	if o.Output != "" {
		if !validOutputs[o.Output] {
			return nil, fmt.Errorf("invalid output %q (valid: table, json, csv, ndjson)", o.Output)
		}
		r.Output = o.Output
		tr.setScalar("output", o.Output, ScopeFlags, "(flag)")
	}
	baseURL, baseFile := o.BaseURL, "(flag)"
	if baseURL == "" {
		if env := os.Getenv(EnvBaseURL); env != "" {
			baseURL, baseFile = env, "(env "+EnvBaseURL+")"
		}
	}
	if baseURL != "" {
		r.BaseURL = baseURL
		tr.setScalar("baseUrl", baseURL, ScopeFlags, baseFile)
	}

	// 5. Managed scope, always last so managed values win. Always tolerant:
	// a broken managed file is skipped with a warning, never fatal.
	for _, f := range managedSettingsFiles() {
		s, err := readSettingsLayer(f, false, &warnings)
		if err != nil {
			return nil, err
		}
		if err := applyLayer(r, layer{scope: ScopeManaged, file: f, s: s}, tr, &warnings); err != nil {
			return nil, err
		}
	}

	// 6. Org fallback: state.json current_org when no scope set one.
	if r.Org == "" {
		st, err := LoadState()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("state: %v", err))
		} else if st.CurrentOrg != "" {
			r.Org = st.CurrentOrg
			if p, perr := statePath(); perr == nil {
				tr.setScalar("org", r.Org, ScopeUser, p)
			}
		}
	}

	r.Sources = tr.finalize()
	r.Warnings = warnings
	return r, nil
}

// readSettingsLayer reads and tolerantly parses one settings file. A missing
// file yields (nil, nil). With fatal == false, unreadable or syntactically
// invalid files are skipped with a warning; with fatal == true (--settings)
// they are errors.
func readSettingsLayer(path string, fatal bool, warnings *[]string) (*Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !fatal {
			return nil, nil
		}
		if fatal {
			return nil, fmt.Errorf("--settings: %w", err)
		}
		*warnings = append(*warnings, fmt.Sprintf("%s: unreadable (%v); file skipped", path, err))
		return nil, nil
	}
	return parseSettingsJSON(data, path, fatal, warnings)
}

// parseSettingsJSON decodes into map[string]any first, then extracts known
// keys with type checks so a single bad entry never discards a whole file.
func parseSettingsJSON(data []byte, file string, fatal bool, warnings *[]string) (*Settings, error) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		if fatal {
			return nil, fmt.Errorf("%s: invalid JSON: %v", file, err)
		}
		*warnings = append(*warnings, fmt.Sprintf("%s: invalid JSON (%v); file skipped", file, err))
		return nil, nil
	}
	p := &settingsParser{file: file, warnings: warnings}
	return p.settings(m), nil
}

// settingsParser extracts typed values from a decoded JSON map, stripping
// type-mismatched entries with one warning each.
type settingsParser struct {
	file     string
	warnings *[]string
}

func (p *settingsParser) warn(key string) {
	*p.warnings = append(*p.warnings, fmt.Sprintf("%s: invalid value for %q; entry ignored", p.file, key))
}

func (p *settingsParser) str(v any, key string) *string {
	if sv, ok := v.(string); ok {
		return &sv
	}
	p.warn(key)
	return nil
}

func (p *settingsParser) boolean(v any, key string) *bool {
	if bv, ok := v.(bool); ok {
		return &bv
	}
	p.warn(key)
	return nil
}

func (p *settingsParser) strList(v any, key string) []string {
	lv, ok := v.([]any)
	if !ok {
		p.warn(key)
		return nil
	}
	var out []string
	for i, e := range lv {
		es, ok := e.(string)
		if !ok {
			p.warn(fmt.Sprintf("%s[%d]", key, i))
			continue
		}
		out = append(out, es)
	}
	return out
}

func (p *settingsParser) obj(v any, key string) map[string]any {
	ov, ok := v.(map[string]any)
	if !ok {
		p.warn(key)
		return nil
	}
	return ov
}

func (p *settingsParser) strMap(v any, key string) map[string]string {
	ov := p.obj(v, key)
	if ov == nil {
		return nil
	}
	out := map[string]string{}
	for _, k := range sortedKeys(ov) {
		e := ov[k]
		if e == nil {
			continue
		}
		es, ok := e.(string)
		if !ok {
			p.warn(key + "." + k)
			continue
		}
		out[k] = es
	}
	return out
}

func (p *settingsParser) settings(m map[string]any) *Settings {
	s := &Settings{}
	for _, key := range sortedKeys(m) {
		v := m[key]
		if v == nil {
			continue // explicit null means "unset" (matches the spec example)
		}
		switch key {
		case "$schema":
			if sv, ok := v.(string); ok {
				s.Schema = sv
			}
		case "org":
			s.Org = p.str(v, key)
		case "baseUrl":
			s.BaseURL = p.str(v, key)
		case "output":
			s.Output = p.str(v, key)
		case "permissions":
			if o := p.obj(v, key); o != nil {
				s.Permissions = p.permissions(o)
			}
		case "mcp":
			if o := p.obj(v, key); o != nil {
				s.MCP = p.mcp(o)
			}
		case "hooks":
			if o := p.obj(v, key); o != nil {
				s.Hooks = p.hooks(o)
			}
		case "env":
			s.Env = p.strMap(v, key)
		case "auth":
			if o := p.obj(v, key); o != nil {
				s.Auth = p.auth(o)
			}
		case "telemetry":
			if o := p.obj(v, key); o != nil {
				s.Telemetry = p.telemetry(o)
			}
		case "update":
			if o := p.obj(v, key); o != nil {
				s.Update = p.update(o)
			}
		case "requiredMinimumVersion":
			s.RequiredMinimumVersion = p.str(v, key)
		case "requiredMaximumVersion":
			s.RequiredMaximumVersion = p.str(v, key)
		case "allowedMcpServers":
			s.AllowedMcpServers = p.strList(v, key)
		case "deniedMcpServers":
			s.DeniedMcpServers = p.strList(v, key)
		default:
			// Unknown top-level keys are ignored (forward compatibility).
		}
	}
	return s
}

func (p *settingsParser) permissions(o map[string]any) *PermissionsBlock {
	b := &PermissionsBlock{}
	for _, k := range sortedKeys(o) {
		v := o[k]
		if v == nil {
			continue
		}
		switch k {
		case "defaultMode":
			b.DefaultMode = p.str(v, "permissions.defaultMode")
		case "allow":
			b.Allow = p.strList(v, "permissions.allow")
		case "ask":
			b.Ask = p.strList(v, "permissions.ask")
		case "deny":
			b.Deny = p.strList(v, "permissions.deny")
		case "allowManagedRulesOnly":
			b.AllowManagedRulesOnly = p.boolean(v, "permissions.allowManagedRulesOnly")
		}
	}
	return b
}

func (p *settingsParser) mcp(o map[string]any) *MCPBlock {
	b := &MCPBlock{}
	for _, k := range sortedKeys(o) {
		v := o[k]
		if v == nil {
			continue
		}
		switch k {
		case "enableAllProjectServers":
			b.EnableAllProjectServers = p.boolean(v, "mcp.enableAllProjectServers")
		case "enabledServers":
			b.EnabledServers = p.strList(v, "mcp.enabledServers")
		case "disabledServers":
			b.DisabledServers = p.strList(v, "mcp.disabledServers")
		}
	}
	return b
}

func (p *settingsParser) hooks(o map[string]any) map[string][]Hook {
	out := map[string][]Hook{}
	for _, ev := range sortedKeys(o) {
		v := o[ev]
		if v == nil {
			continue
		}
		list, ok := v.([]any)
		if !ok {
			p.warn("hooks." + ev)
			continue
		}
		for i, e := range list {
			eo, ok := e.(map[string]any)
			if !ok {
				p.warn(fmt.Sprintf("hooks.%s[%d]", ev, i))
				continue
			}
			h := Hook{}
			valid := true
			for _, f := range sortedKeys(eo) {
				fv := eo[f]
				if fv == nil {
					continue
				}
				fs, ok := fv.(string)
				if !ok {
					p.warn(fmt.Sprintf("hooks.%s[%d].%s", ev, i, f))
					valid = false
					break
				}
				switch f {
				case "matcher":
					h.Matcher = fs
				case "command":
					h.Command = fs
				case "type":
					h.Type = fs
				case "url":
					h.URL = fs
				}
			}
			if valid {
				out[ev] = append(out[ev], h)
			}
		}
	}
	return out
}

func (p *settingsParser) auth(o map[string]any) *AuthBlock {
	b := &AuthBlock{}
	for _, k := range sortedKeys(o) {
		v := o[k]
		if v == nil {
			continue
		}
		switch k {
		case "loginMethod":
			b.LoginMethod = p.str(v, "auth.loginMethod")
		case "apiKeyHelper":
			b.APIKeyHelper = p.str(v, "auth.apiKeyHelper")
		case "forceOrg":
			b.ForceOrg = p.str(v, "auth.forceOrg")
		}
	}
	return b
}

func (p *settingsParser) telemetry(o map[string]any) *TelemetryBlock {
	b := &TelemetryBlock{}
	for _, k := range sortedKeys(o) {
		v := o[k]
		if v == nil {
			continue
		}
		switch k {
		case "enabled":
			b.Enabled = p.boolean(v, "telemetry.enabled")
		case "endpoint":
			b.Endpoint = p.str(v, "telemetry.endpoint")
		}
	}
	return b
}

func (p *settingsParser) update(o map[string]any) *UpdateBlock {
	b := &UpdateBlock{}
	for _, k := range sortedKeys(o) {
		v := o[k]
		if v == nil {
			continue
		}
		switch k {
		case "channel":
			b.Channel = p.str(v, "update.channel")
		case "autoUpdate":
			b.AutoUpdate = p.boolean(v, "update.autoUpdate")
		}
	}
	return b
}

// applyLayer merges one parsed layer into r. It returns an error only for the
// flags scope (an invalid --settings value is fatal); every other scope
// degrades to warnings.
func applyLayer(r *Resolved, ly layer, tr *sourceTracker, warnings *[]string) error {
	s := ly.s
	if s == nil {
		return nil
	}
	// managedOnly warns and reports true when key must be ignored at ly.scope.
	managedOnly := func(key string) bool {
		if ly.scope == ScopeManaged {
			return false
		}
		*warnings = append(*warnings, fmt.Sprintf(
			"%s: %q is a managed-only setting; ignored at %s scope", ly.file, key, ly.scope))
		return true
	}

	if s.Org != nil {
		r.Org = *s.Org
		tr.setScalar("org", r.Org, ly.scope, ly.file)
	}
	if s.BaseURL != nil {
		r.BaseURL = *s.BaseURL
		tr.setScalar("baseUrl", r.BaseURL, ly.scope, ly.file)
	}
	if s.Output != nil {
		switch {
		case validOutputs[*s.Output]:
			r.Output = *s.Output
			tr.setScalar("output", r.Output, ly.scope, ly.file)
		case ly.scope == ScopeFlags:
			return fmt.Errorf("%s: invalid output %q (valid: table, json, csv, ndjson)", ly.file, *s.Output)
		default:
			*warnings = append(*warnings, fmt.Sprintf(
				"%s: invalid output %q (valid: table, json, csv, ndjson); entry ignored", ly.file, *s.Output))
		}
	}

	if s.Permissions != nil {
		pb := s.Permissions
		if pb.DefaultMode != nil {
			if validDefaultModes[*pb.DefaultMode] {
				r.PermissionDefaultMode = *pb.DefaultMode
				tr.setScalar("permissions.defaultMode", r.PermissionDefaultMode, ly.scope, ly.file)
			} else {
				*warnings = append(*warnings, fmt.Sprintf(
					"%s: invalid permissions.defaultMode %q (valid: allow, ask, deny); entry ignored", ly.file, *pb.DefaultMode))
			}
		}
		appendRules(&r.Allow, pb.Allow, "permissions.allow", ly, tr)
		appendRules(&r.Ask, pb.Ask, "permissions.ask", ly, tr)
		appendRules(&r.Deny, pb.Deny, "permissions.deny", ly, tr)
		if pb.AllowManagedRulesOnly != nil && !managedOnly("permissions.allowManagedRulesOnly") {
			r.AllowManagedRulesOnly = *pb.AllowManagedRulesOnly
			tr.setScalar("permissions.allowManagedRulesOnly", strconv.FormatBool(r.AllowManagedRulesOnly), ly.scope, ly.file)
		}
	}

	if s.MCP != nil {
		if s.MCP.EnableAllProjectServers != nil {
			r.MCPEnableAllProjectServers = *s.MCP.EnableAllProjectServers
			tr.setScalar("mcp.enableAllProjectServers", strconv.FormatBool(r.MCPEnableAllProjectServers), ly.scope, ly.file)
		}
		appendStrings(&r.MCPEnabledServers, s.MCP.EnabledServers, "mcp.enabledServers", ly, tr)
		appendStrings(&r.MCPDisabledServers, s.MCP.DisabledServers, "mcp.disabledServers", ly, tr)
	}

	for _, ev := range sortedKeys(s.Hooks) {
		for _, h := range s.Hooks[ev] {
			if hookExists(r.Hooks[ev], h) {
				continue // de-dup, first occurrence wins
			}
			h.Scope = ly.scope
			r.Hooks[ev] = append(r.Hooks[ev], h)
			tr.addList("hooks."+ev, hookDesc(h), ly.scope, ly.file)
		}
	}

	for _, k := range sortedKeys(s.Env) {
		r.Env[k] = s.Env[k]
		tr.setScalar("env."+k, s.Env[k], ly.scope, ly.file)
	}

	if s.Auth != nil {
		if s.Auth.LoginMethod != nil {
			if validLoginMethods[*s.Auth.LoginMethod] {
				r.AuthLoginMethod = *s.Auth.LoginMethod
				tr.setScalar("auth.loginMethod", r.AuthLoginMethod, ly.scope, ly.file)
			} else {
				*warnings = append(*warnings, fmt.Sprintf(
					"%s: invalid auth.loginMethod %q (valid: token, oauth); entry ignored", ly.file, *s.Auth.LoginMethod))
			}
		}
		if s.Auth.APIKeyHelper != nil {
			switch {
			case ly.scope == ScopeProject && !ly.trusted:
				*warnings = append(*warnings, fmt.Sprintf(
					"%s: auth.apiKeyHelper ignored from untrusted project settings; run `tallyfy trust` to allow project-scoped helpers", ly.file))
			case ly.scope == ScopeFlags:
				*warnings = append(*warnings, fmt.Sprintf(
					"%s: auth.apiKeyHelper is honored from user, local, trusted-project, and managed scopes only; ignored", ly.file))
			default:
				r.APIKeyHelper = *s.Auth.APIKeyHelper
				r.APIKeyHelperFrom = ly.scope
				tr.setScalar("auth.apiKeyHelper", r.APIKeyHelper, ly.scope, ly.file)
			}
		}
		if s.Auth.ForceOrg != nil && !managedOnly("auth.forceOrg") {
			r.ForceOrg = *s.Auth.ForceOrg
			tr.setScalar("auth.forceOrg", r.ForceOrg, ly.scope, ly.file)
		}
	}

	if s.Telemetry != nil {
		if s.Telemetry.Enabled != nil {
			r.TelemetryEnabled = *s.Telemetry.Enabled
			tr.setScalar("telemetry.enabled", strconv.FormatBool(r.TelemetryEnabled), ly.scope, ly.file)
		}
		if s.Telemetry.Endpoint != nil {
			r.TelemetryEndpoint = *s.Telemetry.Endpoint
			tr.setScalar("telemetry.endpoint", r.TelemetryEndpoint, ly.scope, ly.file)
		}
	}

	if s.Update != nil {
		if s.Update.Channel != nil {
			if validChannels[*s.Update.Channel] {
				r.UpdateChannel = *s.Update.Channel
				tr.setScalar("update.channel", r.UpdateChannel, ly.scope, ly.file)
			} else {
				*warnings = append(*warnings, fmt.Sprintf(
					"%s: invalid update.channel %q (valid: stable, latest); entry ignored", ly.file, *s.Update.Channel))
			}
		}
		if s.Update.AutoUpdate != nil {
			r.UpdateAutoUpdate = *s.Update.AutoUpdate
			tr.setScalar("update.autoUpdate", strconv.FormatBool(r.UpdateAutoUpdate), ly.scope, ly.file)
		}
	}

	if s.RequiredMinimumVersion != nil && !managedOnly("requiredMinimumVersion") {
		r.RequiredMinimumVersion = *s.RequiredMinimumVersion
		tr.setScalar("requiredMinimumVersion", r.RequiredMinimumVersion, ly.scope, ly.file)
	}
	if s.RequiredMaximumVersion != nil && !managedOnly("requiredMaximumVersion") {
		r.RequiredMaximumVersion = *s.RequiredMaximumVersion
		tr.setScalar("requiredMaximumVersion", r.RequiredMaximumVersion, ly.scope, ly.file)
	}
	if len(s.AllowedMcpServers) > 0 && !managedOnly("allowedMcpServers") {
		appendStrings(&r.AllowedMcpServers, s.AllowedMcpServers, "allowedMcpServers", ly, tr)
	}
	if len(s.DeniedMcpServers) > 0 && !managedOnly("deniedMcpServers") {
		appendStrings(&r.DeniedMcpServers, s.DeniedMcpServers, "deniedMcpServers", ly, tr)
	}
	return nil
}

// appendRules merges permission rules: append in scope order, de-dup on the
// raw rule text preserving the first occurrence (and its scope).
func appendRules(dst *[]Rule, raws []string, key string, ly layer, tr *sourceTracker) {
	for _, raw := range raws {
		dup := false
		for _, ex := range *dst {
			if ex.Raw == raw {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		*dst = append(*dst, Rule{Raw: raw, Scope: ly.scope})
		tr.addList(key, raw, ly.scope, ly.file)
	}
}

// appendStrings merges list values: append in scope order, de-dup preserving
// the first occurrence.
func appendStrings(dst *[]string, in []string, key string, ly layer, tr *sourceTracker) {
	for _, v := range in {
		dup := false
		for _, ex := range *dst {
			if ex == v {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		*dst = append(*dst, v)
		tr.addList(key, v, ly.scope, ly.file)
	}
}

func hookExists(existing []Hook, h Hook) bool {
	for _, ex := range existing {
		if ex.Matcher == h.Matcher && ex.Command == h.Command && ex.Type == h.Type && ex.URL == h.URL {
			return true
		}
	}
	return false
}

func hookDesc(h Hook) string {
	switch {
	case h.Command != "":
		return h.Command
	case h.URL != "":
		return h.URL
	default:
		return "(hook)"
	}
}

// sourceTracker accumulates SourceInfo provenance during a Load: one winning
// entry per scalar key, one entry per contributed list element.
type sourceTracker struct {
	scalars map[string]SourceInfo
	lists   []SourceInfo
}

func newSourceTracker() *sourceTracker {
	return &sourceTracker{scalars: map[string]SourceInfo{}}
}

func (t *sourceTracker) setScalar(key, value string, scope Scope, file string) {
	t.scalars[key] = SourceInfo{Key: key, Value: value, Scope: scope, File: file}
}

func (t *sourceTracker) addList(key, value string, scope Scope, file string) {
	t.lists = append(t.lists, SourceInfo{Key: key, Value: value, Scope: scope, File: file})
}

// finalize returns all provenance entries sorted by key (stable, so list
// contributions keep their merge order within a key).
func (t *sourceTracker) finalize() []SourceInfo {
	out := make([]SourceInfo, 0, len(t.scalars)+len(t.lists))
	for _, k := range sortedKeys(t.scalars) {
		out = append(out, t.scalars[k])
	}
	out = append(out, t.lists...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// seedDefaultSources records provenance for the built-in defaults that carry
// a meaningful value.
func seedDefaultSources(t *sourceTracker, d *Resolved) {
	const builtin = "(built-in)"
	t.setScalar("baseUrl", d.BaseURL, ScopeDefault, builtin)
	t.setScalar("output", d.Output, ScopeDefault, builtin)
	t.setScalar("permissions.defaultMode", d.PermissionDefaultMode, ScopeDefault, builtin)
	t.setScalar("permissions.allowManagedRulesOnly", strconv.FormatBool(d.AllowManagedRulesOnly), ScopeDefault, builtin)
	t.setScalar("mcp.enableAllProjectServers", strconv.FormatBool(d.MCPEnableAllProjectServers), ScopeDefault, builtin)
	t.setScalar("auth.loginMethod", d.AuthLoginMethod, ScopeDefault, builtin)
	t.setScalar("telemetry.enabled", strconv.FormatBool(d.TelemetryEnabled), ScopeDefault, builtin)
	t.setScalar("update.channel", d.UpdateChannel, ScopeDefault, builtin)
	t.setScalar("update.autoUpdate", strconv.FormatBool(d.UpdateAutoUpdate), ScopeDefault, builtin)
}

// sortedKeys returns m's keys in sorted order for deterministic iteration
// (warnings, source entries, and merge order must not depend on map order).
func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
