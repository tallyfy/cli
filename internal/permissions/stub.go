package permissions

import "errors"

// Parse parses one Resource(verb)[@org] rule. REPLACED BY LANE L4.
func Parse(raw string) (Rule, error) {
	return Rule{}, errors.New("permissions.Parse not implemented (L4)")
}

// Evaluate applies the deny-wins algorithm. REPLACED BY LANE L4.
func Evaluate(in Input) Result {
	return Result{Decision: Deny, Reason: "permissions engine not implemented (L4)"}
}

// MatchesMatcher reports whether a hook matcher matches a token. REPLACED BY LANE L4.
func MatchesMatcher(matcher string, tok Token, org string) (bool, error) { return false, nil }
