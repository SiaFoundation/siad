package modules

import (
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestSkynetHelpers is a convenience function that wraps all of the Skynet
// helper tests, this ensures these tests are ran when supplying `-run
// TestSkynet` from the command line.
func TestSkynetHelpers(t *testing.T) {
	t.Run("ValidateDefaultPath", testValidateDefaultPath)
	t.Run("ValidateSkyfileMetadata", testValidateSkyfileMetadata)
	t.Run("EnsurePrefix", testEnsurePrefix)
	t.Run("EnsureSuffix", testEnsureSuffix)
}

// testValidateDefaultPath ensures the functionality of 'validateDefaultPath'
func testValidateDefaultPath(t *testing.T) {
	t.Parallel()

	subfiles := func(filenames ...string) SkyfileSubfiles {
		md := make(SkyfileSubfiles)
		for _, fn := range filenames {
			md[fn] = SkyfileSubfileMetadata{Filename: fn}
		}
		return md
	}

	tests := []struct {
		name       string
		dpQuery    string
		dpExpected string
		subfiles   SkyfileSubfiles
		err        string
	}{
		{
			name:       "empty default path - no files",
			subfiles:   nil,
			dpQuery:    "",
			dpExpected: "",
			err:        "",
		},
		{
			name:       "no default path - files",
			subfiles:   subfiles("a.html"),
			dpQuery:    "",
			dpExpected: "",
			err:        "",
		},
		{
			name:       "existing default path",
			subfiles:   subfiles("a.html"),
			dpQuery:    "/a.html",
			dpExpected: "/a.html",
			err:        "",
		},
		{
			name:       "existing default path - multiple subfiles",
			subfiles:   subfiles("a.html", "b.html"),
			dpQuery:    "/a.html",
			dpExpected: "/a.html",
			err:        "",
		},
		{
			name:       "existing default path - ensure leading slash",
			subfiles:   subfiles("a.html"),
			dpQuery:    "a.html",
			dpExpected: "/a.html",
			err:        "",
		},
		{
			name:       "non existing default path",
			subfiles:   subfiles("b.html"),
			dpQuery:    "a.html",
			dpExpected: "",
			err:        "no such path",
		},
		{
			name:       "non html default path",
			subfiles:   subfiles("a.txt"),
			dpQuery:    "a.txt",
			dpExpected: "",
			err:        "the default path must point to an HTML file",
		},
		{
			name:       "HTML file with extension 'htm' as default path",
			subfiles:   subfiles("a.htm"),
			dpQuery:    "a.htm",
			dpExpected: "/a.htm",
			err:        "",
		},
		{
			name:       "default path not at root",
			subfiles:   subfiles("a/b/c.html"),
			dpQuery:    "a/b/c.html",
			dpExpected: "",
			err:        "the default path must point to a file in the root directory of the skyfile",
		},
	}

	for _, subtest := range tests {
		t.Run(subtest.name, func(t *testing.T) {
			dp, err := validateDefaultPath(subtest.dpQuery, subtest.subfiles)
			if subtest.err != "" && !strings.Contains(err.Error(), subtest.err) {
				t.Fatal("Unexpected error", subtest.err)
			}
			if subtest.err == "" && err != nil {
				t.Fatal("Unexpected error", err)
			}
			if dp != subtest.dpExpected {
				t.Fatal("Unexpected default path")
			}
		})
	}
}

// testValidateSkyfileMetadata verifies the functionality of
// `ValidateSkyfileMetadata`
func testValidateSkyfileMetadata(t *testing.T) {
	t.Parallel()

	// happy case
	metadata := SkyfileMetadata{
		Filename: t.Name(),
		Length:   fastrand.Uint64n(10) + 1,
		Subfiles: SkyfileSubfiles{
			"validkey": SkyfileSubfileMetadata{
				Filename: "validkey",
			},
		},
	}
	err := ValidateSkyfileMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}

	// verify invalid filename
	invalid := metadata
	invalid.Filename = "../../" + metadata.Filename
	err = ValidateSkyfileMetadata(invalid)
	if err == nil || !strings.Contains(err.Error(), "invalid filename provided") {
		t.Fatal("unexpected outcome")
	}

	// verify invalid subfile metadata
	invalid = metadata
	invalid.Subfiles = SkyfileSubfiles{
		"invalidkey": SkyfileSubfileMetadata{
			Filename: "keyshouldmatchfilename",
		},
	}
	err = ValidateSkyfileMetadata(invalid)
	if err == nil || !strings.Contains(err.Error(), "subfile name did not match") {
		t.Fatal("unexpected outcome")
	}

	// verify invalid subfile metadata
	invalid = metadata
	invalid.Subfiles = SkyfileSubfiles{
		"foo/../bar": SkyfileSubfileMetadata{
			Filename: "foo/../bar",
		},
	}
	err = ValidateSkyfileMetadata(invalid)
	if err == nil || !strings.Contains(err.Error(), "invalid filename provided for subfile") {
		t.Fatal("unexpected outcome")
	}

	// verify invalid default path
	invalid = metadata
	invalid.DefaultPath = "foo/../bar"
	err = ValidateSkyfileMetadata(invalid)
	if !errors.Contains(err, ErrInvalidDefaultPath) {
		t.Fatal("unexpected outcome")
	}

	invalid.DisableDefaultPath = true
	err = ValidateSkyfileMetadata(invalid)
	if err != nil {
		t.Fatal("unexpected outcome")
	}
}

// testEnsurePrefix ensures EnsurePrefix is properly adding prefixes.
func testEnsurePrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		str string
		pre string
		out string
	}{
		{"base", "pre", "prebase"},
		{"base", "", "base"},
		{"rebase", "pre", "prerebase"},
		{"", "pre", "pre"},
		{"", "", ""},
	}
	for _, tt := range tests {
		out := EnsurePrefix(tt.str, tt.pre)
		if out != tt.out {
			t.Errorf("Expected string %s and prefix %s to result in %s but got %s\n", tt.str, tt.pre, tt.out, out)
		}
	}
}

// testEnsureSuffix ensures EnsureSuffix is properly adding suffixes.
func testEnsureSuffix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		str string
		suf string
		out string
	}{
		{"base", "suf", "basesuf"},
		{"base", "", "base"},
		{"basesu", "suf", "basesusuf"},
		{"", "suf", "suf"},
		{"", "", ""},
	}
	for _, tt := range tests {
		out := EnsureSuffix(tt.str, tt.suf)
		if out != tt.out {
			t.Errorf("Expected string %s and suffix %s to result in %s but got %s\n", tt.str, tt.suf, tt.out, out)
		}
	}
}
