package tallyfy

import (
	"errors"
	"fmt"
)

// ErrorCategory buckets API failures for exit-code mapping (spec §6.8).
type ErrorCategory int

// ErrorCategory values bucket API failures by exit code (spec §6.8).
const (
	CategoryGeneric     ErrorCategory = iota // exit 1
	CategoryAuth                             // exit 3 (401/403)
	CategoryNotFound                         // exit 5 (404)
	CategoryRateLimited                      // exit 6 (429 after retries)
	CategoryValidation                       // exit 7 (422/400)
)

// APIError is a non-2xx response from the Tallyfy API.
type APIError struct {
	StatusCode int
	Message    string // parsed from {"error":true,"message":...} when present
	Body       []byte // raw response body (truncated to 8 KiB)
	RequestID  string // X-Request-Id header when present
	Method     string
	Path       string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("API error %d on %s %s: %s", e.StatusCode, e.Method, e.Path, e.Message)
	}
	return fmt.Sprintf("API error %d on %s %s", e.StatusCode, e.Method, e.Path)
}

// Category maps the HTTP status to an exit-code bucket.
func (e *APIError) Category() ErrorCategory {
	switch e.StatusCode {
	case 401, 403:
		return CategoryAuth
	case 404:
		return CategoryNotFound
	case 429:
		return CategoryRateLimited
	case 422, 400:
		return CategoryValidation
	default:
		return CategoryGeneric
	}
}

// RateLimitError wraps an APIError once the retry budget is exhausted.
type RateLimitError struct {
	*APIError
	Attempts int
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limited after %d attempts: %s", e.Attempts, e.APIError.Error())
}

// CategoryOf returns the ErrorCategory for any error (CategoryGeneric when
// the error is not API-shaped).
func CategoryOf(err error) ErrorCategory {
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return CategoryRateLimited
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Category()
	}
	return CategoryGeneric
}
