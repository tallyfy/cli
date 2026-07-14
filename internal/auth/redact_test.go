package auth

import "testing"

func TestRedact(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"long token keeps 6+4", "abcdefghijklmnopqrstuvwxyz", "abcdef...wxyz"},
		{"17 chars is masked partially", "abcdefghijklmnopq", "abcdef...nopq"},
		{"16 chars is fully masked", "abcdefghijklmnop", "***"},
		{"short", "abc", "***"},
		{"empty", "", "***"},
		{"realistic bearer token", "eyJ0eXAiOiJKV1QiLCJhbGciOiJSUzI1NiJ9", "eyJ0eX...NiJ9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Redact(tc.in); got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactNeverLeaksMiddle(t *testing.T) {
	tok := "prefix-SECRETMIDDLE-suffix-abcd"
	got := Redact(tok)
	if len(got) >= len(tok) {
		t.Errorf("redacted value is not shorter than the token: %q", got)
	}
	if got != tok[:6]+"..."+tok[len(tok)-4:] {
		t.Errorf("Redact(%q) = %q", tok, got)
	}
}

func TestMaskAuthHeader(t *testing.T) {
	long := "abcdefghijklmnopqrstuvwxyz"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bearer long", "Bearer " + long, "Bearer abcdef...wxyz"},
		{"bearer short", "Bearer abc", "Bearer ***"},
		{"bare token", long, "abcdef...wxyz"},
		{"empty", "", ""},
		{"scheme only", "Bearer ", "Bearer"},
		{"basic scheme", "Basic " + long, "Basic abcdef...wxyz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaskAuthHeader(tc.in); got != tc.want {
				t.Errorf("MaskAuthHeader(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
