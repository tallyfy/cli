package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testEnv redirects every filesystem root the loader touches into a temp dir
// so tests never read or write the real ~/.tallyfy or /etc/tallyfy.
type testEnv struct {
	home    string // fake $HOME (user scope + state.json)
	managed string // fake /etc/tallyfy
	project string // project dir (contains .tallyfy once written)
	cwd     string // fake working dir, below project
}

func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir()) // macOS: /var -> /private/var
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	e := &testEnv{
		home:    filepath.Join(root, "home"),
		managed: filepath.Join(root, "managed"),
	}
	e.project = filepath.Join(e.home, "code", "proj")
	e.cwd = filepath.Join(e.project, "sub")
	for _, d := range []string{e.home, e.managed, e.cwd} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	prevHome, prevWd, prevManaged := userHomeDir, workingDir, managedDirs
	userHomeDir = func() (string, error) { return e.home, nil }
	workingDir = func() (string, error) { return e.cwd, nil }
	managedDirs = func() []string { return []string{e.managed} }
	t.Cleanup(func() {
		userHomeDir, workingDir, managedDirs = prevHome, prevWd, prevManaged
	})
	t.Setenv(EnvBaseURL, "") // neutralize any real TALLYFY_BASE_URL
	return e
}

func writeFileT(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func (e *testEnv) writeUser(t *testing.T, content string) string {
	return writeFileT(t, filepath.Join(e.home, ".tallyfy", "settings.json"), content)
}

func (e *testEnv) writeProject(t *testing.T, content string) string {
	return writeFileT(t, filepath.Join(e.project, ".tallyfy", "settings.json"), content)
}

func (e *testEnv) writeLocal(t *testing.T, content string) string {
	return writeFileT(t, filepath.Join(e.project, ".tallyfy", "settings.local.json"), content)
}

func (e *testEnv) writeManaged(t *testing.T, name, content string) string {
	return writeFileT(t, filepath.Join(e.managed, name), content)
}

func mustLoad(t *testing.T, o Overrides) *Resolved {
	t.Helper()
	r, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return r
}

func hasWarning(t *testing.T, warnings []string, substr string) bool {
	t.Helper()
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func TestScalarPrecedenceAcrossScopes(t *testing.T) {
	cases := []struct {
		name                                 string
		user, project, local, flags, managed bool
		want                                 string
	}{
		{name: "defaults only", want: "https://api.tallyfy.com"},
		{name: "user beats default", user: true, want: "https://user.example"},
		{name: "project beats user", user: true, project: true, want: "https://project.example"},
		{name: "local beats project", user: true, project: true, local: true, want: "https://local.example"},
		{name: "flags beat local", user: true, project: true, local: true, flags: true, want: "https://flags.example"},
		{name: "managed beats flags", user: true, project: true, local: true, flags: true, managed: true, want: "https://managed.example"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := setupTestEnv(t)
			if tc.user {
				e.writeUser(t, `{"baseUrl":"https://user.example"}`)
			}
			if tc.project {
				e.writeProject(t, `{"baseUrl":"https://project.example"}`)
			}
			if tc.local {
				e.writeLocal(t, `{"baseUrl":"https://local.example"}`)
			}
			o := Overrides{}
			if tc.flags {
				o.SettingsArg = `{"baseUrl":"https://flags.example"}`
			}
			if tc.managed {
				e.writeManaged(t, "managed-settings.json", `{"baseUrl":"https://managed.example"}`)
			}
			r := mustLoad(t, o)
			if r.BaseURL != tc.want {
				t.Errorf("BaseURL = %q, want %q", r.BaseURL, tc.want)
			}
		})
	}
}

func TestIndividualFlagsBeatSettingsArg(t *testing.T) {
	setupTestEnv(t)
	r := mustLoad(t, Overrides{
		SettingsArg: `{"org":"org_inline","baseUrl":"https://inline.example"}`,
		Org:         "org_flag",
		BaseURL:     "https://flag.example",
	})
	if r.Org != "org_flag" {
		t.Errorf("Org = %q, want org_flag", r.Org)
	}
	if r.BaseURL != "https://flag.example" {
		t.Errorf("BaseURL = %q, want https://flag.example", r.BaseURL)
	}
}

func TestListMergeOrderDedupeScopeStamping(t *testing.T) {
	e := setupTestEnv(t)
	e.writeUser(t, `{"permissions":{"allow":["Blueprint(list)","Process(list)"]},"mcp":{"enabledServers":["tallyfy"]}}`)
	e.writeProject(t, `{"permissions":{"allow":["Process(list)","Task(list)"]},"mcp":{"enabledServers":["tallyfy","extra"]}}`)
	e.writeManaged(t, "managed-settings.json", `{"permissions":{"allow":["Org(read)"]}}`)

	r := mustLoad(t, Overrides{})

	wantAllow := []Rule{
		{Raw: "Blueprint(list)", Scope: ScopeUser},
		{Raw: "Process(list)", Scope: ScopeUser}, // project duplicate deduped, first occurrence kept
		{Raw: "Task(list)", Scope: ScopeProject},
		{Raw: "Org(read)", Scope: ScopeManaged},
	}
	if len(r.Allow) != len(wantAllow) {
		t.Fatalf("Allow = %+v, want %d rules", r.Allow, len(wantAllow))
	}
	for i, want := range wantAllow {
		if r.Allow[i] != want {
			t.Errorf("Allow[%d] = %+v, want %+v", i, r.Allow[i], want)
		}
	}
	wantServers := []string{"tallyfy", "extra"}
	if len(r.MCPEnabledServers) != 2 || r.MCPEnabledServers[0] != wantServers[0] || r.MCPEnabledServers[1] != wantServers[1] {
		t.Errorf("MCPEnabledServers = %v, want %v", r.MCPEnabledServers, wantServers)
	}

	// Provenance: one source entry per contributed (post-dedupe) element.
	var allowSources int
	for _, si := range r.Sources {
		if si.Key == "permissions.allow" {
			allowSources++
		}
	}
	if allowSources != 4 {
		t.Errorf("permissions.allow source entries = %d, want 4", allowSources)
	}
}

func TestManagedOnlyKeysIgnoredOutsideManaged(t *testing.T) {
	e := setupTestEnv(t)
	e.writeProject(t, `{
		"requiredMinimumVersion": "1.0.0",
		"requiredMaximumVersion": "9.0.0",
		"auth": {"forceOrg": "org_evil"},
		"permissions": {"allowManagedRulesOnly": true},
		"allowedMcpServers": ["a"],
		"deniedMcpServers": ["b"]
	}`)
	r := mustLoad(t, Overrides{})

	if r.RequiredMinimumVersion != "" || r.RequiredMaximumVersion != "" {
		t.Errorf("version bounds accepted from project scope: %q / %q", r.RequiredMinimumVersion, r.RequiredMaximumVersion)
	}
	if r.ForceOrg != "" {
		t.Errorf("forceOrg accepted from project scope: %q", r.ForceOrg)
	}
	if r.AllowManagedRulesOnly {
		t.Error("allowManagedRulesOnly accepted from project scope")
	}
	if len(r.AllowedMcpServers) != 0 || len(r.DeniedMcpServers) != 0 {
		t.Errorf("managed MCP lists accepted from project scope: %v / %v", r.AllowedMcpServers, r.DeniedMcpServers)
	}
	if !hasWarning(t, r.Warnings, "managed-only") {
		t.Errorf("expected a managed-only warning, got %v", r.Warnings)
	}
}

func TestManagedOnlyKeysHonoredFromManaged(t *testing.T) {
	e := setupTestEnv(t)
	e.writeManaged(t, "managed-settings.json", `{
		"requiredMinimumVersion": "2.0.0",
		"auth": {"forceOrg": "org_corp"},
		"permissions": {"allowManagedRulesOnly": true},
		"allowedMcpServers": ["tallyfy"],
		"deniedMcpServers": ["sketchy"]
	}`)
	r := mustLoad(t, Overrides{})

	if r.RequiredMinimumVersion != "2.0.0" {
		t.Errorf("RequiredMinimumVersion = %q, want 2.0.0", r.RequiredMinimumVersion)
	}
	if r.ForceOrg != "org_corp" {
		t.Errorf("ForceOrg = %q, want org_corp", r.ForceOrg)
	}
	if !r.AllowManagedRulesOnly {
		t.Error("AllowManagedRulesOnly not honored from managed scope")
	}
	if len(r.AllowedMcpServers) != 1 || r.AllowedMcpServers[0] != "tallyfy" {
		t.Errorf("AllowedMcpServers = %v", r.AllowedMcpServers)
	}
	if len(r.DeniedMcpServers) != 1 || r.DeniedMcpServers[0] != "sketchy" {
		t.Errorf("DeniedMcpServers = %v", r.DeniedMcpServers)
	}
}

func TestAPIKeyHelperTrustGating(t *testing.T) {
	t.Run("project untrusted ignored with warning", func(t *testing.T) {
		e := setupTestEnv(t)
		e.writeProject(t, `{"auth":{"apiKeyHelper":"/bin/project-helper"}}`)
		r := mustLoad(t, Overrides{})
		if r.APIKeyHelper != "" {
			t.Errorf("APIKeyHelper = %q, want empty (untrusted project)", r.APIKeyHelper)
		}
		if !hasWarning(t, r.Warnings, "tallyfy trust") {
			t.Errorf("expected `tallyfy trust` hint in warnings, got %v", r.Warnings)
		}
	})

	t.Run("project trusted honored", func(t *testing.T) {
		e := setupTestEnv(t)
		e.writeProject(t, `{"auth":{"apiKeyHelper":"/bin/project-helper"}}`)
		if err := TrustWorkspace(e.project); err != nil {
			t.Fatalf("TrustWorkspace: %v", err)
		}
		r := mustLoad(t, Overrides{})
		if r.APIKeyHelper != "/bin/project-helper" {
			t.Errorf("APIKeyHelper = %q, want /bin/project-helper", r.APIKeyHelper)
		}
		if r.APIKeyHelperFrom != ScopeProject {
			t.Errorf("APIKeyHelperFrom = %v, want project", r.APIKeyHelperFrom)
		}
		if hasWarning(t, r.Warnings, "tallyfy trust") {
			t.Errorf("unexpected trust warning after trusting: %v", r.Warnings)
		}
	})

	t.Run("user always honored", func(t *testing.T) {
		e := setupTestEnv(t)
		e.writeUser(t, `{"auth":{"apiKeyHelper":"/bin/user-helper"}}`)
		r := mustLoad(t, Overrides{})
		if r.APIKeyHelper != "/bin/user-helper" || r.APIKeyHelperFrom != ScopeUser {
			t.Errorf("APIKeyHelper = %q from %v, want /bin/user-helper from user", r.APIKeyHelper, r.APIKeyHelperFrom)
		}
	})

	t.Run("local honored even in untrusted project", func(t *testing.T) {
		e := setupTestEnv(t)
		e.writeLocal(t, `{"auth":{"apiKeyHelper":"/bin/local-helper"}}`)
		r := mustLoad(t, Overrides{})
		if r.APIKeyHelper != "/bin/local-helper" || r.APIKeyHelperFrom != ScopeLocal {
			t.Errorf("APIKeyHelper = %q from %v, want /bin/local-helper from local", r.APIKeyHelper, r.APIKeyHelperFrom)
		}
	})

	t.Run("flags scope ignored with warning", func(t *testing.T) {
		setupTestEnv(t)
		r := mustLoad(t, Overrides{SettingsArg: `{"auth":{"apiKeyHelper":"/bin/flag-helper"}}`})
		if r.APIKeyHelper != "" {
			t.Errorf("APIKeyHelper = %q, want empty from flags scope", r.APIKeyHelper)
		}
		if !hasWarning(t, r.Warnings, "apiKeyHelper") {
			t.Errorf("expected apiKeyHelper warning, got %v", r.Warnings)
		}
	})

	t.Run("managed overrides user", func(t *testing.T) {
		e := setupTestEnv(t)
		e.writeUser(t, `{"auth":{"apiKeyHelper":"/bin/user-helper"}}`)
		e.writeManaged(t, "managed-settings.json", `{"auth":{"apiKeyHelper":"/bin/managed-helper"}}`)
		r := mustLoad(t, Overrides{})
		if r.APIKeyHelper != "/bin/managed-helper" || r.APIKeyHelperFrom != ScopeManaged {
			t.Errorf("APIKeyHelper = %q from %v, want /bin/managed-helper from managed", r.APIKeyHelper, r.APIKeyHelperFrom)
		}
	})
}

func TestInlineSettingsBeatsLocalLosesToManaged(t *testing.T) {
	e := setupTestEnv(t)
	e.writeLocal(t, `{"org":"org_local"}`)

	r := mustLoad(t, Overrides{SettingsArg: `{"org":"org_inline"}`})
	if r.Org != "org_inline" {
		t.Errorf("Org = %q, want org_inline (flags beat local)", r.Org)
	}

	e.writeManaged(t, "managed-settings.json", `{"org":"org_managed"}`)
	r = mustLoad(t, Overrides{SettingsArg: `{"org":"org_inline"}`})
	if r.Org != "org_managed" {
		t.Errorf("Org = %q, want org_managed (managed beats flags)", r.Org)
	}
}

func TestBaseURLEnv(t *testing.T) {
	t.Run("env applies when flag empty", func(t *testing.T) {
		e := setupTestEnv(t)
		e.writeLocal(t, `{"baseUrl":"https://local.example"}`)
		t.Setenv(EnvBaseURL, "https://env.example")
		r := mustLoad(t, Overrides{})
		if r.BaseURL != "https://env.example" {
			t.Errorf("BaseURL = %q, want env value beating local", r.BaseURL)
		}
		found := false
		for _, si := range r.Sources {
			if si.Key == "baseUrl" {
				found = true
				if si.Scope != ScopeFlags || !strings.Contains(si.File, EnvBaseURL) {
					t.Errorf("baseUrl source = %+v, want flags scope from env", si)
				}
			}
		}
		if !found {
			t.Error("no baseUrl source entry")
		}
	})

	t.Run("flag beats env", func(t *testing.T) {
		setupTestEnv(t)
		t.Setenv(EnvBaseURL, "https://env.example")
		r := mustLoad(t, Overrides{BaseURL: "https://flag.example"})
		if r.BaseURL != "https://flag.example" {
			t.Errorf("BaseURL = %q, want flag value", r.BaseURL)
		}
	})

	t.Run("managed beats env", func(t *testing.T) {
		e := setupTestEnv(t)
		t.Setenv(EnvBaseURL, "https://env.example")
		e.writeManaged(t, "managed-settings.json", `{"baseUrl":"https://managed.example"}`)
		r := mustLoad(t, Overrides{})
		if r.BaseURL != "https://managed.example" {
			t.Errorf("BaseURL = %q, want managed value", r.BaseURL)
		}
	})
}

func TestInvalidJSONFileSkippedWithWarning(t *testing.T) {
	e := setupTestEnv(t)
	userPath := e.writeUser(t, `{ this is not json`)
	e.writeProject(t, `{"org":"org_project"}`)

	r := mustLoad(t, Overrides{})
	if r.Org != "org_project" {
		t.Errorf("Org = %q, want org_project (valid file still applies)", r.Org)
	}
	if !hasWarning(t, r.Warnings, "invalid JSON") || !hasWarning(t, r.Warnings, userPath) {
		t.Errorf("expected invalid-JSON warning naming %s, got %v", userPath, r.Warnings)
	}
}

func TestInvalidManagedFileNeverFatal(t *testing.T) {
	e := setupTestEnv(t)
	e.writeManaged(t, "managed-settings.json", `{ broken`)
	e.writeManaged(t, filepath.Join("managed-settings.d", "10-ok.json"), `{"org":"org_dropin"}`)
	r := mustLoad(t, Overrides{})
	if r.Org != "org_dropin" {
		t.Errorf("Org = %q, want org_dropin (rest of managed policy still enforced)", r.Org)
	}
	if !hasWarning(t, r.Warnings, "invalid JSON") {
		t.Errorf("expected warning for broken managed file, got %v", r.Warnings)
	}
}

func TestInvalidSettingsFlagFatal(t *testing.T) {
	cases := []struct {
		name string
		arg  string
	}{
		{"inline bad json", `{ nope`},
		{"missing file", "/nonexistent/path/settings.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupTestEnv(t)
			if _, err := Load(Overrides{SettingsArg: tc.arg}); err == nil {
				t.Error("Load succeeded, want fatal error for invalid --settings")
			}
		})
	}

	t.Run("file with bad json", func(t *testing.T) {
		e := setupTestEnv(t)
		bad := writeFileT(t, filepath.Join(e.home, "bad-settings.json"), `{ nope`)
		if _, err := Load(Overrides{SettingsArg: bad}); err == nil {
			t.Error("Load succeeded, want fatal error for unparseable --settings file")
		}
	})

	t.Run("valid file accepted", func(t *testing.T) {
		e := setupTestEnv(t)
		good := writeFileT(t, filepath.Join(e.home, "good-settings.json"), `{"org":"org_file"}`)
		r := mustLoad(t, Overrides{SettingsArg: good})
		if r.Org != "org_file" {
			t.Errorf("Org = %q, want org_file", r.Org)
		}
	})
}

