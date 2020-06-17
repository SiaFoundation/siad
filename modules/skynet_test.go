package modules

import "testing"

func TestSkyfileMetadata_ForPath(t *testing.T) {
	fullFilePath := "/foo/file.txt"
	fullMeta := SkyfileMetadata{
		Subfiles: SkyfileSubfiles{
			"foo_file": SkyfileSubfileMetadata{Filename: fullFilePath},
		},
	}
	// Find an exact match.
	subMeta, _, _, _ := fullMeta.ForPath(fullFilePath)
	if _, exists := subMeta.Subfiles[fullFilePath]; !exists {
		t.Fatal("Expected to find a file by its full path and name.")
	}
	// Find files by their directory.
	subMeta, _, _, _ = fullMeta.ForPath("/foo")
	if _, exists := subMeta.Subfiles[fullFilePath]; !exists {
		t.Fatal("Expected to find a file by its directory.")
	}
	// Find files by their directory, even if it contains a trailing slash.
	// This is a regression test.
	subMeta, _, _, _ = fullMeta.ForPath("/foo/")
	if _, exists := subMeta.Subfiles[fullFilePath]; !exists {
		t.Fatal(`Expected to find a file by its directory, even when followed by a "/".`)
	}
	// Find files by their directory, even if it's missing its leading slash.
	// This is a regression test.
	subMeta, _, _, _ = fullMeta.ForPath("foo")
	if _, exists := subMeta.Subfiles[fullFilePath]; !exists {
		t.Fatal(`Expected to find a file by its directory, even when it's missing its leading "/".`)
	}
}
