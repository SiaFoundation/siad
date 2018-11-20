package siafile

import (
	"path/filepath"
	"strings"
	"testing"
)

// newTestSiaFileSetWithFile creates a new SiaFileSet and SiaFile and makes sure
// that they are linked
func newTestSiaFileSetWithFile() (*SiaFile, *SiaFileSet) {
	// Create SiaFileSet
	sfs := NewSiaFileSet()
	sfs.AssignWAL(newTestWAL())

	// Create new SiaFile
	siaFilePath, siaPath, source, rc, sk, fileSize, _, fileMode := newTestFileParams()
	entry, err := sfs.NewSiaFile(siaFilePath, siaPath, source, rc, sk, fileSize, fileMode, SiaFileTestThread)
	if err != nil {
		return nil, nil
	}
	return entry.SiaFile(), sfs
}

// closeTestFile closes a test file that was created for the SiaFileSet so that
// the test doesn't need to know the ThreadType used to create the file
func (entry *SiaFileSetEntry) closeTestFile() {
	entry.Close(SiaFileTestThread)
}

// TestSiaFileSetOpenClose tests that the threadCount of the siafile is
// incremented and decremneted properly when Open() and Close() are called
func TestSiaFileSetOpenClose(t *testing.T) {
	// Create SiaFileSet with SiaFile
	sf, sfs := newTestSiaFileSetWithFile()
	siaPath := sf.SiaPath()
	entry, ok := sfs.siaFileMap[siaPath]
	if !ok {
		t.Fatal("No SiaFileSetEntry found")
	}

	// Confirm file is in memory
	sfs.mu.Lock()
	if len(sfs.siaFileMap) != 1 {
		t.Fatalf("Expected SiaFileSet map to be of length 1, instead is length %v", len(sfs.siaFileMap))
	}
	sfs.mu.Unlock()

	// Confirm threadCount is incremented properly
	if len(entry.threadMap) != 1 {
		t.Fatalf("Expected threadMap to be of length 1, got %v", len(entry.threadMap))
	}

	// Record siafile path
	path := sf.siaFilePath

	// Close siafile
	entry.closeTestFile()

	// Confirm that threadCount was decremented
	if len(entry.threadMap) != 0 {
		t.Fatalf("Expected threadCount to be 0, got %v", len(entry.threadMap))
	}

	// Confirm file was removed from memory
	sfs.mu.Lock()
	if len(sfs.siaFileMap) != 0 {
		t.Fatalf("Expected SiaFileSet map to be empty, instead is length %v", len(sfs.siaFileMap))
	}
	sfs.mu.Unlock()

	// Open siafile again and confirm threadCount was incremented
	dir := filepath.Dir(strings.TrimSuffix(path, ShareExtension))
	entry, err := sfs.Open(siaPath, dir, SiaFileTestThread)
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.threadMap) != 1 {
		t.Fatalf("Expected threadCount to be 1, got %v", len(entry.threadMap))
	}
}
