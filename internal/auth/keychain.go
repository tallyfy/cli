package auth

import (
	"errors"
	"os/exec"
	"strings"

	"github.com/zalando/go-keyring"
)

// keychainAccount is the fixed account name under KeychainService: the CLI
// stores exactly one token per OS user.
const keychainAccount = "default"

// keychainUnavailable classifies a go-keyring error as "no usable keychain
// on this system" (fall back to the encrypted file) versus a real error on a
// working keychain (surface it). keyring.ErrNotFound is NEVER passed here —
// it means the keychain works and simply has no entry.
//
// Unavailability signals:
//   - keyring.ErrUnsupportedPlatform (GOOS with no provider)
//   - *exec.Error (darwin: /usr/bin/security missing; linux: dbus-launch
//     missing during autolaunch)
//   - D-Bus / Secret Service plumbing failures on headless Linux, which
//     go-keyring surfaces as raw dbus errors ("dbus: ...", missing session
//     bus socket, org.freedesktop.secrets not activatable, no X11 display)
func keychainUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, keyring.ErrUnsupportedPlatform) {
		return true
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"dbus",
		"org.freedesktop",
		"secret service",
		"autolaunch",
		"no such file or directory", // dial unix /run/user/N/bus on headless linux
		"x11",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// keychainSave / keychainLoad / keychainDelete are thin wrappers over
// go-keyring with the CLI's fixed service/account, kept separate from
// store.go so the backend-selection logic reads cleanly.

func keychainSave(token string) error {
	return keyring.Set(KeychainService, keychainAccount, token)
}

// keychainLoad returns ("", keyring.ErrNotFound) mapped to ("", nil): an
// absent entry is not an error per the Store contract.
func keychainLoad() (string, error) {
	tok, err := keyring.Get(KeychainService, keychainAccount)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	return tok, err
}

// keychainDelete treats an absent entry as success.
func keychainDelete() error {
	err := keyring.Delete(KeychainService, keychainAccount)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
