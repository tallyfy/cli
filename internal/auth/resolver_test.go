package auth

import (
	"errors"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/tallyfy/cli/internal/config"
)

// newTestEnv gives every test an isolated credential world: in-memory
// keychain (never the real OS keychain), a temp home for the encrypted-file
// backend (never the real ~/.tallyfy), and a clean process-wide backend
// memo. Returns the temp home dir.
func newTestEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	prevHome := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	keyring.MockInit()
	resetBackendChoice()
	t.Cleanup(func() {
		userHomeDir = prevHome
		keyring.MockInit() // drop test secrets from the process-global mock
		resetBackendChoice()
	})
	return home
}

func testResolver() *resolver {
	return NewResolver().(*resolver)
}

func TestResolvePrecedence(t *testing.T) {
	const (
		flagTok   = "tok-from-flag"
		envTok    = "tok-from-env"
		helperTok = "tok-from-helper"
		storeTok  = "tok-from-store"
	)
	helperCfg := &config.Resolved{APIKeyHelper: "printf '" + helperTok + "'"}

	cases := []struct {
		name       string
		flag       string
		env        string
		cfg        *config.Resolved
		storeToken string
		wantToken  string
		wantSource Source
	}{
		{"flag beats env, helper and store", flagTok, envTok, helperCfg, storeTok, flagTok, SourceFlag},
		{"env beats helper and store", "", envTok, helperCfg, storeTok, envTok, SourceEnv},
		{"helper beats store", "", "", helperCfg, storeTok, helperTok, SourceHelper},
		{"store alone", "", "", &config.Resolved{}, storeTok, storeTok, SourceKeychain},
		{"flag alone", flagTok, "", &config.Resolved{}, "", flagTok, SourceFlag},
		{"env alone", "", envTok, &config.Resolved{}, "", envTok, SourceEnv},
		{"helper alone", "", "", helperCfg, "", helperTok, SourceHelper},
		{"env whitespace-only falls through to store", "", "  \n", &config.Resolved{}, storeTok, storeTok, SourceKeychain},
		{"env value is trimmed", "", "  " + envTok + "\n", &config.Resolved{}, "", envTok, SourceEnv},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newTestEnv(t)
			t.Setenv(EnvToken, tc.env)
			if tc.storeToken != "" {
				if err := NewStore().Save(tc.storeToken); err != nil {
					t.Fatalf("seeding store: %v", err)
				}
			}

			cred, err := NewResolver().Resolve(tc.cfg, tc.flag)
			if err != nil {
				t.Fatalf("Resolve() error: %v", err)
			}
			if cred.Token != tc.wantToken {
				t.Errorf("token = %q, want %q", cred.Token, tc.wantToken)
			}
			if cred.Source != tc.wantSource {
				t.Errorf("source = %q, want %q", cred.Source, tc.wantSource)
			}
		})
	}
}

func TestResolveNoCredential(t *testing.T) {
	newTestEnv(t)
	t.Setenv(EnvToken, "")

	_, err := NewResolver().Resolve(&config.Resolved{}, "")
	var target ErrNoCredential
	if !errors.As(err, &target) {
		t.Fatalf("err = %v, want ErrNoCredential", err)
	}
	if !strings.Contains(err.Error(), "tallyfy login") {
		t.Errorf("error should hint at `tallyfy login`, got: %v", err)
	}
}

func TestResolveNilConfig(t *testing.T) {
	newTestEnv(t)
	t.Setenv(EnvToken, "")

	_, err := NewResolver().Resolve(nil, "")
	var target ErrNoCredential
	if !errors.As(err, &target) {
		t.Fatalf("err = %v, want ErrNoCredential", err)
	}
}

func TestResolveHelperFailureSurfaces(t *testing.T) {
	newTestEnv(t)
	t.Setenv(EnvToken, "")

	cfg := &config.Resolved{APIKeyHelper: "echo nope >&2; exit 7"}
	_, err := NewResolver().Resolve(cfg, "")
	if err == nil {
		t.Fatal("expected error from failing helper")
	}
	if !strings.Contains(err.Error(), "apiKeyHelper failed") {
		t.Errorf("error should be prefixed 'apiKeyHelper failed', got: %v", err)
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error should carry helper stderr, got: %v", err)
	}
}

func TestResolveHelperReceivesCfgEnv(t *testing.T) {
	newTestEnv(t)
	t.Setenv(EnvToken, "")

	cfg := &config.Resolved{
		APIKeyHelper: `printf '%s' "$TLFY_TEST_HELPER_TOKEN"`,
		Env:          map[string]string{"TLFY_TEST_HELPER_TOKEN": "tok-via-env-map"},
	}
	cred, err := NewResolver().Resolve(cfg, "")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if cred.Token != "tok-via-env-map" || cred.Source != SourceHelper {
		t.Errorf("got (%q, %q), want (tok-via-env-map, helper)", cred.Token, cred.Source)
	}
}

func TestResolveStoreErrorSurfaces(t *testing.T) {
	newTestEnv(t)
	t.Setenv(EnvToken, "")
	// A keychain error that is NOT an unavailability signal must surface,
	// not silently fall back or masquerade as "no credential".
	keyring.MockInitWithError(errors.New("keychain is locked"))

	_, err := NewResolver().Resolve(&config.Resolved{}, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "reading stored credential") ||
		!strings.Contains(err.Error(), "keychain is locked") {
		t.Errorf("unexpected error: %v", err)
	}
	var target ErrNoCredential
	if errors.As(err, &target) {
		t.Error("a real store error must not be reported as ErrNoCredential")
	}
}

func TestNewResolverWiring(t *testing.T) {
	r := testResolver()
	if r.getenv == nil || r.runHelper == nil || r.newStore == nil {
		t.Fatal("NewResolver must wire all collaborators")
	}
}
