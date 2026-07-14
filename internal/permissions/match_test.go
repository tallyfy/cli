package permissions

import "testing"

func TestMatchesMatcher(t *testing.T) {
	tok := Token{Resource: "Blueprint", Verb: "delete"}
	cases := []struct {
		name    string
		matcher string
		tok     Token
		org     string
		want    bool
		wantErr bool
	}{
		{"empty matches all", "", tok, "org_a", true, false},
		{"whitespace matches all", "   \t", tok, "org_a", true, false},
		{"bare star matches all", "*", tok, "org_a", true, false},
		{"padded star matches all", "  *  ", tok, "org_a", true, false},
		{"wildcard verb match", "Blueprint(*)", tok, "org_a", true, false},
		{"wildcard resource match", "*(delete)", tok, "org_a", true, false},
		{"exact match", "Blueprint(delete)", tok, "org_a", true, false},
		{"case-insensitive match", "blueprint(DELETE)", tok, "org_a", true, false},
		{"resource mismatch", "Task(*)", tok, "org_a", false, false},
		{"verb mismatch", "Blueprint(archive)", tok, "org_a", false, false},
		{"org scope match", "Blueprint(delete)@org_a", tok, "org_a", true, false},
		{"org scope mismatch", "Blueprint(delete)@org_a", tok, "org_b", false, false},
		{"org scope vs no org", "Blueprint(delete)@org_a", tok, "", false, false},
		{"star org matches any", "Blueprint(delete)@*", tok, "org_zzz", true, false},
		{"malformed matcher", "Blueprint(", tok, "org_a", false, true},
		{"malformed matcher empty verb", "Blueprint()", tok, "org_a", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MatchesMatcher(tc.matcher, tc.tok, tc.org)
			if tc.wantErr != (err != nil) {
				t.Fatalf("MatchesMatcher(%q) err = %v, wantErr=%v", tc.matcher, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("MatchesMatcher(%q, %v, %q) = %v, want %v", tc.matcher, tc.tok, tc.org, got, tc.want)
			}
		})
	}
}

func TestTokenString(t *testing.T) {
	tok := Token{Resource: "Blueprint", Verb: "delete"}
	if got := tok.String(); got != "Blueprint(delete)" {
		t.Errorf("Token.String() = %q, want %q", got, "Blueprint(delete)")
	}
}
