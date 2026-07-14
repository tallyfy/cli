// Package config implements the Tallyfy CLI settings system: six scopes with
// strict precedence, where scalar keys override by precedence and list keys
// (permission rules, hooks, MCP server enablement) merge across scopes.
//
// Scope precedence, highest wins:
//
//	Managed > CLI flags > Local (.tallyfy/settings.local.json) >
//	Project (.tallyfy/settings.json) > User (~/.tallyfy/settings.json) > Defaults
//
// Managed settings live in /etc/tallyfy/ (Linux/macOS) or %PROGRAMDATA%\Tallyfy\
// (Windows): managed-settings.json plus managed-settings.d/*.json drop-ins merged
// alphabetically. Managed deny rules and managed-only keys cannot be overridden
// by any other scope. Managed files parse tolerantly: an invalid entry is
// stripped with a warning and the rest of the policy is still enforced.
//
// THIS FILE IS THE FROZEN CONTRACT consumed by other packages. Implementation
// lives in the sibling files of this package; do not change exported shapes
// here without updating all consumers.
package config

// Scope identifies where a setting came from. Higher values take precedence.
type Scope int

const (
	ScopeDefault Scope = iota
	ScopeUser
	ScopeProject
	ScopeLocal
	ScopeFlags
	ScopeManaged
)

// String returns the human name used by `tallyfy config --show-sources`.
func (s Scope) String() string {
	switch s {
	case ScopeDefault:
		return "default"
	case ScopeUser:
		return "user"
	case ScopeProject:
		return "project"
	case ScopeLocal:
		return "local"
	case ScopeFlags:
		return "flags"
	case ScopeManaged:
		return "managed"
	}
	return "unknown"
}

