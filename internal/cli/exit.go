package cli

import (
	"errors"

	"github.com/tallyfy/cli/internal/auth"
	"github.com/tallyfy/cli/internal/hooks"
	"github.com/tallyfy/cli/internal/permissions"
	"github.com/tallyfy/cli/pkg/tallyfy"
)

// Exit codes are a stable contract (spec §6.8). Scripts depend on them.
const (
	ExitOK          = 0
	ExitGeneric     = 1
	ExitUsage       = 2
	ExitAuth        = 3
	ExitPermission  = 4
	ExitNotFound    = 5
	ExitRateLimited = 6
	ExitValidation  = 7
	ExitHookBlocked = 8
	ExitBulkPartial = 9
)

// PermissionDeniedError is returned when the permission engine blocks a
// command (exit 4).
type PermissionDeniedError struct {
	Token  permissions.Token
	Result permissions.Result
}

func (e *PermissionDeniedError) Error() string {
	msg := "permission denied for " + e.Token.String()
	if e.Result.Reason != "" {
		msg += ": " + e.Result.Reason
	}
	return msg
}

// BulkPartialError is returned when a bulk operation partially fails (exit 9).
type BulkPartialError struct {
	Succeeded int
	Failed    int
	Total     int
}

func (e *BulkPartialError) Error() string {
	return "bulk operation partially failed"
}

// UsageError marks bad flags/arguments (exit 2).
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// exitCodeFor maps any error to the exit-code contract.
func exitCodeFor(err error) int {
	if err == nil {
		return ExitOK
	}
	var ue *UsageError
	if errors.As(err, &ue) {
		return ExitUsage
	}
	var nc auth.ErrNoCredential
	if errors.As(err, &nc) {
		return ExitAuth
	}
	var pd *PermissionDeniedError
	if errors.As(err, &pd) {
		return ExitPermission
	}
	var hb *hooks.BlockError
	if errors.As(err, &hb) {
		return ExitHookBlocked
	}
	var bp *BulkPartialError
	if errors.As(err, &bp) {
		return ExitBulkPartial
	}
	switch tallyfy.CategoryOf(err) {
	case tallyfy.CategoryAuth:
		return ExitAuth
	case tallyfy.CategoryNotFound:
		return ExitNotFound
	case tallyfy.CategoryRateLimited:
		return ExitRateLimited
	case tallyfy.CategoryValidation:
		return ExitValidation
	}
	return ExitGeneric
}
