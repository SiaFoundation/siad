package skynetblacklist

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

// TestPersistCompatv143Tov150 tests converting the skynet blacklist persistence
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
	f, err := os.Create(filepath.Join(subTestDir, tempPersistFile))
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
	f, err = os.Create(filepath.Join(subTestDir, tempPersistFile))
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
	aop, reader, err := persist.NewAppendOnlyPersist(testDir, persistFile, metadataHeader, metadataVersionV143)
	if err != nil {
		return err
	}

	// Grab the merkleroots that were persisted
	merkleroots, err := unmarshalObjects(reader)
	if err != nil {
		return err
	}
	if len(merkleroots) == 0 {
		return errors.New("no merkleroots in old version's persist file")
	}

	// Close the original AOP
	err = aop.Close()
	if err != nil {
		return err
	}

	// Create a new SkynetBlacklist, this should convert the persistence
	sb, err := New(testDir)
	if err != nil {
		return err
	}
	defer sb.Close()

	// Verify that the original merkleroots are now the hashes in the blacklist
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if len(merkleroots) != len(sb.hashes) {
		return fmt.Errorf("Expected %v hashes but got %v", len(merkleroots), len(sb.hashes))
	}
	for mr := range merkleroots {
		mrHash := crypto.HashObject(mr)
		if _, ok := sb.hashes[mrHash]; !ok {
			return fmt.Errorf("Original MerkleRoots: %v \nLoaded Hashes: %v \n MerkleRoot hash not found in list of hashes", merkleroots, sb.hashes)
		}
	}
	return nil
}

// loadV143CompatPersistFile loads the v1.4.3 persist file into the testDir
func loadV143CompatPersistFile(testDir string) (err error) {
	v143FileName := filepath.Join("..", "..", "..", "compatibility", persistFile+"_v143")
	f, err := os.Open(v143FileName)
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
	pf, err := os.Create(filepath.Join(testDir, persistFile))
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
