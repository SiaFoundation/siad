package modules

import (
	"bytes"
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
	t.Parallel()

	// Create common layout and metadata bytes
	sl := newTestSkyfileLayout()
	layoutBytes := sl.Encode()
	var sm SkyfileMetadata
	smBytes, err := SkyfileMetadataBytes(sm)
	if err != nil {
		t.Fatal(err)
	}

	// Small file test
	//
	// Create baseSector
	fileData := []byte("Super interesting skyfile data")
	baseSector, _ := BuildBaseSector(layoutBytes, nil, smBytes, fileData)
	// Backup and Restore test
	testBackupAndRestore(t, baseSector, fileData)

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
	// Backup and Restore test
	size := 2 * int(SectorSize)
	fileData = fastrand.Bytes(size)
	testBackupAndRestore(t, baseSector, fileData)
}

// testBackupAndRestore executes the test code for TestBackupAndRestoreSkylink
func testBackupAndRestore(t *testing.T, baseSector []byte, fileData []byte) {
	// Create backup
	backupReader := bytes.NewReader(fileData)
	var buf bytes.Buffer
	err := BackupSkylink(testSkylink, baseSector, backupReader, &buf)
	if err != nil {
		t.Fatal(err)
	}

	// Restore
	restoreReader := bytes.NewBuffer(buf.Bytes())
	skylinkStr, restoreBaseSector, err := RestoreSkylink(restoreReader)
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
	restoredData, err := ioutil.ReadAll(restoreReader)
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
