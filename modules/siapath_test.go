package modules

import (
	"testing"

	"gitlab.com/NebulousLabs/errors"
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
		// {"", false}, // TODO: Need to figure out how to clean up this test.
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
		err := siaPath.Validate(false)
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
		// {"", false}, // TODO: Need to figure out how to clean up this tests.
		{"blank/end/", true}, // clean will trim trailing slashes so this is a valid input
		{"double//dash", false},
		{"../", false},
		{"./", false},
		{".", false},
		{"dollar$sign", true},
		{"and&sign", true},
		{"single`quote", true},
		{"full:colon", true},
		{"semi;colon", true},
		{"hash#tag", true},
		{"percent%sign", true},
		{"at@sign", true},
		{"less<than", true},
		{"greater>than", true},
		{"equal=to", true},
		{"question?mark", true},
		{"open[bracket", true},
		{"close]bracket", true},
		{"open{bracket", true},
		{"close}bracket", true},
		{"carrot^top", true},
		{"pipe|pipe", true},
		{"tilda~tilda", true},
		{"plus+sign", true},
		{"minus-sign", true},
		{"under_score", true},
		{"comma,comma", true},
		{"apostrophy's", true},
		{`quotation"marks`, true},
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

// TestSiapathRebase tests the SiaPath.Rebase method.
func TestSiapathRebase(t *testing.T) {
	var rebasetests = []struct {
		oldBase string
		newBase string
		siaPath string
		result  string
	}{
		{"a/b", "a", "a/b/myfile", "a/myfile"}, // basic rebase
		{"a/b", "", "a/b/myfile", "myfile"},    // newBase is root
		{"", "b", "myfile", "b/myfile"},        // oldBase is root
		{"a/a", "a/b", "a/a", "a/b"},           // folder == oldBase
	}

	for _, test := range rebasetests {
		var oldBase, newBase SiaPath
		var err1, err2 error
		if test.oldBase == "" {
			oldBase = RootSiaPath()
		} else {
			oldBase, err1 = newSiaPath(test.oldBase)
		}
		if test.newBase == "" {
			newBase = RootSiaPath()
		} else {
			newBase, err2 = newSiaPath(test.newBase)
		}
		file, err3 := newSiaPath(test.siaPath)
		expectedPath, err4 := newSiaPath(test.result)
		if err := errors.Compose(err1, err2, err3, err4); err != nil {
			t.Fatal(err)
		}
		// Rebase the path
		res, err := file.Rebase(oldBase, newBase)
		if err != nil {
			t.Fatal(err)
		}
		// Check result.
		if !res.Equals(expectedPath) {
			t.Fatalf("'%v' doesn't match '%v'", res.String(), expectedPath.String())
		}
	}
}
