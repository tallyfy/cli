package telemetry

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/tallyfy/cli/internal/config"
)

// telemetrySink records POSTs for assertions.
type telemetrySink struct {
	srv   *httptest.Server
	count atomic.Int64
	body  atomic.Value // []byte
	ctype atomic.Value // string
}

func newSink(t *testing.T, status int) *telemetrySink {
	t.Helper()
	s := &telemetrySink{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		s.body.Store(data)
		s.ctype.Store(r.Header.Get("Content-Type"))
		s.count.Add(1)
		w.WriteHeader(status)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func clearEnableEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvEnable, "")
	_ = os.Unsetenv(EnvEnable)
}

var testEvent = Event{
	Command:    "blueprint list",
	DurationMS: 412,
	ExitCode:   0,
	Version:    "0.1.0",
	OS:         "darwin/arm64",
}

func TestEmitEnabledViaConfig(t *testing.T) {
	clearEnableEnv(t)
	sink := newSink(t, http.StatusOK)
	cfg := &config.Resolved{TelemetryEnabled: true, TelemetryEndpoint: sink.srv.URL}

	Emit(cfg, testEvent)

	if sink.count.Load() != 1 {
		t.Fatalf("expected 1 POST, got %d", sink.count.Load())
	}
	if ct := sink.ctype.Load().(string); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(sink.body.Load().([]byte), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"command":     "blueprint list",
		"duration_ms": float64(412),
		"exit_code":   float64(0),
		"version":     "0.1.0",
		"os":          "darwin/arm64",
	}
	if len(got) != len(want) {
		t.Errorf("payload must have exactly %d fields, got %d: %v", len(want), len(got), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("payload[%q] = %v, want %v", k, got[k], v)
		}
	}
}

func TestEmitEnabledViaEnv(t *testing.T) {
	sink := newSink(t, http.StatusOK)
	t.Setenv(EnvEnable, "1")
	cfg := &config.Resolved{TelemetryEnabled: false, TelemetryEndpoint: sink.srv.URL}

	Emit(cfg, testEvent)
	if sink.count.Load() != 1 {
		t.Fatalf("TALLYFY_TELEMETRY=1 should enable, got %d posts", sink.count.Load())
	}
}

func TestEmitEnvZeroDoesNotEnable(t *testing.T) {
	sink := newSink(t, http.StatusOK)
	t.Setenv(EnvEnable, "0")
	cfg := &config.Resolved{TelemetryEnabled: false, TelemetryEndpoint: sink.srv.URL}

	Emit(cfg, testEvent)
	if sink.count.Load() != 0 {
		t.Fatalf("TALLYFY_TELEMETRY=0 must stay disabled, got %d posts", sink.count.Load())
	}
}

func TestEmitDisabledByDefault(t *testing.T) {
	clearEnableEnv(t)
	sink := newSink(t, http.StatusOK)
	cfg := &config.Resolved{TelemetryEnabled: false, TelemetryEndpoint: sink.srv.URL}

	Emit(cfg, testEvent)
	if sink.count.Load() != 0 {
		t.Fatalf("default must be off, got %d posts", sink.count.Load())
	}
}

func TestEmitNoEndpointNoSend(t *testing.T) {
	clearEnableEnv(t)
	cfg := &config.Resolved{TelemetryEnabled: true, TelemetryEndpoint: ""}
	Emit(cfg, testEvent) // must not panic or block
}

func TestEmitSwallowsServerErrors(t *testing.T) {
	clearEnableEnv(t)
	sink := newSink(t, http.StatusInternalServerError)
	cfg := &config.Resolved{TelemetryEnabled: true, TelemetryEndpoint: sink.srv.URL}
	Emit(cfg, testEvent) // 500 swallowed
	if sink.count.Load() != 1 {
		t.Fatalf("expected the POST to be attempted once, got %d", sink.count.Load())
	}
}

func TestEmitSwallowsConnectionErrors(t *testing.T) {
	clearEnableEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // dead endpoint
	cfg := &config.Resolved{TelemetryEnabled: true, TelemetryEndpoint: url}
	Emit(cfg, testEvent) // must not panic or return an error (there is none)
}

func TestEmitNilConfig(t *testing.T) {
	Emit(nil, testEvent) // must not panic
}
