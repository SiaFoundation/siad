package modules

import (
	"testing"
)

// TestSiapathValidate verifies that the validate function correctly validates
// SiaPaths.
func TestSiapathValidate(t *testing.T) {
	var pathtests = []struct {
		in    string
		valid bool
	}{
		{"valid/siapath", true},
		{"../../../directory/traversal", false},
		{"testpath", true},
		{"valid/siapath/../with/directory/traversal", false},
		{"validpath/test", true},
		{"..validpath/..test", true},
		{"./invalid/path", false},
		{".../path", true},
		{"valid./path", true},
		{"valid../path", true},
		{"valid/path./test", true},
		{"valid/path../test", true},
		{"test/path", true},
		{"/leading/slash", false},
		{"foo/./bar", false},
		{"", false},
		{"blank/end/", false},
		{"double//dash", false},
		{"../", false},
		{"./", false},
		{".", false},
	}
	for _, pathtest := range pathtests {
		siaPath := SiaPath{
			Path: pathtest.in,
		}
		err := siaPath.validate()
		if err != nil && pathtest.valid {
			t.Fatal("validateSiapath failed on valid path: ", pathtest.in)
		}
		if err == nil && !pathtest.valid {
			t.Fatal("validateSiapath succeeded on invalid path: ", pathtest.in)
		}
	}
}

// TestSiapath tests that the NewSiaPath, LoadString, and Join methods function correctly
func TestSiapath(t *testing.T) {
	var pathtests = []struct {
		in    string
		valid bool
	}{
		{"valid/siapath", true},
		{"\\some\\windows\\path", true}, // clean converts OS separators
		{"../../../directory/traversal", false},
		{"testpath", true},
		{"valid/siapath/../with/directory/traversal", false},
		{"validpath/test", true},
		{"..validpath/..test", true},
		{"./invalid/path", false},
		{".../path", true},
		{"valid./path", true},
		{"valid../path", true},
		{"valid/path./test", true},
		{"valid/path../test", true},
		{"test/path", true},
		{"/leading/slash", true}, // clean will trim leading slashes so this is a valid input
		{"foo/./bar", false},
		{"", false},
		{"blank/end/", true}, // clean will trim trailing slashes so this is a valid input
		{"double//dash", false},
		{"../", false},
		{"./", false},
		{".", false},
	}

	// Test NewSiaPath
	for _, pathtest := range pathtests {
		_, err := NewSiaPath(pathtest.in)
		// Verify expected Error
		if err != nil && pathtest.valid {
			t.Fatal("validateSiapath failed on valid path: ", pathtest.in)
		}
		if err == nil && !pathtest.valid {
			t.Fatal("validateSiapath succeeded on invalid path: ", pathtest.in)
		}
	}

	// Test LoadString
	var sp SiaPath
	for _, pathtest := range pathtests {
		err := sp.LoadString(pathtest.in)
		// Verify expected Error
		if err != nil && pathtest.valid {
			t.Fatal("validateSiapath failed on valid path: ", pathtest.in)
		}
		if err == nil && !pathtest.valid {
			t.Fatal("validateSiapath succeeded on invalid path: ", pathtest.in)
		}
	}

	// Test Join
	sp, err := NewSiaPath("test")
	if err != nil {
		t.Fatal(err)
	}
	for _, pathtest := range pathtests {
		_, err = sp.Join(pathtest.in)
		// Verify expected Error
		if err != nil && pathtest.valid {
			t.Fatal("validateSiapath failed on valid path: ", pathtest.in)
		}
		if err == nil && !pathtest.valid {
			t.Fatal("validateSiapath succeeded on invalid path: ", pathtest.in)
		}
	}
}
