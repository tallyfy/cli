package permissions

import (
	"strings"
	"testing"

	"github.com/tallyfy/cli/internal/config"
)

func mustRule(t *testing.T, raw string, scope config.Scope) Rule {
	t.Helper()
	r, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	r.Scope = scope
	return r
}

func TestEvaluateMatrix(t *testing.T) {
	bpDelete := Token{Resource: "Blueprint", Verb: "delete"}
	bpArchive := Token{Resource: "Blueprint", Verb: "archive"}
	taskComplete := Token{Resource: "Task", Verb: "complete"}

	type tc struct {
		name            string
		in              Input
		want            Decision
		wantFromDefault bool
		wantMatchedRaw  string   // "" means MatchedRule must be nil
		wantReason      []string // substrings that must appear in Reason
	}

	cases := []tc{
		{
			name: "allow only",
			in: Input{
				Token: bpDelete, Org: "org_a",
				Allow:       []Rule{mustRule(t, "Blueprint(delete)", config.ScopeUser)},
				DefaultMode: "ask",
			},
			want:           Allow,
			wantMatchedRaw: "Blueprint(delete)",
			wantReason:     []string{`allowed by rule "Blueprint(delete)"`, "(user scope)"},
		},
		{
			name: "deny beats allow",
			in: Input{
				Token: bpDelete, Org: "org_a",
				Allow:       []Rule{mustRule(t, "Blueprint(delete)", config.ScopeUser)},
				Deny:        []Rule{mustRule(t, "Blueprint(delete)", config.ScopeLocal)},
				DefaultMode: "allow",
			},
			want:           Deny,
			wantMatchedRaw: "Blueprint(delete)",
			wantReason:     []string{`denied by rule "Blueprint(delete)"`, "(local scope)"},
		},
		{
			name: "managed deny beats local allow",
			in: Input{
				Token: bpDelete, Org: "org_a",
				Allow:       []Rule{mustRule(t, "Blueprint(delete)", config.ScopeLocal)},
				Deny:        []Rule{mustRule(t, "Blueprint(delete)", config.ScopeManaged)},
				DefaultMode: "allow",
			},
			want:           Deny,
			wantMatchedRaw: "Blueprint(delete)",
			wantReason:     []string{"managed policy", `"Blueprint(delete)"`, "(managed scope)"},
		},
		{
			name: "managed deny beats managed allow",
			in: Input{
				Token: bpDelete, Org: "org_a",
				Allow: []Rule{mustRule(t, "Blueprint(delete)", config.ScopeManaged)},
				Deny:  []Rule{mustRule(t, "Blueprint(delete)", config.ScopeManaged)},
			},
			want:           Deny,
			wantMatchedRaw: "Blueprint(delete)",
			wantReason:     []string{"managed policy"},
		},
		{
			name: "ask triggers",
			in: Input{
				Token: bpDelete, Org: "org_a",
				AskRules:    []Rule{mustRule(t, "Blueprint(delete)", config.ScopeUser)},
				DefaultMode: "allow",
			},
			want:           Ask,
			wantMatchedRaw: "Blueprint(delete)",
			wantReason:     []string{`rule "Blueprint(delete)"`, "(user scope)"},
		},
		{
			name: "wildcard resource *(delete)",
			in: Input{
				Token: bpDelete, Org: "org_a",
				Deny:        []Rule{mustRule(t, "*(delete)", config.ScopeUser)},
				DefaultMode: "allow",
			},
			want:           Deny,
			wantMatchedRaw: "*(delete)",
			wantReason:     []string{`denied by rule "*(delete)"`},
		},
		{
			name: "wildcard verb Blueprint(*)",
			in: Input{
				Token: bpArchive, Org: "org_a",
				Allow:       []Rule{mustRule(t, "Blueprint(*)", config.ScopeProject)},
				DefaultMode: "deny",
			},
			want:           Allow,
			wantMatchedRaw: "Blueprint(*)",
			wantReason:     []string{"(project scope)"},
		},
		{
			name: "org-scoped rule does not match another org",
			in: Input{
				Token: taskComplete, Org: "org_b",
				Allow:       []Rule{mustRule(t, "Task(complete)@org_a", config.ScopeUser)},
				DefaultMode: "deny",
			},
			want:            Deny,
			wantFromDefault: true,
			wantReason:      []string{"no matching rule", "defaultMode=deny"},
		},
		{
			name: "org-scoped rule matches its org",
			in: Input{
				Token: taskComplete, Org: "org_a",
				Allow:       []Rule{mustRule(t, "Task(complete)@org_a", config.ScopeUser)},
				DefaultMode: "deny",
			},
			want:           Allow,
			wantMatchedRaw: "Task(complete)@org_a",
		},
		{
			name: "rule without org matches any org",
			in: Input{
				Token: taskComplete, Org: "org_zzz",
				Allow:       []Rule{mustRule(t, "Task(complete)", config.ScopeUser)},
				DefaultMode: "deny",
			},
			want:           Allow,
			wantMatchedRaw: "Task(complete)",
		},
		{
			name: "org-scoped rule never matches empty org",
			in: Input{
				Token: taskComplete, Org: "",
				Allow:       []Rule{mustRule(t, "Task(complete)@org_a", config.ScopeUser)},
				DefaultMode: "deny",
			},
			want:            Deny,
			wantFromDefault: true,
			wantReason:      []string{"defaultMode=deny"},
		},
		{
			name: "case-insensitive resource+verb matching",
			in: Input{
				Token: Token{Resource: "blueprint", Verb: "delete"}, Org: "org_a",
				Allow:       []Rule{mustRule(t, "Blueprint(DELETE)", config.ScopeUser)},
				DefaultMode: "deny",
			},
			want:           Allow,
			wantMatchedRaw: "Blueprint(DELETE)",
		},
		{
			name: "allowManagedRulesOnly with matching managed allow",
			in: Input{
				Token: bpDelete, Org: "org_a",
				Allow:                 []Rule{mustRule(t, "Blueprint(delete)", config.ScopeManaged)},
				DefaultMode:           "deny",
				AllowManagedRulesOnly: true,
			},
			want:           Allow,
			wantMatchedRaw: "Blueprint(delete)",
			wantReason:     []string{"(managed scope)"},
		},
		{
			name: "allowManagedRulesOnly without managed allow denies despite local allow",
			in: Input{
				Token: bpDelete, Org: "org_a",
				Allow:                 []Rule{mustRule(t, "Blueprint(delete)", config.ScopeLocal)},
				DefaultMode:           "allow",
				AllowManagedRulesOnly: true,
			},
			want:       Deny,
			wantReason: []string{"allowManagedRulesOnly", "Blueprint(delete)"},
		},
		{
			name: "allowManagedRulesOnly ignores non-managed allow when attributing",
			in: Input{
				Token: bpDelete, Org: "org_a",
				Allow: []Rule{
					mustRule(t, "*(*)", config.ScopeUser), // would match first, but is not managed
					mustRule(t, "Blueprint(delete)", config.ScopeManaged),
				},
				AllowManagedRulesOnly: true,
			},
			want:           Allow,
			wantMatchedRaw: "Blueprint(delete)",
			wantReason:     []string{"(managed scope)"},
		},
		{
			name: "allowManagedRulesOnly satisfied but deny still wins",
			in: Input{
				Token: bpDelete, Org: "org_a",
				Allow:                 []Rule{mustRule(t, "Blueprint(delete)", config.ScopeManaged)},
				Deny:                  []Rule{mustRule(t, "Blueprint(delete)", config.ScopeLocal)},
				AllowManagedRulesOnly: true,
			},
			want:           Deny,
			wantMatchedRaw: "Blueprint(delete)",
			wantReason:     []string{"(local scope)"},
		},
		{
			name:            "defaultMode allow",
			in:              Input{Token: bpDelete, Org: "org_a", DefaultMode: "allow"},
			want:            Allow,
			wantFromDefault: true,
			wantReason:      []string{"no matching rule", "defaultMode=allow"},
		},
		{
			name:            "defaultMode deny",
			in:              Input{Token: bpDelete, Org: "org_a", DefaultMode: "deny"},
			want:            Deny,
			wantFromDefault: true,
			wantReason:      []string{"no matching rule", "defaultMode=deny"},
		},
		{
			name:            "defaultMode ask",
			in:              Input{Token: bpDelete, Org: "org_a", DefaultMode: "ask"},
			want:            Ask,
			wantFromDefault: true,
			wantReason:      []string{"no matching rule", "defaultMode=ask"},
		},
		{
			name:            "defaultMode empty resolves to ask",
			in:              Input{Token: bpDelete, Org: "org_a", DefaultMode: ""},
			want:            Ask,
			wantFromDefault: true,
			wantReason:      []string{"defaultMode=ask"},
		},
		{
			name:            "defaultMode unknown resolves to ask",
			in:              Input{Token: bpDelete, Org: "org_a", DefaultMode: "bogus"},
			want:            Ask,
			wantFromDefault: true,
			wantReason:      []string{"defaultMode=ask"},
		},
		{
			name:            "read verb list allowed under ask default",
			in:              Input{Token: Token{Resource: "Blueprint", Verb: "list"}, Org: "org_a", DefaultMode: "ask"},
			want:            Allow,
			wantFromDefault: true,
			wantReason:      []string{"read-only verb", "list"},
		},
		{
			name:            "read verb get allowed under empty default",
			in:              Input{Token: Token{Resource: "Task", Verb: "get"}, Org: "org_a", DefaultMode: ""},
			want:            Allow,
			wantFromDefault: true,
			wantReason:      []string{"read-only verb"},
		},
		{
			name: "explicit deny beats read-verb auto-allow",
			in: Input{
				Token: Token{Resource: "Blueprint", Verb: "list"}, Org: "org_a",
				Deny:        []Rule{mustRule(t, "Blueprint(list)", config.ScopeManaged)},
				DefaultMode: "ask",
			},
			want:           Deny,
			wantMatchedRaw: "Blueprint(list)",
		},
		{
			name: "explicit ask beats read-verb auto-allow",
			in: Input{
				Token: Token{Resource: "Blueprint", Verb: "export"}, Org: "org_a",
				AskRules:    []Rule{mustRule(t, "Blueprint(export)", config.ScopeUser)},
				DefaultMode: "ask",
			},
			want:           Ask,
			wantMatchedRaw: "Blueprint(export)",
		},
		{
			name:            "defaultMode deny lockdown denies read verbs too",
			in:              Input{Token: Token{Resource: "Blueprint", Verb: "list"}, Org: "org_a", DefaultMode: "deny"},
			want:            Deny,
			wantFromDefault: true,
			wantReason:      []string{"defaultMode=deny"},
		},
		{
			name: "first matching rule wins provenance",
			in: Input{
				Token: bpDelete, Org: "org_a",
				Allow: []Rule{
					mustRule(t, "Process(*)", config.ScopeUser), // no match
					mustRule(t, "Blueprint(*)", config.ScopeProject),
					mustRule(t, "Blueprint(delete)", config.ScopeUser),
				},
			},
			want:           Allow,
			wantMatchedRaw: "Blueprint(*)",
			wantReason:     []string{"(project scope)"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Evaluate(tc.in)
			if res.Decision != tc.want {
				t.Errorf("Decision = %s, want %s (reason: %s)", res.Decision, tc.want, res.Reason)
			}
			if res.FromDefault != tc.wantFromDefault {
				t.Errorf("FromDefault = %v, want %v", res.FromDefault, tc.wantFromDefault)
			}
			if tc.wantMatchedRaw == "" {
				if res.MatchedRule != nil {
					t.Errorf("MatchedRule = %+v, want nil", res.MatchedRule)
				}
			} else if res.MatchedRule == nil {
				t.Errorf("MatchedRule = nil, want rule %q", tc.wantMatchedRaw)
			} else if res.MatchedRule.Raw != tc.wantMatchedRaw {
				t.Errorf("MatchedRule.Raw = %q, want %q", res.MatchedRule.Raw, tc.wantMatchedRaw)
			}
			for _, sub := range tc.wantReason {
				if !strings.Contains(res.Reason, sub) {
					t.Errorf("Reason %q does not contain %q", res.Reason, sub)
				}
			}
			if res.Reason == "" {
				t.Error("Reason is empty; every Result must carry a reason")
			}
		})
	}
}

// TestEvaluateSkipsInvalidRulesNever documents that Evaluate trusts its
// inputs: rules arrive pre-parsed via Parse (invalid raw strings never
// become Rule values), so a zero Rule matches nothing but wildcard-free
// tokens only by accident. The CLI layer (toRules) drops unparseable rules.
func TestEvaluateEmptyInputs(t *testing.T) {
	res := Evaluate(Input{Token: Token{Resource: "Blueprint", Verb: "delete"}})
	if res.Decision != Ask || !res.FromDefault {
		t.Errorf("empty input = %s (FromDefault=%v), want ask from default", res.Decision, res.FromDefault)
	}
}
