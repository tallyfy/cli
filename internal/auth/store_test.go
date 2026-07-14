package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestStoreKeychainRoundTrip(t *testing.T) {
	newTestEnv(t) // keyring.MockInit(): in-memory keychain, never the real one

	s := NewStore()
	const token = "keychain-token-123"

	if err := s.Save(token); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if got := s.Backend(); got != backendKeychain {
		t.Errorf("Backend() = %q, want %q", got, backendKeychain)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got != token {
		t.Errorf("Load() = %q, want %q", got, token)
	}

	// The mock keychain really holds it under the contract service/account.
	raw, err := keyring.Get(KeychainService, keychainAccount)
	if err != nil || raw != token {
		t.Errorf("keyring.Get(%q, %q) = (%q, %v), want (%q, nil)",
			KeychainService, keychainAccount, raw, err, token)
	}

	if err := s.Delete(); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	got, err = s.Load()
	if err != nil || got != "" {
		t.Errorf("Load() after delete = (%q, %v), want (\"\", nil)", got, err)
	}
	// Deleting an absent entry is not an error.
	if err := s.Delete(); err != nil {
		t.Errorf("second Delete() error: %v", err)
	}
}

func TestStoreLoadAbsentIsEmpty(t *testing.T) {
	newTestEnv(t)
	got, err := NewStore().Load()
	if err != nil || got != "" {
		t.Fatalf("Load() on empty keychain = (%q, %v), want (\"\", nil)", got, err)
	}
}

func TestStoreFallsBackWhenKeychainUnsupported(t *testing.T) {
	home := newTestEnv(t)
	keyring.MockInitWithError(keyring.ErrUnsupportedPlatform)

	s := NewStore()
	const token = "fallback-token-456"

	if err := s.Save(token); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if got := s.Backend(); got != backendEncFile {
		t.Errorf("Backend() = %q, want %q", got, backendEncFile)
	}
	if _, err := os.Stat(filepath.Join(home, ".tallyfy", credFileName)); err != nil {
		t.Errorf("encrypted credentials file not written: %v", err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got != token {
		t.Errorf("Load() = %q, want %q", got, token)
	}
	if err := s.Delete(); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if got, err := s.Load(); err != nil || got != "" {
		t.Errorf("Load() after delete = (%q, %v), want (\"\", nil)", got, err)
	}
}

func TestStoreFallsBackOnDbusStyleError(t *testing.T) {
	newTestEnv(t)
	// Headless-Linux-shaped failure from go-keyring's Secret Service path.
	keyring.MockInitWithError(errors.New("dbus: dial unix /run/user/1000/bus: connect: no such file or directory"))

	s := NewStore()
	if err := s.Save("headless-token"); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if got := s.Backend(); got != backendEncFile {
		t.Errorf("Backend() = %q, want %q", got, backendEncFile)
	}
	got, err := s.Load()
	if err != nil || got != "headless-token" {
		t.Errorf("Load() = (%q, %v), want (headless-token, nil)", got, err)
	}
}

func TestStoreFallbackChoiceIsRemembered(t *testing.T) {
	newTestEnv(t)
	keyring.MockInitWithError(keyring.ErrUnsupportedPlatform)

	s := NewStore()
	if err := s.Save("tok-1"); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	// Keychain "recovers" mid-process; the process must stick with the file
	// backend it already chose (and wrote to).
	keyring.MockInit()
	got, err := NewStore().Load() // fresh Store value, same process
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got != "tok-1" {
		t.Errorf("Load() = %q, want %q (backend choice not remembered)", got, "tok-1")
	}
	if got := NewStore().Backend(); got != backendEncFile {
		t.Errorf("Backend() = %q, want %q", got, backendEncFile)
	}
}

func TestStoreRealErrorSurfacesWithoutFallback(t *testing.T) {
	home := newTestEnv(t)
	keyring.MockInitWithError(errors.New("keychain locked by policy"))

	s := NewStore()
	err := s.Save("tok")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "keychain locked by policy") {
		t.Errorf("unexpected error: %v", err)
	}
	// No silent fallback: nothing must land on disk.
	if _, statErr := os.Stat(filepath.Join(home, ".tallyfy", credFileName)); !os.IsNotExist(statErr) {
		t.Error("a non-unavailability keychain error must not write the encrypted file")
	}
	if _, err := s.Load(); err == nil {
		t.Error("Load() should also surface the keychain error")
	}
	if err := s.Delete(); err == nil {
		t.Error("Delete() should also surface the keychain error")
	}
}

func TestStoreBackendProbeWithoutPriorOperation(t *testing.T) {
	t.Run("keychain available", func(t *testing.T) {
		newTestEnv(t)
		if got := NewStore().Backend(); got != backendKeychain {
			t.Errorf("Backend() = %q, want %q", got, backendKeychain)
		}
	})
	t.Run("keychain unavailable", func(t *testing.T) {
		newTestEnv(t)
		keyring.MockInitWithError(keyring.ErrUnsupportedPlatform)
		if got := NewStore().Backend(); got != backendEncFile {
			t.Errorf("Backend() = %q, want %q", got, backendEncFile)
		}
	})
}

func TestKeychainUnavailableClassifier(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unsupported platform", keyring.ErrUnsupportedPlatform, true},
		{"wrapped unsupported platform", errors.Join(errors.New("ctx"), keyring.ErrUnsupportedPlatform), true},
		{"dbus dial", errors.New("dbus: dial unix /run/user/1000/bus: connect: no such file or directory"), true},
		{"secret service", errors.New("The name org.freedesktop.secrets was not provided by any .service files"), true},
		{"autolaunch", errors.New("dbus: cannot autolaunch a dbus-daemon without a $DISPLAY for X11"), true},
		{"plain failure", errors.New("keychain locked by policy"), false},
		{"not found is not unavailability", keyring.ErrNotFound, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := keychainUnavailable(tc.err); got != tc.want {
				t.Errorf("keychainUnavailable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
