package auth

import "github.com/tallyfy/cli/internal/config"

type stubResolver struct{}

func (stubResolver) Resolve(cfg *config.Resolved, apiKeyFlag string) (*Credential, error) {
	return nil, ErrNoCredential{}
}

// NewResolver returns the credential resolver. REPLACED BY LANE L3.
func NewResolver() Resolver { return stubResolver{} }
