package update

import (
	"fmt"
	"strconv"
	"strings"
)

// semver holds a parsed vMAJOR.MINOR.PATCH version.
type semver struct {
	major, minor, patch int
}

// parseSemver parses "v1.2.3" or "1.2.3"; pre-release suffixes after "-" are
// ignored. Returns an error for non-numeric or short inputs.
func parseSemver(s string) (semver, error) {
	s = strings.TrimPrefix(s, "v")
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("not a semver: %q", s)
	}
	var v semver
	var err error
	if v.major, err = strconv.Atoi(parts[0]); err != nil {
		return semver{}, fmt.Errorf("not a semver: %q", s)
	}
	if v.minor, err = strconv.Atoi(parts[1]); err != nil {
		return semver{}, fmt.Errorf("not a semver: %q", s)
	}
	// Strip pre-release suffix from patch.
	patch := strings.SplitN(parts[2], "-", 2)[0]
	if v.patch, err = strconv.Atoi(patch); err != nil {
		return semver{}, fmt.Errorf("not a semver: %q", s)
	}
	return v, nil
}

// newerThan reports whether v is strictly greater than o.
func (v semver) newerThan(o semver) bool {
	if v.major != o.major {
		return v.major > o.major
	}
	if v.minor != o.minor {
		return v.minor > o.minor
	}
	return v.patch > o.patch
}
