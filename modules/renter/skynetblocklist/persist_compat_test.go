package skynetblocklist

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

/* TODO
*
* test v150 blacklist to v150 blocklist
* test v143 blacklist to v150 bloclist
*
 */

// TestPersistCompatBlacklistToBlocklist tests converting the v1.5.0 skynet
// blacklist persistence to the v1.5.1 skynet blocklist persistence
func TestPersistCompatBlacklistToBlocklist(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	testdir := testDir(t.Name())

	testPersistCompat(t, testdir, blacklistPersistFile, persistFile, blacklistMetadataHeader, metadataHeader, persist.MetadataVersionv150, metadataVersion)
}

// TestPersistCompatv143Tov150 tests converting the skynet blacklist persistence
// from v1.4.3 to v1.5.0
func TestPersistCompatv143Tov150(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	testdir := testDir(t.Name())

	testPersistCompat(t, testdir, blacklistPersistFile, blacklistPersistFile, blacklistMetadataHeader, blacklistMetadataHeader, metadataVersionV143, persist.MetadataVersionv150)
}

// testPersistCompat tests the persist compat code going between two versions
func testPersistCompat(t *testing.T, testdir, oldPersistFile, newPersistFile string, oldHeader, newHeader, oldVersion, newVersion types.Specifier) {
	// Test 1: Clean conversion

	// Create sub test directory
	subTestDir := filepath.Join(testdir, "CleanConvert")
	err := os.MkdirAll(subTestDir, modules.DefaultDirPerm)
	if err != nil {
		t.Fatal(err)
	}

	// Initialize the directory with the old version persist file
	err = loadCompatPersistFile(subTestDir, oldVersion)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the persistence
	err = loadAndVerifyPersistence(subTestDir, oldPersistFile, newPersistFile, oldHeader, newHeader, oldVersion, newVersion)
	if err != nil {
		t.Fatal(err)
	}

	// Test 2A: Empty Temp File Exists

	// Create sub test directory
	subTestDir = filepath.Join(testdir, "EmptyTempFile")
	err = os.MkdirAll(subTestDir, modules.DefaultDirPerm)
	if err != nil {
		t.Fatal(err)
	}

	// Initialize the directory with the old version persist file
	err = loadCompatPersistFile(subTestDir, oldVersion)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a crash during the creation a temporary file by creating an empty
	// temp file
	f, err := os.Create(filepath.Join(subTestDir, tempPersistFileName(oldPersistFile)))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Verify the persistence
	err = loadAndVerifyPersistence(subTestDir, oldPersistFile, newPersistFile, oldHeader, newHeader, oldVersion, newVersion)
	if err != nil {
		t.Fatal(err)
	}

	// Test 2B: Temp File Exists with an invalid checksum

	// Create sub test directory
	subTestDir = filepath.Join(testdir, "InvalidChecksum")
	err = os.MkdirAll(subTestDir, modules.DefaultDirPerm)
	if err != nil {
		t.Fatal(err)
	}

	// Initialize the directory with the old version persist file
	err = loadCompatPersistFile(subTestDir, oldVersion)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a crash during the creation a temporary file by creating a temp
	// file with random bytes
	f, err = os.Create(filepath.Join(subTestDir, tempPersistFileName(oldPersistFile)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write(fastrand.Bytes(100))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Verify the persistence
	err = loadAndVerifyPersistence(subTestDir, oldPersistFile, newPersistFile, oldHeader, newHeader, oldVersion, newVersion)
	if err != nil {
		t.Fatal(err)
	}

	// Test 3: Temp File Exists with a valid checksum

	// Create sub test directory
	subTestDir = filepath.Join(testdir, "ValidChecksum")
	err = os.MkdirAll(subTestDir, modules.DefaultDirPerm)
	if err != nil {
		t.Fatal(err)
	}

	// Initialize the directory with the old version persist file
	err = loadCompatPersistFile(subTestDir, oldVersion)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a crash after creating a temporary file
	_, err = createTempFileFromPersistFile(subTestDir, oldPersistFile, oldHeader, oldVersion)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the persistence
	err = loadAndVerifyPersistence(subTestDir, oldPersistFile, newPersistFile, oldHeader, newHeader, oldVersion, newVersion)
	if err != nil {
		t.Fatal(err)
	}
}

// loadAndVerifyPersistence loads the persistence and verifies that the
// conversion updated the persistence as expected
func loadAndVerifyPersistence(testDir, oldPersistFile, newPersistFile string, oldHeader, newHeader, oldVersion, newVersion types.Specifier) error {
	// Verify that loading the older persist file works
	aop, reader, err := persist.NewAppendOnlyPersist(testDir, oldPersistFile, oldHeader, oldVersion)
	if err != nil {
		return errors.AddContext(err, "unable to open old persist file")
	}

	// Grab the old persistence
	oldPersistence, err := unmarshalObjects(reader)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal old persistence")
	}
	if len(oldPersistence) == 0 {
		return errors.New("no data in old version's persist file")
	}

	// Close the original AOP
	err = aop.Close()
	if err != nil {
		return errors.AddContext(err, "unable to close old aop")
	}

	// Convert the persistence based on the old version
	switch oldVersion {
	case metadataVersionV143:
		err = convertPersistVersionFromv143Tov150(testDir)
	case persist.MetadataVersionv150:
		err = convertPersistVersionFromv150ToBlocklist(testDir)
	default:
		err = errors.New("invalid version")
	}
	if err != nil {
		return errors.AddContext(err, "unable to convert persistence")
	}

	// Load the new persistence
	aop, reader, err = persist.NewAppendOnlyPersist(testDir, newPersistFile, newHeader, newVersion)
	if err != nil {
		return errors.AddContext(err, "unable to open new persistence")
	}
	defer aop.Close()

	// Grab the new persistence
	newPersistence, err := unmarshalObjects(reader)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal new persistence")
	}
	if len(newPersistence) == 0 {
		return errors.New("no data in new version's persist file")
	}

	// Verify that the original persistence was properly updated
	if len(oldPersistence) != len(newPersistence) {
		return fmt.Errorf("Expected %v hashes but got %v", len(newPersistence), len(oldPersistence))
	}
	for p := range oldPersistence {
		var hash crypto.Hash
		switch oldVersion {
		case metadataVersionV143:
			hash = crypto.HashObject(p)
		case persist.MetadataVersionv150:
			hash = p
		default:
			return errors.New("invalid version")
		}
		if _, ok := newPersistence[hash]; !ok {
			return fmt.Errorf("Original persistence: %v \nLoaded persistence: %v \n Persist hash not found in list of hashes", oldPersistence, newPersistence)
		}
	}
	return nil
}

