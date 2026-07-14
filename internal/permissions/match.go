package permissions

import "strings"

// matches reports whether rule r applies to token tok in org.
//
// Resource and verb match case-insensitively, with "*" matching anything.
// Org matches case-sensitively: a rule without an org scope (Org == "")
// applies in any org, while an org-scoped rule applies only in exactly that
// org — so when the invocation has no org (org == ""), only unscoped rules
// match.
func matches(r Rule, tok Token, org string) bool {
	if r.Resource != "*" && !strings.EqualFold(r.Resource, tok.Resource) {
		return false
	}
	if r.Verb != "*" && !strings.EqualFold(r.Verb, tok.Verb) {
		return false
	}
	return r.Org == "" || r.Org == org
}

// MatchesMatcher reports whether a hook matcher string matches tok in org
// (spec §6.7: hook matchers reuse the permission rule grammar). An empty or
// whitespace-only matcher and the bare wildcard "*" match everything.
// A malformed matcher returns the parse error.
func MatchesMatcher(matcher string, tok Token, org string) (bool, error) {
	m := strings.TrimSpace(matcher)
	if m == "" || m == "*" {
		return true, nil
	}
	r, err := Parse(m)
	if err != nil {
		return false, err
	}
	return matches(r, tok, org), nil
}
