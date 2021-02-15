package modules

import (
	"runtime"
	"testing"

	"gitlab.com/NebulousLabs/errors"
)

var (
	// TestGlobalSiaPathVar tests that the NewGlobalSiaPath initialization
	// works.
	TestGlobalSiaPathVar SiaPath = NewGlobalSiaPath("/testdir")
)

// TestGlobalSiaPath checks that initializing a new global siapath does not
// cause any issues.
func TestGlobalSiaPath(t *testing.T) {
	sp, err := TestGlobalSiaPathVar.Join("testfile")
	if err != nil {
		t.Fatal(err)
	}
	mirror, err := NewSiaPath("/testdir")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := mirror.Join("testfile")
	if err != nil {
		t.Fatal(err)
	}
	if !sp.Equals(expected) {
		t.Error("the separately spawned siapath should equal the global siapath")
	}
}

// TestRandomSiaPath tests that RandomSiaPath always returns a valid SiaPath
func TestRandomSiaPath(t *testing.T) {
	for i := 0; i < 1000; i++ {
		err := RandomSiaPath().Validate(false)
		if err != nil {
			t.Fatal(err)
		}
	}
}

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
		err := ValidatePathString(pathtest.in, false)
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
		out   string
	}{
		{`\\some\\windows\\path`, true, `\\some\\windows\\path`}, // if the os is not windows this will not update the separators
		{"valid/siapath", true, "valid/siapath"},
		{`\some\back\slashes\path`, true, `\some\back\slashes\path`},
		{"../../../directory/traversal", false, ""},
		{"testpath", true, "testpath"},
		{"valid/siapath/../with/directory/traversal", false, ""},
		{"validpath/test", true, "validpath/test"},
		{"..validpath/..test", true, "..validpath/..test"},
		{"./invalid/path", false, ""},
		{".../path", true, ".../path"},
		{"valid./path", true, "valid./path"},
		{"valid../path", true, "valid../path"},
		{"valid/path./test", true, "valid/path./test"},
		{"valid/path../test", true, "valid/path../test"},
		{"test/path", true, "test/path"},
		{"/leading/slash", true, "leading/slash"}, // clean will trim leading slashes so this is a valid input
		{"foo/./bar", false, ""},
		{"", false, ""},
		{`\`, true, `\`},
		{`\\`, true, `\\`},
		{`\\\`, true, `\\\`},
		{`\\\\`, true, `\\\\`},
		{`\\\\\`, true, `\\\\\`},
		{"/", false, ""},
		{"//", false, ""},
		{"///", false, ""},
		{"////", false, ""},
		{"/////", false, ""},
		{"blank/end/", true, "blank/end"}, // clean will trim trailing slashes so this is a valid input
		{"double//dash", false, ""},
		{"../", false, ""},
		{"./", false, ""},
		{".", false, ""},
		{"dollar$sign", true, "dollar$sign"},
		{"and&sign", true, "and&sign"},
		{"single`quote", true, "single`quote"},
		{"full:colon", true, "full:colon"},
		{"semi;colon", true, "semi;colon"},
		{"hash#tag", true, "hash#tag"},
		{"percent%sign", true, "percent%sign"},
		{"at@sign", true, "at@sign"},
		{"less<than", true, "less<than"},
		{"greater>than", true, "greater>than"},
		{"equal=to", true, "equal=to"},
		{"question?mark", true, "question?mark"},
		{"open[bracket", true, "open[bracket"},
		{"close]bracket", true, "close]bracket"},
		{"open{bracket", true, "open{bracket"},
		{"close}bracket", true, "close}bracket"},
		{"carrot^top", true, "carrot^top"},
		{"pipe|pipe", true, "pipe|pipe"},
		{"tilda~tilda", true, "tilda~tilda"},
		{"plus+sign", true, "plus+sign"},
		{"minus-sign", true, "minus-sign"},
		{"under_score", true, "under_score"},
		{"comma,comma", true, "comma,comma"},
		{"apostrophy's", true, "apostrophy's"},
		{`quotation"marks`, true, `quotation"marks`},
	}
	// If the OS is windows then the windows path is valid and will be updated
	if runtime.GOOS == "windows" {
		pathtests[0].valid = true
		pathtests[0].out = `some/windows/path`
	}

	// Test NewSiaPath
	for _, pathtest := range pathtests {
		sp, err := NewSiaPath(pathtest.in)
		// Verify expected Error
		if err != nil && pathtest.valid {
			t.Fatal("validateSiapath failed on valid path: ", pathtest.in)
		}
		if err == nil && !pathtest.valid {
			t.Fatal("validateSiapath succeeded on invalid path: ", pathtest.in)
		}
		// Verify expected path
		if err == nil && pathtest.valid && sp.String() != pathtest.out {
			t.Fatalf("Unexpected SiaPath From New; got %v, expected %v, for test %v", sp.String(), pathtest.out, pathtest.in)
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
		// Verify expected path
		if err == nil && pathtest.valid && sp.String() != pathtest.out {
			t.Fatalf("Unexpected SiaPath from LoadString; got %v, expected %v, for test %v", sp.String(), pathtest.out, pathtest.in)
		}
	}

	// Test Join
	sp, err := NewSiaPath("test")
	if err != nil {
		t.Fatal(err)
	}
	for _, pathtest := range pathtests {
		newSiaPath, err := sp.Join(pathtest.in)
		// Verify expected Error
		if err != nil && pathtest.valid {
			t.Fatal("validateSiapath failed on valid path: ", pathtest.in)
		}
		if err == nil && !pathtest.valid {
			t.Fatal("validateSiapath succeeded on invalid path: ", pathtest.in)
		}
		// Verify expected path
		if err == nil && pathtest.valid && newSiaPath.String() != "test/"+pathtest.out {
			t.Fatalf("Unexpected SiaPath from Join; got %v, expected %v, for test %v", newSiaPath.String(), "test/"+pathtest.out, pathtest.in)
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

// TestSiapathDir probes the Dir function for SiaPaths.
func TestSiapathDir(t *testing.T) {
	var pathtests = []struct {
		path string
		dir  string
	}{
		{"one/dir", "one"},
		{"many/more/dirs", "many/more"},
		{"nodir", ""},
		{"/leadingslash", ""},
		{"./leadingdotslash", ""},
		{"", ""},
		{".", ""},
	}
	for _, pathtest := range pathtests {
		siaPath := SiaPath{
			Path: pathtest.path,
		}
		dir, err := siaPath.Dir()
		if err != nil {
			t.Errorf("Dir should not return an error %v, path %v", err, pathtest.path)
			continue
		}
		if dir.Path != pathtest.dir {
			t.Errorf("Dir %v not the same as expected dir %v ", dir.Path, pathtest.dir)
			continue
		}
	}
}

// TestSiapathName probes the Name function for SiaPaths.
func TestSiapathName(t *testing.T) {
	var pathtests = []struct {
		path string
		name string
	}{
		{"one/dir", "dir"},
		{"many/more/dirs", "dirs"},
		{"nodir", "nodir"},
		{"/leadingslash", "leadingslash"},
		{"./leadingdotslash", "leadingdotslash"},
		{"", ""},
		{".", ""},
	}
	for _, pathtest := range pathtests {
		siaPath := SiaPath{
			Path: pathtest.path,
		}
		name := siaPath.Name()
		if name != pathtest.name {
			t.Errorf("name %v not the same as expected name %v ", name, pathtest.name)
		}
	}
}
