package auth

import (
	"fmt"
	"sync"
)

// Backend names reported by Store.Backend() for doctor/status output.
const (
	backendKeychain = "keychain"
	backendEncFile  = "encrypted-file"
)

// The backend choice is probed lazily at the first store operation and then
// remembered for the whole process: once the OS keychain is detected as
// unavailable (headless Linux without a Secret Service, unsupported GOOS),
// every subsequent operation goes straight to the encrypted file.
var (
	backendMu     sync.Mutex
	backendChoice string // "" (undecided), backendKeychain, or backendEncFile
)

func rememberBackend(name string) {
	backendMu.Lock()
	defer backendMu.Unlock()
	backendChoice = name
}

func currentBackend() string {
	backendMu.Lock()
	defer backendMu.Unlock()
	return backendChoice
}

// resetBackendChoice clears the process-wide memo. Test helper only.
func resetBackendChoice() {
	rememberBackend("")
}

// store implements the Store contract: OS keychain first, transparent
// encrypted-file fallback when no keychain is usable.
type store struct{}

// NewStore returns the token store for `tallyfy login` / `logout` /
// `auth status`.
func NewStore() Store { return &store{} }

func (s *store) Save(token string) error {
	if currentBackend() == backendEncFile {
		return encSave(token)
	}
	err := keychainSave(token)
	switch {
	case err == nil:
		rememberBackend(backendKeychain)
		return nil
	case keychainUnavailable(err):
		rememberBackend(backendEncFile)
		return encSave(token)
	default:
		return fmt.Errorf("keychain: %w", err)
	}
}

func (s *store) Load() (string, error) {
	if currentBackend() == backendEncFile {
		return encLoad()
	}
	tok, err := keychainLoad() // absent entry already mapped to ("", nil)
	switch {
	case err == nil:
		rememberBackend(backendKeychain)
		return tok, nil
	case keychainUnavailable(err):
		rememberBackend(backendEncFile)
		return encLoad()
	default:
		return "", fmt.Errorf("keychain: %w", err)
	}
}

func (s *store) Delete() error {
	if currentBackend() == backendEncFile {
		return encDelete()
	}
	err := keychainDelete() // absent entry already mapped to nil
	switch {
	case err == nil:
		rememberBackend(backendKeychain)
		return nil
	case keychainUnavailable(err):
		rememberBackend(backendEncFile)
		return encDelete()
	default:
		return fmt.Errorf("keychain: %w", err)
	}
}

// Backend reports which backend this process is using. When no operation
// has run yet it probes with a read; a keychain that errors for reasons
// OTHER than unavailability (e.g. locked) is still reported as "keychain".
func (s *store) Backend() string {
	if b := currentBackend(); b != "" {
		return b
	}
	_, err := keychainLoad()
	if err != nil && keychainUnavailable(err) {
		rememberBackend(backendEncFile)
		return backendEncFile
	}
	rememberBackend(backendKeychain)
	return backendKeychain
}
