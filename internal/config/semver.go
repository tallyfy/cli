package config

import (
	"fmt"
	"strconv"
	"strings"
)

// VersionGate enforces the managed requiredMinimumVersion /
// requiredMaximumVersion bounds against the running CLI version. It is
// FAIL-OPEN: an unparseable bound (or current version) appends a warning to
// r.Warnings and does not block — a policy typo must never brick a fleet.
// When the running version falls outside a parseable bound, the returned
// error tells the user the managed policy requires a version range.
//
// Note: implemented with an internal semver-ish comparator because
// golang.org/x/mod is not a module dependency (adding dependencies is out of
// scope for this package).
func VersionGate(r *Resolved, current string) error {
	minBound, maxBound := r.RequiredMinimumVersion, r.RequiredMaximumVersion
	if minBound == "" && maxBound == "" {
		return nil
	}
	cur, err := parseVersion(current)
	if err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"version gate skipped: cannot parse current version %q: %v", current, err))
		return nil
	}
	rangeDesc := describeRange(minBound, maxBound)
	if minBound != "" {
		lo, err := parseVersion(minBound)
		if err != nil {
			r.Warnings = append(r.Warnings, fmt.Sprintf(
				"version gate: cannot parse requiredMinimumVersion %q (%v); minimum not enforced", minBound, err))
		} else if compareVersions(cur, lo) < 0 {
			return fmt.Errorf(
				"tallyfy %s is older than the version range your organization's managed policy requires (%s); update the CLI (`tallyfy update`) or contact your administrator",
				current, rangeDesc)
		}
	}
	if maxBound != "" {
		hi, err := parseVersion(maxBound)
		if err != nil {
			r.Warnings = append(r.Warnings, fmt.Sprintf(
				"version gate: cannot parse requiredMaximumVersion %q (%v); maximum not enforced", maxBound, err))
		} else if compareVersions(cur, hi) > 0 {
			return fmt.Errorf(
				"tallyfy %s is newer than the version range your organization's managed policy requires (%s); install a permitted version or contact your administrator",
				current, rangeDesc)
		}
	}
	return nil
}

func describeRange(minBound, maxBound string) string {
	switch {
	case minBound != "" && maxBound != "":
		return fmt.Sprintf("requires version >= %s and <= %s", minBound, maxBound)
	case minBound != "":
		return fmt.Sprintf("requires version >= %s", minBound)
	default:
		return fmt.Sprintf("requires version <= %s", maxBound)
	}
}

// version is a parsed semver-ish version: MAJOR[.MINOR[.PATCH]][-PRERELEASE].
// Build metadata (+...) is parsed and ignored, per semver.
type version struct {
	nums [3]int64
	pre  []string
}

// parseVersion accepts an optional leading "v"/"V", 1-3 numeric dotted core
// components (missing components are zero), an optional pre-release, and
// optional build metadata.
func parseVersion(s string) (version, error) {
	v := version{}
	s = strings.TrimSpace(s)
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		s = s[1:]
	}
	if s == "" {
		return v, fmt.Errorf("empty version")
	}
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i] // build metadata never affects precedence
	}
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) > 3 {
		return v, fmt.Errorf("expected MAJOR[.MINOR[.PATCH]], got %d components", len(parts))
	}
	for i, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 {
			return v, fmt.Errorf("non-numeric version component %q", p)
		}
		v.nums[i] = n
	}
	if hasPre {
		if pre == "" {
			return v, fmt.Errorf("empty pre-release")
		}
		v.pre = strings.Split(pre, ".")
	}
	return v, nil
}

// compareVersions returns -1, 0, or 1 per semver precedence rules
// (semver.org §11), including pre-release ordering.
func compareVersions(a, b version) int {
	for i := 0; i < 3; i++ {
		switch {
		case a.nums[i] < b.nums[i]:
			return -1
		case a.nums[i] > b.nums[i]:
			return 1
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1 // a release outranks any of its pre-releases
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		if c := comparePreID(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.pre) < len(b.pre):
		return -1 // shorter pre-release set has lower precedence
	case len(a.pre) > len(b.pre):
		return 1
	}
	return 0
}

// comparePreID compares one pre-release identifier pair: numeric identifiers
// compare numerically and rank below alphanumeric ones; alphanumerics compare
// in ASCII order.
func comparePreID(x, y string) int {
	xn, xerr := strconv.ParseInt(x, 10, 64)
	yn, yerr := strconv.ParseInt(y, 10, 64)
	switch {
	case xerr == nil && yerr == nil:
		switch {
		case xn < yn:
			return -1
		case xn > yn:
			return 1
		}
		return 0
	case xerr == nil:
		return -1
	case yerr == nil:
		return 1
	default:
		return strings.Compare(x, y)
	}
}
