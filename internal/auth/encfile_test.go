package auth

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func encPaths(home string) (dir, credPath, keyPath string) {
	dir = filepath.Join(home, ".tallyfy")
	return dir, filepath.Join(dir, credFileName), filepath.Join(dir, keyFileName)
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return // unix permission bits are not meaningful on windows
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s permissions = %04o, want %04o", path, got, want)
	}
}

func TestEncFileRoundTrip(t *testing.T) {
	home := newTestEnv(t)
	dir, credPath, keyPath := encPaths(home)

	const token = "tlfy_secret_token_1234567890"
	if err := encSave(token); err != nil {
		t.Fatalf("encSave() error: %v", err)
	}

	assertPerm(t, dir, 0o700)
	assertPerm(t, credPath, 0o600)
	assertPerm(t, keyPath, 0o600)

	// Ciphertext at rest: the token must not appear in the file.
	raw, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatalf("reading credentials file: %v", err)
	}
	if strings.Contains(string(raw), token) {
		t.Error("token stored in plaintext")
	}

	got, err := encLoad()
	if err != nil {
		t.Fatalf("encLoad() error: %v", err)
	}
	if got != token {
		t.Errorf("encLoad() = %q, want %q", got, token)
	}

	if err := encDelete(); err != nil {
		t.Fatalf("encDelete() error: %v", err)
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Error("credentials file still exists after delete")
	}
	if got, err := encLoad(); err != nil || got != "" {
		t.Errorf("encLoad() after delete = (%q, %v), want (\"\", nil)", got, err)
	}
	// Deleting again is not an error.
	if err := encDelete(); err != nil {
		t.Errorf("second encDelete() error: %v", err)
	}
}

func TestEncFileLoadMissingIsEmpty(t *testing.T) {
	home := newTestEnv(t)
	got, err := encLoad()
	if err != nil || got != "" {
		t.Fatalf("encLoad() with nothing stored = (%q, %v), want (\"\", nil)", got, err)
	}
	// Load must be side-effect free: no ~/.tallyfy created.
	if _, err := os.Stat(filepath.Join(home, ".tallyfy")); !os.IsNotExist(err) {
		t.Error("encLoad() created ~/.tallyfy as a side effect")
	}
}

func TestEncFileOverwrite(t *testing.T) {
	newTestEnv(t)
	if err := encSave("first-token"); err != nil {
		t.Fatalf("encSave() error: %v", err)
	}
	if err := encSave("second-token"); err != nil {
		t.Fatalf("second encSave() error: %v", err)
	}
	got, err := encLoad()
	if err != nil {
		t.Fatalf("encLoad() error: %v", err)
	}
	if got != "second-token" {
		t.Errorf("encLoad() = %q, want %q", got, "second-token")
	}
}

func TestEncFileCorruptedFile(t *testing.T) {
	home := newTestEnv(t)
	_, credPath, keyPath := encPaths(home)

	if err := encSave("some-token"); err != nil {
		t.Fatalf("encSave() error: %v", err)
	}

	cases := []struct {
		name    string
		corrupt func(t *testing.T)
	}{
		{"not base64", func(t *testing.T) {
			t.Helper()
			if err := os.WriteFile(credPath, []byte("!!!not-base64!!!"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"too short", func(t *testing.T) {
			t.Helper()
			short := base64.StdEncoding.EncodeToString([]byte("tiny"))
			if err := os.WriteFile(credPath, []byte(short), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"flipped ciphertext byte", func(t *testing.T) {
			t.Helper()
			b64, err := os.ReadFile(credPath)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := base64.StdEncoding.DecodeString(string(b64))
			if err != nil {
				t.Fatal(err)
			}
			raw[len(raw)-1] ^= 0xFF
			if err := os.WriteFile(credPath, []byte(base64.StdEncoding.EncodeToString(raw)), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"key file missing", func(t *testing.T) {
			t.Helper()
			if err := os.Remove(keyPath); err != nil {
				t.Fatal(err)
			}
		}},
		{"key file wrong size", func(t *testing.T) {
			t.Helper()
			if err := os.WriteFile(keyPath, []byte("short-key"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Fresh valid state, then corrupt one piece.
			if err := encSave("some-token"); err != nil {
				t.Fatalf("encSave() error: %v", err)
			}
			tc.corrupt(t)
			_, err := encLoad()
			if err == nil {
				t.Fatal("expected a corruption error")
			}
			if !strings.Contains(err.Error(), "cannot be decrypted") {
				t.Errorf("error should explain the file is unreadable, got: %v", err)
			}
			if !strings.Contains(err.Error(), "tallyfy login") {
				t.Errorf("error should include the recovery hint, got: %v", err)
			}
			// Restore for the next case.
			if err := encDelete(); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(keyPath); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		})
	}
}

func TestEncSaveTokensDifferCiphertext(t *testing.T) {
	home := newTestEnv(t)
	_, credPath, _ := encPaths(home)

	if err := encSave("same-token"); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := encSave("same-token"); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) == string(second) {
		t.Error("re-encrypting the same token produced identical ciphertext (nonce reuse?)")
	}
}
