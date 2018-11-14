package siafile

import "testing"

// newTestSiaFileSetWithFile creates a new SiaFileSet and SiaFile and makes sure
// that they are linked
func newTestSiaFileSetWithFile() (*SiaFile, *SiaFileSet) {
	// Create SiaFileSet
	sfs := NewSiaFileSet()

	// Create new SiaFile
	siaFilePath, siaPath, source, rc, sk, fileSize, _, fileMode := newTestFileParams()
	sf, err := sfs.NewSiaFile(siaFilePath, siaPath, source, newTestWAL(), rc, sk, fileSize, fileMode, SiaFileTestThread)
	if err != nil {
		return nil, nil
	}
	return sf, sfs
}

// closeTestFile closes a test file that was created for the SiaFileSet so that
// the test doesn't need to know the ThreadType used to create the file
func (sfs *SiaFileSet) closeTestFile(sf *SiaFile) {
	sfs.Close(sf, SiaFileTestThread)
}

// TestSiaFileSetOpenClose tests that the threadCount of the siafile is
// incremented and decremneted properly when Open() and Close() are called
func TestSiaFileSetOpenClose(t *testing.T) {
	// Create SiaFileSet with SiaFile
	sf, sfs := newTestSiaFileSetWithFile()
	siaPath := sf.SiaPath()

	// Confirm threadCount is incremented properly
	if len(sf.threadMap) != 1 {
		t.Fatalf("Expected threadMap to be of length 1, got %v", len(sf.threadMap))
	}

	// Close siafile
	sfs.closeTestFile(sf)

	// Confirm that threadCount was decremented
	if len(sf.threadMap) != 0 {
		t.Fatalf("Expected threadCount to be 0, got %v", len(sf.threadMap))
	}

	// Open siafile again and confirm threadCount was incremented
	sf, err := sfs.Open(siaPath, SiaFileTestThread)
	if err != nil {
		t.Fatal(err)
	}
	if len(sf.threadMap) != 1 {
		t.Fatalf("Expected threadCount to be 1, got %v", len(sf.threadMap))
	}
}
