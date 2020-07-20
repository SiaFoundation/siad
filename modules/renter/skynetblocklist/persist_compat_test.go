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
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestPersistCompatv143Tov150 tests converting the skynet blocklist persistence
// from v143 to v150
func TestPersistCompatv143Tov150(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	testdir := testDir(t.Name())

	// Test 1: Clean conversion from v143 to v150

	// Create sub test directory
	subTestDir := filepath.Join(testdir, "CleanConvert")
	err := os.MkdirAll(subTestDir, modules.DefaultDirPerm)
	if err != nil {
		t.Fatal(err)
	}

	// Initialize the directory with a v143 persist file
	err = loadV143CompatPersistFile(subTestDir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the persistence
	err = loadAndVerifyPersistence(subTestDir)
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

	// Initialize the directory with a v143 persist file
	err = loadV143CompatPersistFile(subTestDir)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a crash during the creation a temporary file by creating an empty
	// temp file
	f, err := os.Create(filepath.Join(subTestDir, tempPersistFileName(blacklistPersistFile)))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Verify the persistence
	err = loadAndVerifyPersistence(subTestDir)
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

	// Initialize the directory with a v143 persist file
	err = loadV143CompatPersistFile(subTestDir)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a crash during the creation a temporary file by creating a temp
	// file with random bytes
	f, err = os.Create(filepath.Join(subTestDir, tempPersistFileName(blacklistPersistFile)))
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
	err = loadAndVerifyPersistence(subTestDir)
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

	// Initialize the directory with a v143 persist file
	err = loadV143CompatPersistFile(subTestDir)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a crash after creating a temporary file
	_, err = createTempFileFromPersistFile(subTestDir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the persistence
	err = loadAndVerifyPersistence(subTestDir)
	if err != nil {
		t.Fatal(err)
	}
}

// loadAndVerifyPersistence loads the persistence and verifies that the
// conversion from v1.4.3 to v1.5.0 updated the persistence as expected
func loadAndVerifyPersistence(testDir string) error {
	// Verify that loading the older persist file works
	aop, reader, err := persist.NewAppendOnlyPersist(testDir, blacklistPersistFile, blacklistMetadataHeader, metadataVersionV143)
	if err != nil {
		return errors.AddContext(err, "unable to open v143 persist file")
	}

	// Grab the merkleroots that were persisted
	merkleroots, err := unmarshalObjects(reader)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal merkleroots")
	}
	if len(merkleroots) == 0 {
		return errors.New("no merkleroots in old version's persist file")
	}

	// Close the original AOP
	err = aop.Close()
	if err != nil {
		return errors.AddContext(err, "unable to close v1.4.3 aop")
	}

	// Call convertPersistVersionFromv143Tov150, can't call New as that will go to
	// the latest version of the persistence
	err = convertPersistVersionFromv143Tov150(testDir)
	if err != nil {
		return errors.AddContext(err, "unable to convert persistense")
	}

	// Load the v1.5.0 persistence
	aop, reader, err = persist.NewAppendOnlyPersist(testDir, blacklistPersistFile, blacklistMetadataHeader, metadataVersion)
	if err != nil {
		return errors.AddContext(err, "unable to open v1.5.0 persistence")
	}
	defer aop.Close()

	// Grab the hashes that were persisted
	hashes, err := unmarshalObjects(reader)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal hashes")
	}
	if len(hashes) == 0 {
		return errors.New("no hashes in new version's persist file")
	}

	// Verify that the original merkleroots are now the hashes in the blocklist
	if len(merkleroots) != len(hashes) {
		return fmt.Errorf("Expected %v hashes but got %v", len(merkleroots), len(hashes))
	}
	for mr := range merkleroots {
		mrHash := crypto.HashObject(mr)
		if _, ok := hashes[mrHash]; !ok {
			return fmt.Errorf("Original MerkleRoots: %v \nLoaded Hashes: %v \n MerkleRoot hash not found in list of hashes", merkleroots, hashes)
		}
	}
	return nil
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
