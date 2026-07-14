package auth

import (
	"context"
	"errors"
	"time"

	"github.com/tallyfy/cli/pkg/tallyfy"
)

// validateTimeout bounds the GET /me round-trip. A var so tests can shorten
// it.
var validateTimeout = 15 * time.Second

// ValidateToken checks a token by calling GET /me and returns the
// authenticated user. Used by `tallyfy login` (before storing a pasted
// token) and `tallyfy auth status` / `doctor`.
//
// pkg/tallyfy's Me method is implemented by lane L2 in parallel; this
// package is compiled independently, so the method is reached through a
// runtime interface assertion covering both conventional signatures. Once
// L2 lands, the first case binds statically-typed behavior; until then a
// clear "not implemented yet" error is returned (the integration stage
// exercises the wired-up path).
func ValidateToken(baseURL, token string) (*tallyfy.Me, error) {
	client := tallyfy.New(tallyfy.Options{
		BaseURL: baseURL,
		Token:   token,
	})

	ctx, cancel := context.WithTimeout(context.Background(), validateTimeout)
	defer cancel()

	switch c := any(client).(type) {
	case interface {
		Me(context.Context) (*tallyfy.Me, error)
	}:
		return c.Me(ctx)
	case interface {
		Me(context.Context) (tallyfy.Me, error)
	}:
		me, err := c.Me(ctx)
		if err != nil {
			return nil, err
		}
		return &me, nil
	default:
		return nil, errors.New("token validation unavailable: pkg/tallyfy does not implement Me yet (lane L2 pending)")
	}
}
