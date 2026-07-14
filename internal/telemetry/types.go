// Package telemetry implements a minimal, privacy-first usage emitter
// (spec §6.10). OFF by default; enabled only via telemetry.enabled=true or
// TALLYFY_TELEMETRY=1 plus an explicit endpoint.
//
// Exactly five fields are ever emitted - never tokens, org data, arguments,
// or resource content:
//
//	{"command":"blueprint list","duration_ms":412,"exit_code":0,
//	 "version":"0.1.0","os":"darwin/arm64"}
//
// The full OpenTelemetry SDK is deliberately deferred to keep the dependency
// tree lean; the emitter POSTs one JSON object per invocation with a hard
// 1500ms budget and swallows all errors.
//
// THIS FILE IS THE FROZEN CONTRACT consumed by internal/cli.
package telemetry

// EnvEnable force-enables telemetry when set to "1" (endpoint still required).
const EnvEnable = "TALLYFY_TELEMETRY"

// Event is the single record emitted per CLI invocation when enabled.
type Event struct {
	Command    string `json:"command"` // command path only, e.g. "blueprint list"
	DurationMS int64  `json:"duration_ms"`
	ExitCode   int    `json:"exit_code"`
	Version    string `json:"version"`
	OS         string `json:"os"` // GOOS/GOARCH
}
