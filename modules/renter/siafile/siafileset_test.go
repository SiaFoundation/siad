package siafile

import "testing"

func newTestSiaFileSetWithFile() (*SiaFile, *SiaFileSet) {
	// Create SiaFileSet
	sfs := NewSiaFileSet()

	// Create new SiaFile
	siaFilePath, siaPath, source, rc, sk, fileSize, _, fileMode := newTestFileParams()
	sf, err := sfs.NewSiaFile(siaFilePath, siaPath, source, newTestWAL(), rc, sk, fileSize, fileMode)
	if err != nil {
		return nil, nil
	}
	return sf, sfs
}

// TestSiaFileSetOpenClose tests that the threadCount of the siafile is
// incremented and decremneted properly when Open() and Close() are called
func TestSiaFileSetOpenClose(t *testing.T) {
	// Create SiaFileSet with SiaFile
	sf, sfs := newTestSiaFileSetWithFile()
	siaPath := sf.SiaPath()

	// Confirm threadCount is incremented properly
	if sf.threadCount != 1 {
		t.Fatalf("Expected threadCount to be 1, got %v", sf.threadCount)
	}

	// Close siafile
	sfs.Close(sf)

	// Confirm that threadCount was decremented
	if sf.threadCount != 0 {
		t.Fatalf("Expected threadCount to be 0, got %v", sf.threadCount)
	}

	// Open siafile again and confirm threadCount was incremented
	sf, exists := sfs.Open(siaPath)
	if !exists {
		t.Fatal("SiaFile not found in SiaFileSet")
	}
	if sf.threadCount != 1 {
		t.Fatalf("Expected threadCount to be 1, got %v", sf.threadCount)
	}
}
