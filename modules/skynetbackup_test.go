package modules

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
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

	// Small file test
	backupDir := modulesTestDir(filepath.Join(t.Name(), "smallfile"))
	data := []byte("Super interesting skyfile data")
	var sm SkyfileMetadata
	testBackupAndRestore(t, backupDir, data, newTestSkyfileLayout(), sm)

	// Large file test
	backupDir = modulesTestDir(filepath.Join(t.Name(), "largefile"))
	// NOTE: Manually creating Fuzz() since siatest would create an import cycle.
	// Could refactor Fuzz() out of siatest later on.
	size := int(SectorSize)*2 + fastrand.Intn(3) - 1
	data = fastrand.Bytes(size)
	subFiles := make(map[string]SkyfileSubfileMetadata)
	sfm := SkyfileSubfileMetadata{
		FileMode:    os.ModeAppend,
		Filename:    "subfile",
		ContentType: "secret",
		Offset:      100,
		Len:         100,
	}
	subFiles[sfm.Filename] = sfm
	sm = SkyfileMetadata{
		Filename:           "backupfile",
		Length:             uint64(size),
		Mode:               os.ModeDir,
		Subfiles:           subFiles,
		DefaultPath:        "thesamepath",
		DisableDefaultPath: true,
	}
	testBackupAndRestore(t, backupDir, data, newTestSkyfileLayout(), sm)
}

// testBackupAndRestore executes the test code for TestBackupAndRestoreSkylink
func testBackupAndRestore(t *testing.T, backupDir string, data []byte, sl SkyfileLayout, sm SkyfileMetadata) {
	// Create the reader
	reader := ioutil.NopCloser(bytes.NewReader(data))
	defer reader.Close() // no-op so ignoring error

	// Create backup
	backupFilePath, err := BackupSkylink(testSkylink, backupDir, reader, sl, sm)
	if err != nil {
		t.Fatal(err)
	}
	if backupFilePath != filepath.Join(backupDir, SkylinkToSysPath(testSkylink)) {
		t.Fatal("bad backup path", backupFilePath)
	}

	// Restore
	skylinkStr, backupReader, backupSL, backupSM, err := RestoreSkylink(backupFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if skylinkStr != testSkylink {
		t.Error("Skylink back", skylinkStr)
	}
	if !reflect.DeepEqual(sl, backupSL) {
		t.Log("original sl:", sl)
		t.Log("backup sl:", backupSL)
		t.Error("Bad Skyfile Layout")
	}
	if !reflect.DeepEqual(sm, backupSM) {
		t.Log("original sm:", sm)
		t.Log("backup sm:", backupSM)
		t.Error("Bad Skyfile Metadata")
	}
	backupData, err := ioutil.ReadAll(backupReader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, backupData) {
		t.Log("original data:", data)
		t.Log("backup data:", backupData)
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
