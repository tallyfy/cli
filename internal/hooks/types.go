// Package hooks fires user-supplied commands at CLI lifecycle events
// (spec §6.7).
//
// Protocol (exec hooks): the CLI writes a JSON event to the hook's stdin.
// Exit 0 = proceed. Non-zero on a Pre* event = block the command (CLI exit 8),
// surfacing the hook's stderr. Post*/SessionStart/ConfigChange/Error hooks are
// advisory: non-zero logs a warning. HTTP hooks POST the same JSON; a non-2xx
// blocks Pre* events. HTTP hook URLs must pass the allowlist.
//
// Security: hooks execute only from trusted scopes (user/local/managed) unless
// the workspace has been explicitly trusted via `tallyfy trust` - a committed
// .tallyfy/settings.json from a cloned repo must never execute code.
//
// THIS FILE IS THE FROZEN CONTRACT consumed by internal/cli.
package hooks

import (
	"time"

	"github.com/tallyfy/cli/internal/config"
)

// Event names, mirroring spec §6.7.
const (
	PreLaunch    = "PreLaunch"
	PostLaunch   = "PostLaunch"
	PreComplete  = "PreComplete"
	PostComplete = "PostComplete"
	PreArchive   = "PreArchive"
	PostArchive  = "PostArchive"
	PreDelete    = "PreDelete"
	PostDelete   = "PostDelete"
	PreImport    = "PreImport"
	PostImport   = "PostImport"
	SessionStart = "SessionStart"
	ConfigChange = "ConfigChange"
	ErrorEvent   = "Error"
)

// Events lists every valid event name (for doctor validation).
var Events = []string{
	PreLaunch, PostLaunch, PreComplete, PostComplete, PreArchive, PostArchive,
	PreDelete, PostDelete, PreImport, PostImport, SessionStart, ConfigChange, ErrorEvent,
}

// Payload is the JSON written to hook stdin / POSTed to HTTP hooks.
type Payload struct {
	Event     string         `json:"event"`
	Resource  string         `json:"resource,omitempty"` // lowercase resource, e.g. "blueprint"
	ID        string         `json:"id,omitempty"`
	Org       string         `json:"org,omitempty"`
	Args      map[string]any `json:"args,omitempty"`
	User      string         `json:"user,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

// BlockError is returned when a Pre* hook blocks the command (CLI exit 8).
type BlockError struct {
	HookDesc string // human description of the hook (command or URL)
	Stderr   string // captured stderr (exec) or response summary (http)
}

func (e *BlockError) Error() string {
	if e.Stderr != "" {
		return "blocked by hook " + e.HookDesc + ": " + e.Stderr
	}
	return "blocked by hook " + e.HookDesc
}

// Options configures a Runner.
type Options struct {
	// Env vars injected into exec hooks (from settings `env` + os.Environ()).
	Env map[string]string
	// AllowedHTTPHosts is the allowlist for HTTP hook URLs (exact host or
	// "*.suffix" patterns). Empty = HTTP hooks disabled with a warning.
	AllowedHTTPHosts []string
	// WorkspaceTrusted reports whether the current project dir was trusted
	// via `tallyfy trust` (enables project-scope hooks).
	WorkspaceTrusted bool
	// Timeout per hook execution (default 30s).
	Timeout time.Duration
	// Matcher grammar evaluation is shared with the permissions package.
	// The Runner filters hooks whose Matcher does not match the current
	// Resource(verb) token.
	MatchToken func(matcher string) (bool, error)
}

// Runner fires hooks. Implemented in this package; consumed by internal/cli.
type Runner interface {
	// Fire runs every hook registered for event whose matcher matches.
	// Returns *BlockError when a Pre* hook blocks. Advisory failures are
	// returned in warnings, never as errors.
	Fire(event string, hooks []config.Hook, payload Payload) (warnings []string, err error)
}
