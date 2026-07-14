package permissions

import (
	"fmt"

	"github.com/tallyfy/cli/internal/config"
)

// Evaluate applies the deny-wins algorithm from the package doc (spec §6.5).
func Evaluate(in Input) Result {
	// 1. Managed deny rules are non-overridable.
	if r := firstMatch(in.Deny, in.Token, in.Org, true); r != nil {
		return Result{
			Decision:    Deny,
			MatchedRule: r,
			Reason:      fmt.Sprintf("denied by managed policy rule %q (%s scope)", r.Raw, r.Scope),
		}
	}
	// 2. allowManagedRulesOnly: without a matching managed allow, deny.
	if in.AllowManagedRulesOnly && firstMatch(in.Allow, in.Token, in.Org, true) == nil {
		return Result{
			Decision: Deny,
			Reason:   fmt.Sprintf("denied: allowManagedRulesOnly is set and no managed allow rule matches %s", in.Token),
		}
	}
	// 3. Any deny, from any scope.
	if r := firstMatch(in.Deny, in.Token, in.Org, false); r != nil {
		return Result{
			Decision:    Deny,
			MatchedRule: r,
			Reason:      fmt.Sprintf("denied by rule %q (%s scope)", r.Raw, r.Scope),
		}
	}
	// 4. Any allow. When allowManagedRulesOnly is set, non-managed allow
	// rules are ignored (types.go contract).
	if r := firstMatch(in.Allow, in.Token, in.Org, in.AllowManagedRulesOnly); r != nil {
		return Result{
			Decision:    Allow,
			MatchedRule: r,
			Reason:      fmt.Sprintf("allowed by rule %q (%s scope)", r.Raw, r.Scope),
		}
	}
	// 5. Any ask.
	if r := firstMatch(in.AskRules, in.Token, in.Org, false); r != nil {
		return Result{
			Decision:    Ask,
			MatchedRule: r,
			Reason:      fmt.Sprintf("confirmation required by rule %q (%s scope)", r.Raw, r.Scope),
		}
	}
	// 6. No rule matched: defaultMode decides ("allow"/"deny"; anything
	// else, including "ask" and "", resolves to Ask).
	d := Ask
	switch in.DefaultMode {
	case "allow":
		d = Allow
	case "deny":
		d = Deny
	}
	return Result{
		Decision:    d,
		FromDefault: true,
		Reason:      fmt.Sprintf("no matching rule; defaultMode=%s", d),
	}
}

// firstMatch returns a copy of the first rule matching tok+org, optionally
// considering only managed-scope rules. Nil when nothing matches.
func firstMatch(rules []Rule, tok Token, org string, managedOnly bool) *Rule {
	for i := range rules {
		if managedOnly && rules[i].Scope != config.ScopeManaged {
			continue
		}
		if matches(rules[i], tok, org) {
			r := rules[i]
			return &r
		}
	}
	return nil
}
