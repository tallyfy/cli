package auth

import (
	"fmt"
	"os"
	"strings"

	"github.com/tallyfy/cli/internal/config"
)

// resolver implements the Resolver contract (spec §6.4 precedence chain).
// Collaborators are injected as function fields so tests can substitute
// fakes without touching the process environment or the OS keychain.
type resolver struct {
	getenv    func(string) string
	runHelper func(command string, extraEnv map[string]string) (string, error)
	newStore  func() Store
}

// NewResolver returns the production credential resolver.
func NewResolver() Resolver {
	return &resolver{
		getenv:    os.Getenv,
		runHelper: runHelper,
		newStore:  NewStore,
	}
}

// Resolve applies the fixed precedence order; the first present credential
// wins:
//
//  1. --api-key flag
//  2. TALLYFY_API_TOKEN environment variable
//  3. auth.apiKeyHelper script (trusted scopes only; the config loader
//     guarantees cfg.APIKeyHelper never originates from project scope)
//  4. token stored by `tallyfy login` (keychain / encrypted-file store)
//  5. none -> ErrNoCredential (CLI maps it to exit 3)
//
// Deliberate v1 deviation (STATE.md): helper results are NOT cached; the
// helper runs once per invocation so secrets never persist outside the
// keychain.
func (r *resolver) Resolve(cfg *config.Resolved, apiKeyFlag string) (*Credential, error) {
	// 1. Explicit --api-key flag.
	if apiKeyFlag != "" {
		return &Credential{Token: apiKeyFlag, Source: SourceFlag}, nil
	}

	// 2. TALLYFY_API_TOKEN. Surrounding whitespace (e.g. a trailing newline
	// from `export TALLYFY_API_TOKEN=$(cat file)`) is trimmed: a padded
	// token can never be valid and would break the Authorization header.
	if tok := strings.TrimSpace(r.getenv(EnvToken)); tok != "" {
		return &Credential{Token: tok, Source: SourceEnv}, nil
	}

	// 3. auth.apiKeyHelper script.
	if cfg != nil && cfg.APIKeyHelper != "" {
		tok, err := r.runHelper(cfg.APIKeyHelper, cfg.Env)
		if err != nil {
			return nil, err // already prefixed "apiKeyHelper failed: ..."
		}
		return &Credential{Token: tok, Source: SourceHelper}, nil
	}

	// 4. Token stored by `tallyfy login`.
	tok, err := r.newStore().Load()
	if err != nil {
		return nil, fmt.Errorf("reading stored credential: %w", err)
	}
	if tok != "" {
		return &Credential{Token: tok, Source: SourceKeychain}, nil
	}

	// 5. Nothing found.
	return nil, ErrNoCredential{}
}
