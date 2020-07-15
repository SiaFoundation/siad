package modules

import (
	"encoding/json"
	"fmt"
	"testing"
)

// TestSkyfileMetadata_ForPath tests the behaviour of the ForPath method.
func TestSkyfileMetadata_ForPath(t *testing.T) {
	filePath1 := "/foo/file1.txt"
	filePath2 := "/foo/file2.txt"
	filePath3 := "/file3.txt"
	filePath4 := "/bar/file4.txt"
	filePath5 := "/bar/baz/file5.txt"
	fullMeta := SkyfileMetadata{
		Subfiles: SkyfileSubfiles{
			filePath1: SkyfileSubfileMetadata{Filename: filePath1, Offset: 1, Len: 1},
			filePath2: SkyfileSubfileMetadata{Filename: filePath2, Offset: 2, Len: 2},
			filePath3: SkyfileSubfileMetadata{Filename: filePath3, Offset: 3, Len: 3},
			filePath4: SkyfileSubfileMetadata{Filename: filePath4, Offset: 4, Len: 4},
			filePath5: SkyfileSubfileMetadata{Filename: filePath5, Offset: 5, Len: 5},
		},
	}
	emptyMeta := SkyfileMetadata{}

	// Find an exact match.
	subMeta, isSubFile, offset, size := fullMeta.ForPath(filePath1)
	if _, exists := subMeta.Subfiles[filePath1]; !exists {
		t.Fatal("Expected to find a file by its full path and name.")
	}
	if !isSubFile {
		t.Fatal("Expected to find a file, got a dir.")
	}
	if offset != 1 {
		t.Fatalf("Expected offset %d, got %d", 1, offset)
	}
	if size != 1 {
		t.Fatalf("Expected size %d, got %d", 1, size)
	}

	// Find files by their directory.
	subMeta, isSubFile, offset, size = fullMeta.ForPath("/foo")
	subfile1, exists1 := subMeta.Subfiles[filePath1]
	subfile2, exists2 := subMeta.Subfiles[filePath2]
	// Expect to find files 1 and 2 and nothing else.
	if !(exists1 && exists2 && len(subMeta.Subfiles) == 2) {
		t.Fatal("Expected to find two files by their directory.")
	}
	if isSubFile {
		t.Fatal("Expected to find a dir, got a file.")
	}
	if offset != 1 {
		t.Fatalf("Expected offset %d, got %d", 1, offset)
	}
	if subfile1.Offset != 0 {
		t.Fatalf("Expected offset %d, got %d", 0, subfile1.Offset)
	}
	if subfile2.Offset != 1 {
		t.Fatalf("Expected offset %d, got %d", 1, subfile2.Offset)
	}

	if size != 3 {
		t.Fatalf("Expected size %d, got %d", 3, size)
	}

	// Find files in the given directory and its subdirectories.
	subMeta, isSubFile, offset, size = fullMeta.ForPath("/bar")
	subfile4, exists4 := subMeta.Subfiles[filePath4]
	subfile5, exists5 := subMeta.Subfiles[filePath5]
	// Expect to find files 1 and 2 and nothing else.
	if !(exists4 && exists5 && len(subMeta.Subfiles) == 2) {
		t.Fatal("Expected to find two files by their directory.")
	}
	if isSubFile {
		t.Fatal("Expected to find a dir, got a file.")
	}
	if offset != 4 {
		t.Fatalf("Expected offset %d, got %d", 4, offset)
	}
	if subfile4.Offset != 0 {
		t.Fatalf("Expected offset %d, got %d", 0, subfile4.Offset)
	}
	if subfile5.Offset != 1 {
		t.Fatalf("Expected offset %d, got %d", 1, subfile5.Offset)
	}
	if size != 9 {
		t.Fatalf("Expected size %d, got %d", 9, size)
	}

	// Expect no files found on nonexistent path.
	subMeta, _, offset, size = fullMeta.ForPath("/nonexistent")
	if len(subMeta.Subfiles) > 0 {
		t.Fatal("Expected to not find any files on nonexistent path but found", len(subMeta.Subfiles))
	}
	if offset != 0 {
		t.Fatalf("Expected offset %d, got %d", 0, offset)
	}
	if size != 0 {
		t.Fatalf("Expected size %d, got %d", 0, size)
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

	// Try to find a file in an empty metadata struct.
	subMeta, _, offset, _ = emptyMeta.ForPath("foo")
	if len(subMeta.Subfiles) != 0 {
		t.Fatal(`Expected to not find any files, found`, len(subMeta.Subfiles))
	}
	if offset != 0 {
		t.Fatal(`Expected offset to be zero, got`, offset)
	}
}

// TestSkyfileMetadata_UnmarshalJSON tests the behaviour of UnmarshalJSON.
func TestSkyfileMetadata_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	fn := "img.jpg"
	ofn := "other.jpg"
	subdirfn := "subdir/file.jpg"
	tests := []struct {
		name        string
		metastring  string
		filename    string
		numfiles    int
		defaultPath string
	}{
		{
			name:        "single file",
			metastring:  fmt.Sprintf(`{"filename":"%s"}`, fn),
			filename:    fn,
			numfiles:    0,
			defaultPath: "",
		},
		{
			name:        "single file multipart",
			metastring:  fmt.Sprintf(`{"filename":"%s","subfiles":{"%s":{"filename":"%s","contenttype":"image/jpeg","len":190709}}}`, fn, fn, fn),
			filename:    fn,
			numfiles:    1,
			defaultPath: EnsurePrefix(fn, "/"),
		},
		{
			name:        "multi file default path set",
			metastring:  fmt.Sprintf(`{"filename":"%s","subfiles":{"%s":{"filename":"%s","contenttype":"image/jpeg","len":190709},"%s":{"filename":"%s","contenttype":"image/jpeg","len":190709}},"defaultpath":"/%s"}`, fn, fn, fn, ofn, ofn, fn),
			filename:    fn,
			numfiles:    2,
			defaultPath: EnsurePrefix(fn, "/"),
		},
		{
			name:        "multi file default path set to file in a subdir",
			metastring:  fmt.Sprintf(`{"filename":"%s","subfiles":{"%s":{"filename":"%s","contenttype":"image/jpeg","len":190709},"%s":{"filename":"%s","contenttype":"image/jpeg","len":190709}},"defaultpath":"/%s"}`, fn, fn, fn, subdirfn, subdirfn, subdirfn),
			filename:    fn,
			numfiles:    2,
			defaultPath: EnsurePrefix(subdirfn, "/"),
		},
		{
			name:        "multi file default path set empty",
			metastring:  fmt.Sprintf(`{"filename":"%s","subfiles":{"%s":{"filename":"%s","contenttype":"image/jpeg","len":190709},"%s":{"filename":"%s","contenttype":"image/jpeg","len":190709}},"defaultpath":""}`, fn, fn, fn, ofn, ofn),
			filename:    fn,
			numfiles:    2,
			defaultPath: "",
		},
		{
			name:        "multi file default path not set",
			metastring:  fmt.Sprintf(`{"filename":"%s","subfiles":{"%s":{"filename":"%s","contenttype":"image/jpeg","len":190709},"%s":{"filename":"%s","contenttype":"image/jpeg","len":190709}}}`, fn, fn, fn, ofn, ofn),
			filename:    fn,
			numfiles:    2,
			defaultPath: "",
		},
		{
			// This test aims to ensure that when unmarshalling the metadata we
			// will not mistakenly interpret the file named `defaultpath` as a
			// `defaultpath` entry in the metadata.
			name:        "special case subfile named defaultpath",
			metastring:  `{"filename":"defaultpath","subfiles":{"defaultpath":{"filename":"defaultpath","contenttype":"image/jpeg","len":190709}}}`,
			filename:    "defaultpath",
			numfiles:    1,
			defaultPath: "/defaultpath",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sm SkyfileMetadata
			err := json.Unmarshal([]byte(tt.metastring), &sm)
			if err != nil {
				t.Fatal("Failed to unmarshal:", err)
			}
			if sm.Filename != tt.filename {
				t.Fatalf("Bad filename, expected '%s', got '%s'.", tt.filename, sm.Filename)
			}
			if len(sm.Subfiles) != tt.numfiles {
				t.Fatalf("Bad number of subfiles, expected %d, got %d.", tt.numfiles, len(sm.Subfiles))
			}
			if sm.DefaultPath != tt.defaultPath {
				t.Fatalf("Bad default path, expected '%s', got '%s'.", tt.defaultPath, sm.DefaultPath)
			}
		})
	}
}
