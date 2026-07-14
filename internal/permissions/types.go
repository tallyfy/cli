// Package permissions implements the Resource(verb)[@org] rule engine that
// gates every CLI command (spec §6.5).
//
// Rule grammar (EBNF):
//
//	rule      = resource "(" verb ")" [ "@" orgscope ]
//	resource  = IDENT | "*"
//	verb      = IDENT | "*"
//	orgscope  = IDENT | "*"
//
// Evaluation order (deny wins):
//
//  1. any MANAGED deny matches                 -> Deny (non-overridable)
//  2. allowManagedRulesOnly && no managed allow -> Deny
//  3. any deny matches                          -> Deny
//  4. any allow matches                         -> Allow
//  5. any ask matches                           -> Ask
//  6. otherwise                                 -> defaultMode
//
// In non-interactive contexts the CLI resolves Ask to Deny unless --yes.
//
// THIS FILE IS THE FROZEN CONTRACT consumed by internal/cli.
package permissions

import "github.com/tallyfy/cli/internal/config"

// Token is the Resource(verb) identity of one CLI command invocation.
type Token struct {
	Resource string // e.g. "Blueprint"
	Verb     string // e.g. "delete"
}

// String renders the canonical "Resource(verb)" form.
func (t Token) String() string { return t.Resource + "(" + t.Verb + ")" }

// Rule is one parsed permission rule.
type Rule struct {
	Resource string // "*" allowed
	Verb     string // "*" allowed
	Org      string // "" = any org; "*" normalized to ""
	Raw      string
	Scope    config.Scope
}

// Decision is the evaluation outcome.
type Decision int

// Decision values are the possible permission-evaluation outcomes.
const (
	Allow Decision = iota
	Ask
	Deny
)

func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Ask:
		return "ask"
	case Deny:
		return "deny"
	}
	return "unknown"
}

// Result carries the decision plus provenance for error messages
// ("blocked by Blueprint(delete) from managed scope (/etc/tallyfy/...)").
type Result struct {
	Decision Decision
	// MatchedRule is the rule that determined the outcome; nil when the
	// defaultMode decided.
	MatchedRule *Rule
	// FromDefault is true when defaultMode decided.
	FromDefault bool
	Reason      string
}

// Input is everything Evaluate needs.
type Input struct {
	Token       Token
	Org         string // current org id ("" when none)
	Allow       []Rule
	AskRules    []Rule
	Deny        []Rule
	DefaultMode string // "allow" | "ask" | "deny"
	// AllowManagedRulesOnly ignores all non-managed allow rules.
	AllowManagedRulesOnly bool
}
