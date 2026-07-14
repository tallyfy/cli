package auth

import "strings"

// Redact masks a secret for verbose/log/telemetry output. Values longer
// than 16 bytes keep the first 6 and last 4 characters ("tlfy_a...9xyz");
// anything shorter (including empty) becomes "***" so no length information
// leaks for short secrets.
func Redact(s string) string {
	if len(s) > 16 {
		return s[:6] + "..." + s[len(s)-4:]
	}
	return "***"
}

// MaskAuthHeader redacts the credential part of an Authorization header
// value ("Bearer eyJ..." -> "Bearer eyJhbG...abcd"). Values without a
// scheme prefix are redacted whole; an empty value stays empty.
func MaskAuthHeader(headerValue string) string {
	if headerValue == "" {
		return ""
	}
	if i := strings.IndexByte(headerValue, ' '); i > 0 {
		scheme := headerValue[:i]
		rest := strings.TrimSpace(headerValue[i+1:])
		if rest == "" {
			return scheme
		}
		return scheme + " " + Redact(rest)
	}
	return Redact(headerValue)
}
