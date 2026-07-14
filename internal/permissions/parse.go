package permissions

import (
	"fmt"
	"strings"
)

// Parse parses one Resource(verb)[@org] rule per the grammar in the package
// doc. Whitespace around the rule and around each component is trimmed.
// Storage is case-preserving; matching is case-insensitive on resource+verb
// and case-sensitive on org. An org scope of "*" (any org) normalizes to "".
// Scope is NOT set here; callers stamp it from the contributing config scope.
func Parse(raw string) (Rule, error) {
	bad := func() (Rule, error) {
		return Rule{}, fmt.Errorf("invalid permission rule %q: expected Resource(verb)[@org]", raw)
	}
	trimmed := strings.TrimSpace(raw)
	open := strings.IndexByte(trimmed, '(')
	closing := strings.IndexByte(trimmed, ')')
	if open < 0 || closing < open {
		return bad()
	}
	resource := strings.TrimSpace(trimmed[:open])
	verb := strings.TrimSpace(trimmed[open+1 : closing])
	if !validComponent(resource) || !validComponent(verb) {
		return bad()
	}
	org := ""
	if rest := strings.TrimSpace(trimmed[closing+1:]); rest != "" {
		if rest[0] != '@' {
			return bad()
		}
		org = strings.TrimSpace(rest[1:])
		if org == "*" {
			org = "" // "@*" means any org, same as no org scope
		} else if !isIdent(org) {
			return bad()
		}
	}
	return Rule{Resource: resource, Verb: verb, Org: org, Raw: trimmed}, nil
}

// validComponent accepts IDENT or the "*" wildcard.
func validComponent(s string) bool { return s == "*" || isIdent(s) }

// isIdent reports whether s is a non-empty [A-Za-z0-9_-]+ identifier.
func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}
