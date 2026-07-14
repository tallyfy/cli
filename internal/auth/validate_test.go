package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tallyfy/cli/pkg/tallyfy"
)

// clientImplementsMe reports whether lane L2's client implementation has
// landed. While it has not, the live-call tests are skipped: the unit gate
// for this package must not depend on a working API client (the integration
// stage covers the wired-up path).
func clientImplementsMe() bool {
	c := tallyfy.New(tallyfy.Options{})
	switch any(c).(type) {
	case interface {
		Me(context.Context) (*tallyfy.Me, error)
	}:
		return true
	case interface {
		Me(context.Context) (tallyfy.Me, error)
	}:
		return true
	default:
		return false
	}
}

func TestValidateToken(t *testing.T) {
	if !clientImplementsMe() {
		t.Skip("pkg/tallyfy Me not implemented yet (lane L2 pending); ValidateToken live path is covered by the integration stage")
	}

	var gotAuth, gotClientHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/me") {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		gotClientHeader = r.Header.Get("X-Tallyfy-Client")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"id":         12345,
				"email":      "amit@tallyfy.com",
				"username":   "amit",
				"first_name": "Amit",
				"last_name":  "Kothari",
				"timezone":   "America/Chicago",
			},
		})
	}))
	defer srv.Close()

	me, err := ValidateToken(srv.URL, "test-token-abc")
	if err != nil {
		t.Fatalf("ValidateToken() error: %v", err)
	}
	if me == nil {
		t.Fatal("ValidateToken() returned nil Me")
	}
	if me.Email != "amit@tallyfy.com" {
		t.Errorf("me.Email = %q, want %q", me.Email, "amit@tallyfy.com")
	}
	if gotAuth != "Bearer test-token-abc" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer test-token-abc")
	}
	if gotClientHeader != tallyfy.ClientHeader {
		t.Errorf("X-Tallyfy-Client header = %q, want %q", gotClientHeader, tallyfy.ClientHeader)
	}
}

func TestValidateTokenRejectsBadToken(t *testing.T) {
	if !clientImplementsMe() {
		t.Skip("pkg/tallyfy Me not implemented yet (lane L2 pending); ValidateToken live path is covered by the integration stage")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":true,"message":"Unauthenticated."}`))
	}))
	defer srv.Close()

	if _, err := ValidateToken(srv.URL, "bad-token"); err == nil {
		t.Fatal("ValidateToken() with a 401 response must return an error")
	}
}