func TestOrgFallsBackToState(t *testing.T) {
	setupTestEnv(t)
	if err := SaveState(&State{CurrentOrg: "org_state"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	r := mustLoad(t, Overrides{})
	if r.Org != "org_state" {
		t.Errorf("Org = %q, want org_state fallback", r.Org)
	}
}

func TestOrgFromSettingsBeatsState(t *testing.T) {
	e := setupTestEnv(t)
	e.writeUser(t, `{"org":"org_settings"}`)
	if err := SaveState(&State{CurrentOrg: "org_state"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	r := mustLoad(t, Overrides{})
	if r.Org != "org_settings" {
		t.Errorf("Org = %q, want org_settings (state is a fallback only)", r.Org)
	}
}

func TestTypeMismatchedEntriesStripped(t *testing.T) {
	e := setupTestEnv(t)
	e.writeUser(t, `{
		"org": 123,
		"baseUrl": "https://ok.example",
		"permissions": {"allow": "not-a-list"},
		"env": {"GOOD": "1", "BAD": 7}
	}`)
	r := mustLoad(t, Overrides{})

	if r.Org != "" {
		t.Errorf("Org = %q, want empty (numeric org stripped)", r.Org)
	}
	if r.BaseURL != "https://ok.example" {
		t.Errorf("BaseURL = %q, want https://ok.example (valid siblings survive)", r.BaseURL)
	}
	if len(r.Allow) != 0 {
		t.Errorf("Allow = %v, want empty", r.Allow)
	}
	if r.Env["GOOD"] != "1" {
		t.Errorf("Env[GOOD] = %q, want 1", r.Env["GOOD"])
	}
	if _, ok := r.Env["BAD"]; ok {
		t.Error("Env[BAD] present, want stripped")
	}
	for _, key := range []string{`"org"`, `"permissions.allow"`, `"env.BAD"`} {
		if !hasWarning(t, r.Warnings, key) {
			t.Errorf("missing warning for %s entry, got %v", key, r.Warnings)
		}
	}
}

func TestOutputValidation(t *testing.T) {
	t.Run("invalid in file warns and keeps previous", func(t *testing.T) {
		e := setupTestEnv(t)
		e.writeUser(t, `{"output":"xml"}`)
		r := mustLoad(t, Overrides{})
		if r.Output != "table" {
			t.Errorf("Output = %q, want table (default kept)", r.Output)
		}
		if !hasWarning(t, r.Warnings, "invalid output") {
			t.Errorf("expected invalid output warning, got %v", r.Warnings)
		}
	})

	t.Run("valid in file applies", func(t *testing.T) {
		e := setupTestEnv(t)
		e.writeUser(t, `{"output":"ndjson"}`)
		r := mustLoad(t, Overrides{})
		if r.Output != "ndjson" {
			t.Errorf("Output = %q, want ndjson", r.Output)
		}
	})

	t.Run("invalid from output flag is fatal", func(t *testing.T) {
		setupTestEnv(t)
		if _, err := Load(Overrides{Output: "xml"}); err == nil {
			t.Error("Load succeeded, want error for invalid --output")
		}
	})

	t.Run("invalid inside --settings is fatal", func(t *testing.T) {
		setupTestEnv(t)
		if _, err := Load(Overrides{SettingsArg: `{"output":"xml"}`}); err == nil {
			t.Error("Load succeeded, want error for invalid output at flags scope")
		}
	})
}

func TestSourcesShadowedKeyShowsWinningScope(t *testing.T) {
	e := setupTestEnv(t)
	e.writeUser(t, `{"baseUrl":"https://user.example"}`)
	projPath := e.writeProject(t, `{"baseUrl":"https://project.example"}`)

	r := mustLoad(t, Overrides{})
	var entries []SourceInfo
	for _, si := range r.Sources {
		if si.Key == "baseUrl" {
			entries = append(entries, si)
		}
	}
	if len(entries) != 1 {
		t.Fatalf("baseUrl source entries = %d, want exactly 1 (the winner)", len(entries))
	}
	if entries[0].Scope != ScopeProject || entries[0].File != projPath || entries[0].Value != "https://project.example" {
		t.Errorf("baseUrl source = %+v, want project scope from %s", entries[0], projPath)
	}
}

func TestManagedDropInsMergeAlphabetically(t *testing.T) {
	t.Run("later drop-in wins scalars", func(t *testing.T) {
		e := setupTestEnv(t)
		e.writeManaged(t, "managed-settings.json", `{"org":"org_main"}`)
		e.writeManaged(t, filepath.Join("managed-settings.d", "10-first.json"), `{"org":"org_first"}`)
		e.writeManaged(t, filepath.Join("managed-settings.d", "20-second.json"), `{"org":"org_second"}`)
		r := mustLoad(t, Overrides{})
		if r.Org != "org_second" {
			t.Errorf("Org = %q, want org_second (alphabetically last drop-in)", r.Org)
		}
	})

	t.Run("drop-ins work without main file", func(t *testing.T) {
		e := setupTestEnv(t)
		e.writeManaged(t, filepath.Join("managed-settings.d", "10-only.json"), `{"org":"org_only"}`)
		r := mustLoad(t, Overrides{})
		if r.Org != "org_only" {
			t.Errorf("Org = %q, want org_only", r.Org)
		}
	})

	t.Run("non-json files in drop-in dir ignored", func(t *testing.T) {
		e := setupTestEnv(t)
		e.writeManaged(t, filepath.Join("managed-settings.d", "readme.txt"), `not json at all`)
		e.writeManaged(t, filepath.Join("managed-settings.d", "10-ok.json"), `{"org":"org_ok"}`)
		r := mustLoad(t, Overrides{})
		if r.Org != "org_ok" {
			t.Errorf("Org = %q, want org_ok", r.Org)
		}
		if hasWarning(t, r.Warnings, "readme.txt") {
			t.Errorf("readme.txt should be ignored silently, got %v", r.Warnings)
		}
	})
}

func TestEnvMapMergeHigherScopeWins(t *testing.T) {
	e := setupTestEnv(t)
	e.writeUser(t, `{"env":{"A":"1","B":"2"}}`)
	projPath := e.writeProject(t, `{"env":{"B":"3"}}`)

	r := mustLoad(t, Overrides{})
	if r.Env["A"] != "1" || r.Env["B"] != "3" {
		t.Errorf("Env = %v, want A=1 B=3", r.Env)
	}
	for _, si := range r.Sources {
		if si.Key == "env.B" && (si.Scope != ScopeProject || si.File != projPath) {
			t.Errorf("env.B source = %+v, want project scope", si)
		}
	}
}

func TestHooksMergeAndScopeStamp(t *testing.T) {
	e := setupTestEnv(t)
	e.writeUser(t, `{"hooks":{"PreLaunch":[{"command":"echo user"}]}}`)
	e.writeProject(t, `{"hooks":{"PreLaunch":[{"command":"echo project"},{"command":"echo user"}],"PostLaunch":[{"type":"http","url":"https://hooks.example/x"}]}}`)

	r := mustLoad(t, Overrides{})
	pre := r.Hooks["PreLaunch"]
	if len(pre) != 2 {
		t.Fatalf("PreLaunch hooks = %+v, want 2 (identical hook deduped)", pre)
	}
	if pre[0].Command != "echo user" || pre[0].Scope != ScopeUser {
		t.Errorf("PreLaunch[0] = %+v, want echo user from user scope", pre[0])
	}
	if pre[1].Command != "echo project" || pre[1].Scope != ScopeProject {
		t.Errorf("PreLaunch[1] = %+v, want echo project from project scope", pre[1])
	}
	post := r.Hooks["PostLaunch"]
	if len(post) != 1 || post[0].URL != "https://hooks.example/x" || post[0].Scope != ScopeProject {
		t.Errorf("PostLaunch = %+v, want one http hook from project scope", post)
	}
}

func TestProjectDiscovery(t *testing.T) {
	t.Run("walks up from cwd to nearest .tallyfy", func(t *testing.T) {
		e := setupTestEnv(t)
		e.writeProject(t, `{}`)
		r := mustLoad(t, Overrides{})
		if r.ProjectDir != e.project {
			t.Errorf("ProjectDir = %q, want %q", r.ProjectDir, e.project)
		}
	})

	t.Run("no project when no .tallyfy exists", func(t *testing.T) {
		setupTestEnv(t)
		r := mustLoad(t, Overrides{})
		if r.ProjectDir != "" {
			t.Errorf("ProjectDir = %q, want empty", r.ProjectDir)
		}
	})

	t.Run("home .tallyfy never makes home a project", func(t *testing.T) {
		e := setupTestEnv(t)
		e.writeUser(t, `{"org":"org_user"}`) // creates ~/.tallyfy
		r := mustLoad(t, Overrides{})
		if r.ProjectDir != "" {
			t.Errorf("ProjectDir = %q, want empty (walk stops at $HOME)", r.ProjectDir)
		}
		if r.Org != "org_user" {
			t.Errorf("Org = %q, want org_user via user scope", r.Org)
		}
	})

	t.Run(".tallyfy file (not dir) is not a project", func(t *testing.T) {
		e := setupTestEnv(t)
		writeFileT(t, filepath.Join(e.project, ".tallyfy"), "not a dir")
		r := mustLoad(t, Overrides{})
		if r.ProjectDir != "" {
			t.Errorf("ProjectDir = %q, want empty for a .tallyfy regular file", r.ProjectDir)
		}
	})
}

func TestNullValuesTreatedAsUnset(t *testing.T) {
	e := setupTestEnv(t)
	// Mirrors the spec's full example file where unset keys are explicit nulls.
	e.writeUser(t, `{"org":null,"auth":{"apiKeyHelper":null,"forceOrg":null},"telemetry":{"enabled":false,"endpoint":null},"requiredMinimumVersion":null}`)
	r := mustLoad(t, Overrides{})
	if len(r.Warnings) != 0 {
		t.Errorf("nulls must not warn, got %v", r.Warnings)
	}
	if r.Org != "" || r.APIKeyHelper != "" || r.RequiredMinimumVersion != "" {
		t.Errorf("null keys leaked values: org=%q helper=%q minVer=%q", r.Org, r.APIKeyHelper, r.RequiredMinimumVersion)
	}
}
