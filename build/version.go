package build

import (
	"strconv"
	"strings"
)

const (
	// MaxEncodedVersionLength is the maximum length of a version string encoded
	// with the encode package. 100 is much larger than any version number we send
	// now, but it allows us to send additional information in the version string
	// later if we choose. For example appending the version string with the HEAD
	// commit hash.
	MaxEncodedVersionLength = 100
)

var (
	// BinaryName is the name of the node binary. It is supplied at compile time
	// via ldflags.
	BinaryName = "unknown"

	// NodeVersion is the current version of the node software. It is supplied
	// at compile time via ldflags.
	NodeVersion = "?.?.?"
)

// IsVersion returns whether str is a valid release version with no -rc component.
func IsVersion(str string) bool {
	for _, n := range strings.Split(str, ".") {
		if _, err := strconv.Atoi(n); err != nil {
			return false
		}
	}
	return true
}

// min returns the smaller of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// splitVersion splits a version string into it's version and optional rc component.
// full releases are considered rc 0.
func splitVersion(v string) (version []int, rc int) {
	parts := strings.Split(v, "-rc")
	for _, s := range strings.Split(parts[0], ".") {
		n, _ := strconv.Atoi(s)
		version = append(version, n)
	}
	if len(parts) == 1 { // if we don't have an rc part, we're done
		return
	} else if len(parts[1]) == 0 { // -rc is equivalent to -rc1 since rc0 is a full release
		return version, 1
	}

	rc, _ = strconv.Atoi(parts[1])
	return
}

// VersionCmp returns an int indicating the difference between a and b. It
// follows the convention of bytes.Compare and big.Cmp:
//
//	-1 if a <  b
//	 0 if a == b
//	+1 if a >  b
//
// One important quirk is that "1.1.0" is considered newer than "1.1", despite
// being numerically equal.
func VersionCmp(a, b string) int {
	va, rca := splitVersion(a)
	vb, rcb := splitVersion(b)

	for i := 0; i < min(len(va), len(vb)); i++ {
		if va[i] < vb[i] {
			return -1
		} else if va[i] > vb[i] {
			return 1
		}
	}

	switch {
	case len(va) < len(vb): // a has fewer digits than b
		return -1
	case len(va) > len(vb): // a has more digits than b
		return 1
	case rca == rcb: // length is equal and rcs are equal
		return 0
	case rca == 0: // a is a full release
		return 1
	case rcb == 0: // b is a full release
		return -1
	case rca > rcb:
		return 1
	case rca < rcb:
		return -1
	}

	return 0
}
