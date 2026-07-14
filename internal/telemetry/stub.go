package telemetry

import "github.com/tallyfy/cli/internal/config"

// Emit sends one event when telemetry is enabled. REPLACED BY LANE L5.
func Emit(cfg *config.Resolved, ev Event) {}
