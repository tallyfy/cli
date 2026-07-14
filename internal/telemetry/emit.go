package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/tallyfy/cli/internal/config"
)

// httpClient posts events; package variable so tests can substitute one
// pointed at an httptest server (or with custom transport/timeouts).
var httpClient = &http.Client{}

// emitTimeout is the hard budget for the entire POST (spec §6.10).
const emitTimeout = 1500 * time.Millisecond

// Emit sends one Event to the configured endpoint. Telemetry fires only when
// (telemetry.enabled OR TALLYFY_TELEMETRY=1) AND telemetry.endpoint is set.
// It never returns an error and never blocks beyond the 1500ms budget: any
// marshal, network, or HTTP failure is swallowed silently.
func Emit(cfg *config.Resolved, ev Event) {
	if cfg == nil {
		return
	}
	enabled := cfg.TelemetryEnabled || os.Getenv(EnvEnable) == "1"
	if !enabled || cfg.TelemetryEndpoint == "" {
		return
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), emitTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TelemetryEndpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
}