// Settings is the raw JSON shape of a settings file at any scope. Pointer
// fields distinguish "absent" from "zero" so the merge is precise.
type Settings struct {
	Schema      string            `json:"$schema,omitempty"`
	Org         *string           `json:"org,omitempty"`
	BaseURL     *string           `json:"baseUrl,omitempty"`
	Output      *string           `json:"output,omitempty"`
	Permissions *PermissionsBlock `json:"permissions,omitempty"`
	MCP         *MCPBlock         `json:"mcp,omitempty"`
	Hooks       map[string][]Hook `json:"hooks,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Auth        *AuthBlock        `json:"auth,omitempty"`
	Telemetry   *TelemetryBlock   `json:"telemetry,omitempty"`
	Update      *UpdateBlock      `json:"update,omitempty"`
	// Managed-scope-only keys; ignored (with a warning) elsewhere.
	RequiredMinimumVersion *string  `json:"requiredMinimumVersion,omitempty"`
	RequiredMaximumVersion *string  `json:"requiredMaximumVersion,omitempty"`
	AllowedMcpServers      []string `json:"allowedMcpServers,omitempty"`
	DeniedMcpServers       []string `json:"deniedMcpServers,omitempty"`
}

// PermissionsBlock holds Resource(verb) rule lists. Allow/Ask/Deny MERGE
// across scopes; DefaultMode and AllowManagedRulesOnly are scalars.
type PermissionsBlock struct {
	DefaultMode           *string  `json:"defaultMode,omitempty"` // "allow" | "ask" | "deny"
	Allow                 []string `json:"allow,omitempty"`
	Ask                   []string `json:"ask,omitempty"`
	Deny                  []string `json:"deny,omitempty"`
	AllowManagedRulesOnly *bool    `json:"allowManagedRulesOnly,omitempty"` // managed scope only
}

// MCPBlock controls which project MCP servers are active.
// EnabledServers/DisabledServers MERGE across scopes.
type MCPBlock struct {
	EnableAllProjectServers *bool    `json:"enableAllProjectServers,omitempty"`
	EnabledServers          []string `json:"enabledServers,omitempty"`
	DisabledServers         []string `json:"disabledServers,omitempty"`
}

// Hook is one lifecycle hook definition. Type is "exec" (default) or "http".
type Hook struct {
	Matcher string `json:"matcher,omitempty"` // Resource(verb) grammar; empty = match all
	Command string `json:"command,omitempty"` // exec hooks
	Type    string `json:"type,omitempty"`    // "exec" | "http"
	URL     string `json:"url,omitempty"`     // http hooks
	// Scope is stamped by the loader (not part of the file format) so the
	// hooks runner can enforce trusted-scope execution rules.
	Scope Scope `json:"-"`
}

// AuthBlock configures authentication behavior.
type AuthBlock struct {
	LoginMethod  *string `json:"loginMethod,omitempty"`  // "token" (v1); "oauth" reserved
	APIKeyHelper *string `json:"apiKeyHelper,omitempty"` // honored from user/local/managed scopes ONLY
	ForceOrg     *string `json:"forceOrg,omitempty"`     // managed scope only
}

// TelemetryBlock configures the (default-off) telemetry emitter.
type TelemetryBlock struct {
	Enabled  *bool   `json:"enabled,omitempty"`
	Endpoint *string `json:"endpoint,omitempty"`
}

// UpdateBlock configures self-update behavior.
type UpdateBlock struct {
	Channel    *string `json:"channel,omitempty"` // "stable" | "latest"
	AutoUpdate *bool   `json:"autoUpdate,omitempty"`
}

// SourceInfo records which scope and file supplied one resolved key, for
// `tallyfy config --show-sources` and doctor diagnostics.
type SourceInfo struct {
	Key   string
	Value string // rendered value (secrets never pass through settings)
	Scope Scope
	File  string // absolute path, or "(built-in)" / "(flag)"
}

// Rule is a permission rule tagged with the scope that contributed it.
type Rule struct {
	Raw   string
	Scope Scope
}

// Resolved is the fully merged, concrete configuration for one invocation.
type Resolved struct {
	Org     string
	BaseURL string
	Output  string

	PermissionDefaultMode string // "allow" | "ask" | "deny" (default "ask")
	Allow, Ask, Deny      []Rule
	AllowManagedRulesOnly bool

	MCPEnableAllProjectServers bool
	MCPEnabledServers          []string
	MCPDisabledServers         []string
	AllowedMcpServers          []string // managed allowlist (empty = no restriction)
	DeniedMcpServers           []string // managed denylist

	Hooks map[string][]Hook // event name -> hooks (scope-stamped, all scopes merged)

	Env map[string]string // injected into hook/helper subprocesses

	AuthLoginMethod  string
	APIKeyHelper     string // path; empty if unset. Loader guarantees trusted-scope origin.
	APIKeyHelperFrom Scope
	ForceOrg         string // managed only; overrides Org when set

	TelemetryEnabled  bool
	TelemetryEndpoint string

	UpdateChannel    string // "stable" | "latest"
	UpdateAutoUpdate bool

	RequiredMinimumVersion string
	RequiredMaximumVersion string

	// Sources lists provenance for every resolved key (for --show-sources).
	Sources []SourceInfo
	// Warnings collected during load (invalid managed entries, ignored keys,
	// unreadable files). Printed to stderr by the CLI once per invocation.
	Warnings []string
	// ProjectDir is the directory containing the discovered .tallyfy project
	// config ("" when none). Used for workspace trust decisions.
	ProjectDir string
}

// Overrides carries per-invocation values from CLI flags (ScopeFlags) plus
// the --settings <path|inline-json> ephemeral layer.
type Overrides struct {
	SettingsArg string // --settings: path to a JSON file OR an inline JSON object
	Org         string // --org
	Output      string // --output / -o / --json
	BaseURL     string // --base-url (also TALLYFY_BASE_URL env, handled by loader)
}

// Defaults returns the built-in default settings (lowest precedence).
func Defaults() Resolved {
	return Resolved{
		BaseURL:               "https://api.tallyfy.com",
		Output:                "table",
		PermissionDefaultMode: "ask",
		AuthLoginMethod:       "token",
		UpdateChannel:         "stable",
		UpdateAutoUpdate:      true,
		Hooks:                 map[string][]Hook{},
		Env:                   map[string]string{},
	}
}
