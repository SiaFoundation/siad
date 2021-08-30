package build

import (
	"testing"
)

// TestVersionCmp checks that in all cases, VersionCmp returns the correct
// result.
func TestVersionCmp(t *testing.T) {
	versionTests := []struct {
		a, b string
		exp  int
	}{
		{"0.1", "0.0.9", 1},
		{"0.1", "0.1", 0},
		{"0.1", "0.1.1", -1},
		{"0.1", "0.1.0", -1},
		{"0.1", "1.1", -1},
		{"0.1.1.0", "0.1.1", 1},
		{"2.3.7", "2.3.7-rc1", 1},
		{"2.3.7-rc2", "2.3.7", -1},
		{"1.1.0-rc2", "1.1.0-rc1", 1},
		{"1.1.0-rc1", "1.1.0-rc2", -1},
		{"2.5.5", "2.5.4-rc1", 1},
		{"2.5.4-rc1", "2.5.5", -1},
		{"2.5.4-rc1", "2.5.4-rc1", 0},
		{"2.5.4.0-rc1", "2.5.4-rc1", 1},
		{"2.5.4-rc1", "2.5.4.0-rc1", -1},
		// version strings are unvalidated
		{"x.y.z", "2.5.4", -1},
		{"1.0.0", "x.y.z-rc1", 1},
		{"0.0.0-rc1", "x.y.z-rc1", 0},
		{"rc0", "rc1", 0},
	}

	for _, test := range versionTests {
		if actual := VersionCmp(test.a, test.b); actual != test.exp {
			t.Errorf("Comparing %v to %v should return %v (got %v)", test.a, test.b, test.exp, actual)
		}
	}
}

// TestIsVersion tests the IsVersion function.
func TestIsVersion(t *testing.T) {
	versionTests := []struct {
		str string
		exp bool
	}{
		{"1.0", true},
		{"1", true},
		{"0.1.2.3.4.5", true},
		{"2.5.4", true},

		{"foo", false},
		{".1", false},
		{"1.", false},
		{"a.b", false},
		{"1.o", false},
		{".", false},
		{"", false},
		{"-rc1", false},
		{"2.5.6-rc1", false},
		{"2.5.6-rc1-rc1", false},
	}

	for _, test := range versionTests {
		if IsVersion(test.str) != test.exp {
			t.Errorf("IsVersion(%v) should return %v", test.str, test.exp)
		}
	}
}
