package modules

import (
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/fastrand"
)

const (
	testSkylink = "AABEKWZ_wc2R9qlhYkzbG8mImFVi08kBu1nsvvwPLBtpEg"
)

// modulesTestDir creates a testing directory for the test
func modulesTestDir(testName string) string {
	path := build.TempDir("modules", testName)
	if err := os.MkdirAll(path, persist.DefaultDiskPermissionsTest); err != nil {
		panic(err)
	}
	return path
}

// TestBackupAndRestoreSkylink probes the backup and restore skylink methods
func TestBackupAndRestoreSkylink(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create common layout and metadata bytes
	sl := newTestSkyfileLayout()
	layoutBytes := sl.Encode()
	var sm SkyfileMetadata
	smBytes, err := SkyfileMetadataBytes(sm)
	if err != nil {
		t.Fatal(err)
	}

	// Helper function
	createFileAndTest := func(t *testing.T, baseSector []byte, fileData []byte, filename string) {
		// Create the file on disk
		dir := filepath.Dir(filename)
		err := os.MkdirAll(dir, persist.DefaultDiskPermissionsTest)
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filename)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		}()
		// Backup and Restore test
		testBackupAndRestore(t, baseSector, fileData, f)
	}

	// Small file test
	//
	// Create baseSector
	fileData := []byte("Super interesting skyfile data")
	baseSector, _ := BuildBaseSector(layoutBytes, nil, smBytes, fileData)
	// Create file on disk that the back up will be written to and then read from
	filename := filepath.Join(modulesTestDir(t.Name()), "small", SkylinkToSysPath(testSkylink))
	// Backup and Restore test
	createFileAndTest(t, baseSector, fileData, filename)

	// Large file test
	//
	// Create fanout to mock 2 chunks with 3 pieces each
	numChunks := 2
	numPieces := 3
	fanoutBytes := make([]byte, 0, numChunks*numPieces*crypto.HashSize)
	for ci := 0; ci < numChunks; ci++ {
		for pi := 0; pi < numPieces; pi++ {
			root := fastrand.Bytes(crypto.HashSize)
			fanoutBytes = append(fanoutBytes, root...)
		}
	}
	// Create baseSector
	baseSector, _ = BuildBaseSector(layoutBytes, fanoutBytes, smBytes, nil)
	// Create file on disk that the back up will be written to and then read from
	filename = filepath.Join(modulesTestDir(t.Name()), "large", SkylinkToSysPath(testSkylink))
	// Backup and Restore test
	size := 2 * int(SectorSize)
	fileData = fastrand.Bytes(size)
	createFileAndTest(t, baseSector, fileData, filename)
}

// testBackupAndRestore executes the test code for TestBackupAndRestoreSkylink
func testBackupAndRestore(t *testing.T, baseSector []byte, fileData []byte, backupFile *os.File) {
	// Create backup
	backupReader := bytes.NewReader(fileData)
	err := BackupSkylink(testSkylink, baseSector, backupReader, backupFile)
	if err != nil {
		t.Fatal(err)
	}

	// Seek to the beginning of the file
	_, err = backupFile.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatal(err)
	}

	// Restore
	skylinkStr, restoreBaseSector, err := RestoreSkylink(backupFile)
	if err != nil {
		t.Fatal(err)
	}
	if skylinkStr != testSkylink {
		t.Error("Skylink back", skylinkStr)
	}
	if !bytes.Equal(baseSector, restoreBaseSector) {
		t.Log("original baseSector:", baseSector)
		t.Log("restored baseSector:", restoreBaseSector)
		t.Fatal("BaseSector bytes not equal")
	}
	restoredData, err := ioutil.ReadAll(backupFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fileData, restoredData) {
		t.Log("original data:", fileData)
		t.Log("backup restoredData:", restoredData)
		t.Fatal("Data bytes not equal")
	}
}

// TestSkylinkToFromSysPath tests the SkylinkToSysPath and SkylinkFromSysPath
// functions
func TestSkylinkToFromSysPath(t *testing.T) {
	t.Parallel()
	expectedPath := filepath.Join("AA", "BE", "KW", "Z_wc2R9qlhYkzbG8mImFVi08kBu1nsvvwPLBtpEg")

	// Test creating a path
	path := SkylinkToSysPath(testSkylink)
	if path != expectedPath {
		t.Fatal("bad path:", path)
	}

	// Test creating the skylink
	skylinkStr := SkylinkFromSysPath(path)
	if testSkylink != skylinkStr {
		t.Fatal("bad skylink string:", skylinkStr)
	}

	// Test creating the skylink from an absolute path
	path = filepath.Join("many", "dirs", "in", "abs", "path", path)
	skylinkStr = SkylinkFromSysPath(path)
	if testSkylink != skylinkStr {
		t.Fatal("bad skylink string:", skylinkStr)
	}
}
