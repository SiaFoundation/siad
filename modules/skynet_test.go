package modules

import "testing"

// TestSkyfileMetadata_ForPath tests the behaviour of the ForPath method.
func TestSkyfileMetadata_ForPath(t *testing.T) {
	filePath1 := "/foo/file1.txt"
	filePath2 := "/foo/file2.txt"
	filePath3 := "/file3.txt"
	fullMeta := SkyfileMetadata{
		Subfiles: SkyfileSubfiles{
			filePath1: SkyfileSubfileMetadata{Filename: filePath1},
			filePath2: SkyfileSubfileMetadata{Filename: filePath2},
			filePath3: SkyfileSubfileMetadata{Filename: filePath3},
		},
	}
	// Find an exact match.
	subMeta, _, _, _ := fullMeta.ForPath(filePath1)
	if _, exists := subMeta.Subfiles[filePath1]; !exists {
		t.Fatal("Expected to find a file by its full path and name.")
	}
	// Find files by their directory.
	subMeta, _, _, _ = fullMeta.ForPath("/foo")
	_, exists1 := subMeta.Subfiles[filePath1]
	_, exists2 := subMeta.Subfiles[filePath2]
	_, exists3 := subMeta.Subfiles[filePath3]
	// We expect files 1 and 2 to exist and 3 to not exist.
	if !(exists1 && exists2 && !exists3) {
		t.Fatal("Expected to find two files by their directory.")
	}
	// Find files by their directory, even if it contains a trailing slash.
	// This is a regression test.
	subMeta, _, _, _ = fullMeta.ForPath("/foo/")
	if _, exists := subMeta.Subfiles[filePath1]; !exists {
		t.Fatal(`Expected to find a file by its directory, even when followed by a "/".`)
	}
	subMeta, _, _, _ = fullMeta.ForPath("foo/")
	if _, exists := subMeta.Subfiles[filePath1]; !exists {
		t.Fatal(`Expected to find a file by its directory, even when missing leading "/" and followed by a "/".`)
	}
	// Find files by their directory, even if it's missing its leading slash.
	// This is a regression test.
	subMeta, _, _, _ = fullMeta.ForPath("foo")
	if _, exists := subMeta.Subfiles[filePath1]; !exists {
		t.Fatal(`Expected to find a file by its directory, even when it's missing its leading "/".`)
	}
}
