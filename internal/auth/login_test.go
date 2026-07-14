package auth

import (
	"runtime"
	"testing"
)

func TestLoginURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"production api", "https://api.tallyfy.com", productionLoginURL},
		{"production api trailing slash", "https://api.tallyfy.com/", productionLoginURL},
		{"staging go host with /api path", "https://staging.go.tallyfy.com/api", stagingLoginURL},
		{"staging-api host", "https://staging-api.tallyfy.com", stagingLoginURL},
		{"staging-api on tallyfy.net", "https://staging-api.tallyfy.net", stagingLoginURL},
		{"other staging subdomain", "https://staging.tallyfy.com", stagingLoginURL},
		{"argo production zone", "https://api.tallyfy.net", productionLoginURL},
		{"empty defaults to production", "", productionLoginURL},
		{"garbage defaults to production", "not a url", productionLoginURL},
		{"schemeless production", "api.tallyfy.com", productionLoginURL},
		{"schemeless staging", "staging-api.tallyfy.com", stagingLoginURL},
		{"localhost defaults to production", "http://localhost:8080", productionLoginURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LoginURL(tc.in); got != tc.want {
				t.Errorf("LoginURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestOpenBrowserErrorPath empties PATH so the platform opener binary cannot
// be found: OpenBrowser must return an error instead of hanging or panicking
// (and must NOT open a real browser during tests).
func TestOpenBrowserErrorPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH-based lookup differs on windows")
	}
	t.Setenv("PATH", t.TempDir())
	if err := OpenBrowser("https://example.invalid"); err == nil {
		t.Error("expected error when the opener binary is unavailable")
	}
}
