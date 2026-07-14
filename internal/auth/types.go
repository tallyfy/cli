// Package auth resolves and stores Tallyfy API credentials.
//
// Resolution precedence (first present wins), per spec §6.4:
//
//  1. --api-key flag
//  2. TALLYFY_API_TOKEN environment variable
//  3. auth.apiKeyHelper script (user/local/managed settings scopes ONLY)
//  4. token stored by `tallyfy login` (OS keychain, encrypted-file fallback)
//
// Secrets live in the OS keychain (service "com.tallyfy.cli") via go-keyring.
// On headless systems without a keychain, an AES-256-GCM encrypted file at
// ~/.tallyfy/credentials.enc is used, with its key material stored 0600
// alongside; this protects against casual reads, not a compromised account
// (documented in SECURITY.md).
//
// THIS FILE IS THE FROZEN CONTRACT consumed by internal/cli.
package auth

import "github.com/tallyfy/cli/internal/config"

// Source identifies where the active credential came from.
type Source string

const (
	SourceFlag     Source = "flag"     // --api-key
	SourceEnv      Source = "env"      // TALLYFY_API_TOKEN
	SourceHelper   Source = "helper"   // auth.apiKeyHelper script
	SourceKeychain Source = "keychain" // tallyfy login
	SourceNone     Source = "none"
)

// EnvToken is the environment variable checked at precedence step 2.
const EnvToken = "TALLYFY_API_TOKEN"

// KeychainService is the OS keychain service name.
const KeychainService = "com.tallyfy.cli"

// Credential is a resolved API token plus its provenance.
type Credential struct {
	Token  string
	Source Source
}

// ErrNoCredential is returned (wrapped) when no credential source yields a
// token; the CLI maps it to exit 3 with a "run `tallyfy login`" hint.
type ErrNoCredential struct{}

func (ErrNoCredential) Error() string {
	return "no credential found: set TALLYFY_API_TOKEN, pass --api-key, configure auth.apiKeyHelper, or run `tallyfy login`"
}

// HelperResult is the JSON contract for apiKeyHelper stdout. A bare token
// string (non-JSON stdout) is also accepted.
type HelperResult struct {
	Token            string `json:"token"`
	ExpiresInSeconds int    `json:"expiresInSeconds,omitempty"`
}

// Resolver resolves credentials. Implemented in this package; consumed by
// internal/cli. Tests may substitute fakes.
type Resolver interface {
	// Resolve applies the precedence chain. apiKeyFlag is the --api-key value
	// ("" when absent). cfg supplies APIKeyHelper (already trust-filtered by
	// the config loader) and Env for the helper subprocess.
	Resolve(cfg *config.Resolved, apiKeyFlag string) (*Credential, error)
}

// Store abstracts token persistence for `tallyfy login` / `logout`.
type Store interface {
	Save(token string) error
	Load() (string, error) // returns "" with nil error when absent
	Delete() error
	// Backend reports "keychain" or "encrypted-file" for doctor/status output.
	Backend() string
}
