package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestGet(t *testing.T) {
	e := setupTestEnv(t)
	e.writeUser(t, `{
		"org": "org_get",
		"baseUrl": "https://get.example",
		"output": "json",
		"permissions": {"defaultMode": "deny", "allow": ["Blueprint(list)"]},
		"telemetry": {"enabled": true},
		"env": {"FOO": "bar"}
	}`)
	r := mustLoad(t, Overrides{})

	cases := []struct {
		key  string
		want string
	}{
		{"org", "org_get"},
		{"baseUrl", "https://get.example"},
		{"output", "json"},
		{"permissions.defaultMode", "deny"},
		{"permissions.allow", `["Blueprint(list)"]`},
		{"permissions.ask", `[]`},
		{"telemetry.enabled", "true"},
		{"update.channel", "stable"},
		{"update.autoUpdate", "true"},
		{"auth.loginMethod", "token"},
		{"env.FOO", "bar"},
		{"mcp.enabledServers", `[]`},
	}
	for _, tc := range cases {
		got, ok := Get(r, tc.key)
		if !ok {
			t.Errorf("Get(%q) not found", tc.key)
			continue
		}
		if got != tc.want {
			t.Errorf("Get(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}

	for _, unknown := range []string{"nope", "auth.nope", "env.MISSING", "env."} {
		if _, ok := Get(r, unknown); ok {
			t.Errorf("Get(%q) = found, want not found", unknown)
		}
	}
}

func TestListEffectiveOrderedWithScopes(t *testing.T) {
	e := setupTestEnv(t)
	e.writeUser(t, `{"org":"org_user","permissions":{"allow":["A(x)"]}}`)
	e.writeProject(t, `{"org":"org_project","permissions":{"allow":["B(y)"]}}`)

	r := mustLoad(t, Overrides{})
	entries := ListEffective(r)
	if len(entries) == 0 {
		t.Fatal("ListEffective returned nothing")
	}
	if !sort.SliceIsSorted(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key }) {
		t.Error("ListEffective entries not ordered by key")
	}

	var orgRows, allowRows []EffectiveEntry
	for _, en := range entries {
		switch en.Key {
		case "org":
			orgRows = append(orgRows, en)
		case "permissions.allow":
			allowRows = append(allowRows, en)
		}
	}
	if len(orgRows) != 1 || orgRows[0].Value != "org_project" || orgRows[0].Scope != ScopeProject {
		t.Errorf("org rows = %+v, want single winning project row", orgRows)
	}
	if len(allowRows) != 2 || allowRows[0].Value != "A(x)" || allowRows[0].Scope != ScopeUser ||
		allowRows[1].Value != "B(y)" || allowRows[1].Scope != ScopeProject {
		t.Errorf("permissions.allow rows = %+v, want both contributions in merge order", allowRows)
	}
}

func TestSetKeyRoundTrip(t *testing.T) {
	e := setupTestEnv(t)

	if err := SetKey("user", "", "org", "org_set"); err != nil {
		t.Fatalf("SetKey org: %v", err)
	}
	if err := SetKey("user", "", "permissions.defaultMode", "deny"); err != nil {
		t.Fatalf("SetKey permissions.defaultMode: %v", err)
	}
	if err := SetKey("user", "", "telemetry.enabled", "true"); err != nil {
		t.Fatalf("SetKey telemetry.enabled: %v", err)
	}
	if err := SetKey("user", "", "env.FOO", "bar"); err != nil {
		t.Fatalf("SetKey env.FOO: %v", err)
	}

	r := mustLoad(t, Overrides{})
	if r.Org != "org_set" {
		t.Errorf("Org = %q, want org_set", r.Org)
	}
	if r.PermissionDefaultMode != "deny" {
		t.Errorf("PermissionDefaultMode = %q, want deny", r.PermissionDefaultMode)
	}
	if !r.TelemetryEnabled {
		t.Error("TelemetryEnabled = false, want true")
	}
	if r.Env["FOO"] != "bar" {
		t.Errorf("Env[FOO] = %q, want bar", r.Env["FOO"])
	}

	// The file itself must hold real JSON types, not strings.
	data, err := os.ReadFile(filepath.Join(e.home, ".tallyfy", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("settings not valid JSON: %v", err)
	}
	tel, _ := m["telemetry"].(map[string]any)
	if v, ok := tel["enabled"].(bool); !ok || !v {
		t.Errorf("telemetry.enabled in file = %#v, want JSON true", tel["enabled"])
	}
}

func TestSetKeyListValues(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"json array", `["Blueprint(list)","Process(list)"]`},
		{"comma separated", `Blueprint(list), Process(list)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupTestEnv(t)
			if err := SetKey("user", "", "permissions.allow", tc.value); err != nil {
				t.Fatalf("SetKey: %v", err)
			}
			r := mustLoad(t, Overrides{})
			if len(r.Allow) != 2 || r.Allow[0].Raw != "Blueprint(list)" || r.Allow[1].Raw != "Process(list)" {
				t.Errorf("Allow = %+v, want the two rules", r.Allow)
			}
		})
	}
}

func TestSetKeyProjectAndLocalScopes(t *testing.T) {
	e := setupTestEnv(t)
	if err := SetKey("project", e.project, "org", "org_project"); err != nil {
		t.Fatalf("SetKey project: %v", err)
	}
	if err := SetKey("local", e.project, "org", "org_local"); err != nil {
		t.Fatalf("SetKey local: %v", err)
	}
	for _, f := range []string{"settings.json", "settings.local.json"} {
		if _, err := os.Stat(filepath.Join(e.project, ".tallyfy", f)); err != nil {
			t.Errorf("expected %s to exist: %v", f, err)
		}
	}
	r := mustLoad(t, Overrides{})
	if r.Org != "org_local" {
		t.Errorf("Org = %q, want org_local (local beats project)", r.Org)
	}
}

func TestSetKeyErrors(t *testing.T) {
	e := setupTestEnv(t)
	cases := []struct {
		name            string
		scope, dir, key string
		value           string
	}{
		{"unknown key", "user", "", "no.such.key", "x"},
		{"managed-only key", "user", "", "auth.forceOrg", "org_x"},
		{"managed-only version bound", "user", "", "requiredMinimumVersion", "1.0.0"},
		{"bad bool", "user", "", "telemetry.enabled", "maybe"},
		{"bad enum output", "user", "", "output", "xml"},
		{"bad enum channel", "user", "", "update.channel", "nightly"},
		{"project scope without dir", "project", "", "org", "x"},
		{"local scope without dir", "local", "", "org", "x"},
		{"unknown scope", "corporate", "", "org", "x"},
		{"empty env name", "user", "", "env.", "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := SetKey(tc.scope, tc.dir, tc.key, tc.value); err == nil {
				t.Errorf("SetKey(%s, %q, %q, %q) succeeded, want error", tc.scope, tc.dir, tc.key, tc.value)
			}
		})
	}

	t.Run("corrupt existing file refuses write", func(t *testing.T) {
		p := e.writeUser(t, `{ broken`)
		if err := SetKey("user", "", "org", "x"); err == nil {
			t.Error("SetKey over corrupt file succeeded, want error")
		}
		data, _ := os.ReadFile(p)
		if string(data) != `{ broken` {
			t.Error("corrupt file was modified by failed SetKey")
		}
	})

	t.Run("intermediate non-object refuses write", func(t *testing.T) {
		setupTestEnv(t) // fresh env; previous subtest corrupted the user file
		if err := SetKey("user", "", "org", "x"); err != nil {
			t.Fatalf("seed SetKey: %v", err)
		}
		if err := SetKey("user", "", "org.sub", "x"); err == nil {
			t.Error(`SetKey("org.sub") over scalar "org" succeeded, want type-conflict error`)
		}
	})
}

func TestSetKeyPreservesUnknownKeys(t *testing.T) {
	e := setupTestEnv(t)
	e.writeUser(t, `{"customFutureKey":{"a":1},"org":"org_old"}`)
	if err := SetKey("user", "", "org", "org_new"); err != nil {
		t.Fatalf("SetKey: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(e.home, ".tallyfy", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["org"] != "org_new" {
		t.Errorf("org = %v, want org_new", m["org"])
	}
	if _, ok := m["customFutureKey"].(map[string]any); !ok {
		t.Errorf("customFutureKey lost on rewrite: %v", m)
	}
}

func TestSetKeyBackupRotationKeepsFiveNewest(t *testing.T) {
	e := setupTestEnv(t)
	p := filepath.Join(e.home, ".tallyfy", "settings.json")

	// First write: file absent, so no backup is taken.
	if err := SetKey("user", "", "org", "v0"); err != nil {
		t.Fatalf("SetKey: %v", err)
	}
	if got := numericBackups(t, p); len(got) != 0 {
		t.Fatalf("backups after first write = %v, want none", got)
	}

	// Seed 5 old backups plus one non-timestamp file that must survive.
	for _, ts := range []int64{1000, 2000, 3000, 4000, 5000} {
		writeFileT(t, fmt.Sprintf("%s.bak.%d", p, ts), "old")
	}
	writeFileT(t, p+".bak.notatimestamp", "keep me")

	if err := SetKey("user", "", "org", "v1"); err != nil {
		t.Fatalf("SetKey: %v", err)
	}

	got := numericBackups(t, p)
	if len(got) != 5 {
		t.Fatalf("numeric backups = %v, want exactly 5", got)
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	// Oldest seeded (1000) must be rotated out; 2000..5000 plus the fresh one remain.
	want := []int64{2000, 3000, 4000, 5000}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("backups[%d] = %d, want %d (full set %v)", i, got[i], w, got)
		}
	}
	if got[4] <= 5000 {
		t.Errorf("newest backup ts = %d, want a fresh unix timestamp", got[4])
	}
	if _, err := os.Stat(p + ".bak.notatimestamp"); err != nil {
		t.Errorf("non-timestamp backup was deleted: %v", err)
	}

	// The fresh backup holds the pre-write content (org v0).
	fresh := fmt.Sprintf("%s.bak.%d", p, got[4])
	data, err := os.ReadFile(fresh)
	if err != nil {
		t.Fatalf("read fresh backup: %v", err)
	}
	if !strings.Contains(string(data), "v0") {
		t.Errorf("fresh backup content = %s, want the previous file (org v0)", data)
	}
}

func numericBackups(t *testing.T, path string) []int64 {
	t.Helper()
	matches, err := filepath.Glob(path + ".bak.*")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var out []int64
	for _, m := range matches {
		if ts, err := strconv.ParseInt(strings.TrimPrefix(m, path+".bak."), 10, 64); err == nil {
			out = append(out, ts)
		}
	}
	return out
}