// loadCompatPersistFile loads the persist file for the supplied version into
// the testDir
func loadCompatPersistFile(testDir string, version types.Specifier) error {
	switch version {
	case metadataVersionV143:
		return loadV143CompatPersistFile(testDir)
	case persist.MetadataVersionv150:
		return loadV150CompatPersistFile(testDir)
	default:
	}
	return errors.New("invalid error")
}

// loadV143CompatPersistFile loads the v1.4.3 persist file into the testDir
func loadV143CompatPersistFile(testDir string) error {
	v143FileName := filepath.Join("..", "..", "..", "compatibility", blacklistPersistFile+"_v143")
	return copyFileToTestDir(v143FileName, filepath.Join(testDir, blacklistPersistFile))
}

// loadV150CompatPersistFile loads the v1.5.0 persist file into the testDir
func loadV150CompatPersistFile(testDir string) error {
	v150FileName := filepath.Join("..", "..", "..", "compatibility", blacklistPersistFile+"_v150")
	return copyFileToTestDir(v150FileName, filepath.Join(testDir, blacklistPersistFile))
}

// copyFileToTestDir copies the file at fromFilePath and writes it at toFilePath
func copyFileToTestDir(fromFilePath, toFilePath string) error {
	f, err := os.Open(fromFilePath)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Compose(err, f.Close())
	}()
	bytes, err := ioutil.ReadAll(f)
	if err != nil {
		return err
	}
	pf, err := os.Create(toFilePath)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Compose(err, pf.Close())
	}()
	_, err = pf.Write(bytes)
	if err != nil {
		return err
	}
	return nil
}
