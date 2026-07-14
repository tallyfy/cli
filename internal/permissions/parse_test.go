package permissions

import (
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	cases := []struct {
		raw      string
		resource string
		verb     string
		org      string
		wantRaw  string
	}{
		{"Blueprint(delete)", "Blueprint", "delete", "", "Blueprint(delete)"},
		{"Process(*)", "Process", "*", "", "Process(*)"},
		{"*(delete)", "*", "delete", "", "*(delete)"},
		{"*(*)", "*", "*", "", "*(*)"},
		{"Task(complete)@org_abc", "Task", "complete", "org_abc", "Task(complete)@org_abc"},
		// "*" org scope normalizes to "" (any org).
		{"Task(complete)@*", "Task", "complete", "", "Task(complete)@*"},
		// Outer whitespace is trimmed; Raw stores the trimmed rule.
		{"  Blueprint(delete)  ", "Blueprint", "delete", "", "Blueprint(delete)"},
		// Whitespace around components is tolerated.
		{"Blueprint ( delete ) @ org-1", "Blueprint", "delete", "org-1", "Blueprint ( delete ) @ org-1"},
		// Case is PRESERVED in storage.
		{"BLUEPRINT(Delete)@Org_A", "BLUEPRINT", "Delete", "Org_A", "BLUEPRINT(Delete)@Org_A"},
		// Idents may contain digits, underscore, hyphen.
		{"form-field_2(list)", "form-field_2", "list", "", "form-field_2(list)"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			r, err := Parse(tc.raw)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tc.raw, err)
			}
			if r.Resource != tc.resource || r.Verb != tc.verb || r.Org != tc.org {
				t.Errorf("Parse(%q) = {Resource:%q Verb:%q Org:%q}, want {%q %q %q}",
					tc.raw, r.Resource, r.Verb, r.Org, tc.resource, tc.verb, tc.org)
			}
			if r.Raw != tc.wantRaw {
				t.Errorf("Parse(%q).Raw = %q, want %q", tc.raw, r.Raw, tc.wantRaw)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"Blueprint",               // no (verb)
		"Blueprint()",             // empty verb
		"(delete)",                // empty resource
		"Blueprint(delete",        // unclosed paren
		"Blueprint delete)",       // no open paren
		")(",                      // closing before opening
		"Blueprint(de lete)",      // whitespace inside verb
		"Blue print(delete)",      // whitespace inside resource
		"Blueprint(delete)@",      // empty org
		"Blueprint(delete)@a b",   // whitespace inside org
		"Blueprint(delete)@org@2", // second @
		"Blueprint(delete)x",      // trailing junk before @
		"Blueprint(delete))",      // extra paren
		"Blueprint(del.ete)",      // illegal char in verb
		"Blue.print(delete)",      // illegal char in resource
		"Blueprint(delete)@org.1", // illegal char in org
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := Parse(raw)
			if err == nil {
				t.Fatalf("Parse(%q) succeeded, want error", raw)
			}
			msg := err.Error()
			if !strings.Contains(msg, "invalid permission rule") ||
				!strings.Contains(msg, "expected Resource(verb)[@org]") {
				t.Errorf("Parse(%q) error %q lacks the descriptive format", raw, msg)
			}
		})
	}
}
