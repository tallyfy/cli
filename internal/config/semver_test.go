package config

import (
	"strings"
	"testing"
)

func TestVersionGate(t *testing.T) {
	cases := []struct {
		name     string
		min, max string
		current  string
		wantErr  bool
		wantWarn string // substring expected in a warning ("" = no warning required)
	}{
		{name: "no bounds", current: "1.0.0"},
		{name: "at minimum", min: "1.2.0", current: "1.2.0"},
		{name: "above minimum", min: "1.2.0", current: "1.3.0"},
		{name: "below minimum", min: "1.2.0", current: "1.1.9", wantErr: true},
		{name: "at maximum", max: "2.0.0", current: "2.0.0"},
		{name: "above maximum", max: "2.0.0", current: "2.0.1", wantErr: true},
		{name: "inside range", min: "1.0.0", max: "2.0.0", current: "1.5.0"},
		{name: "below range", min: "1.0.0", max: "2.0.0", current: "0.9.0", wantErr: true},
		{name: "above range", min: "1.0.0", max: "2.0.0", current: "2.1.0", wantErr: true},
		{name: "v prefixes accepted", min: "v1.2.0", current: "v1.2.3"},
		{name: "short form bound", min: "1.2", current: "1.2.0"},
		{name: "prerelease below its release", min: "1.2.0", current: "1.2.0-rc.1", wantErr: true},
		{name: "prerelease of higher release passes", min: "1.2.0", current: "1.3.0-rc.1"},
		{name: "build metadata ignored", min: "1.2.0", current: "1.2.0+build.7"},
		{name: "unparseable min fails open", min: "garbage", current: "0.0.1", wantWarn: "requiredMinimumVersion"},
		{name: "unparseable max fails open", max: "not.a.version", current: "99.0.0", wantWarn: "requiredMaximumVersion"},
		{name: "unparseable min still enforces max", min: "garbage", max: "0.0.5", current: "1.0.0", wantErr: true, wantWarn: "requiredMinimumVersion"},
		{name: "unparseable current fails open", min: "1.0.0", current: "dev", wantWarn: "current version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Resolved{
				RequiredMinimumVersion: tc.min,
				RequiredMaximumVersion: tc.max,
			}
			err := VersionGate(r, tc.current)
			if tc.wantErr && err == nil {
				t.Errorf("VersionGate(min=%q max=%q cur=%q) = nil, want error", tc.min, tc.max, tc.current)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("VersionGate(min=%q max=%q cur=%q) = %v, want nil", tc.min, tc.max, tc.current, err)
			}
			if err != nil && !strings.Contains(err.Error(), "managed policy") {
				t.Errorf("error %q does not mention the managed policy", err)
			}
			if tc.wantWarn != "" {
				found := false
				for _, w := range r.Warnings {
					if strings.Contains(w, tc.wantWarn) {
						found = true
					}
				}
				if !found {
					t.Errorf("warnings %v missing %q", r.Warnings, tc.wantWarn)
				}
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.2.3", "1.2.3", 0},
		{"v1.2.3", "1.2.3", 0},
		{"1.2", "1.2.0", 0},
		{"1", "1.0.0", 0},
		{"1.0.0+build.1", "1.0.0", 0},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1}, // numeric identifiers rank below alphanumeric
		{"1.0.0-alpha.beta", "1.0.0-beta", -1},
		{"1.0.0-beta.2", "1.0.0-beta.11", -1}, // numeric identifiers compare numerically
		{"1.0.0-rc.1", "1.0.0", -1},
	}
	for _, tc := range cases {
		av, err := parseVersion(tc.a)
		if err != nil {
			t.Fatalf("parseVersion(%q): %v", tc.a, err)
		}
		bv, err := parseVersion(tc.b)
		if err != nil {
			t.Fatalf("parseVersion(%q): %v", tc.b, err)
		}
		if got := compareVersions(av, bv); got != tc.want {
			t.Errorf("compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := compareVersions(bv, av); got != -tc.want {
			t.Errorf("compare(%q, %q) = %d, want %d (antisymmetry)", tc.b, tc.a, got, -tc.want)
		}
	}
}

func TestParseVersionErrors(t *testing.T) {
	for _, bad := range []string{"", "v", "abc", "1.2.3.4", "1.-2", "1.2.x", "1.2.3-"} {
		if _, err := parseVersion(bad); err == nil {
			t.Errorf("parseVersion(%q) succeeded, want error", bad)
		}
	}
}
