package skynetblacklist

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
)

// TestPersistCompatv143Tov150 tests converting the skynet blacklist persistence
// from v143 to v150
func TestPersistCompatv143Tov150(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create test directory
	testdir := testDir(t.Name())

	// Initialize the directory with a v143 persist file
	err := os.MkdirAll(testdir, modules.DefaultDirPerm)
	if err != nil {
		t.Fatal(err)
	}
	v143FileName := filepath.Join("..", "..", "..", "compatibility", persistFile+"_v143")
	f, err := os.Open(v143FileName)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	bytes, err := ioutil.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	pf, err := os.Create(filepath.Join(testdir, persistFile))
	if err != nil {
		t.Fatal(err)
	}
	defer pf.Close()
	_, err = pf.Write(bytes)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that loading the older persist file works
	aop, reader, err := persist.NewAppendOnlyPersist(testdir, persistFile, metadataHeader, metadataVersionv143)
	if err != nil {
		t.Fatal(err)
	}

	// Grab the merkleroots that were persisted
	merkleroots, err := unmarshalObjects(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(merkleroots) == 0 {
		t.Fatal("no merkleroots in old versioned persist file")
	}

	// Close the original AOP
	err = aop.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Create a new SkynetBlacklist, this should convert the persistence
	pl, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}
	defer pl.Close()

	// Verify that the original merkleroots are now the hashes in the blacklist
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if len(merkleroots) != len(pl.hashes) {
		t.Errorf("Expected %v hashes but got %v", len(merkleroots), len(pl.hashes))
	}
	for mr := range merkleroots {
		mrHash := crypto.HashObject(mr)
		if _, ok := pl.hashes[mrHash]; !ok {
			t.Log("Original MerkleRoots:", merkleroots)
			t.Log("Loaded Hashes:", pl.hashes)
			t.Fatal("MerkleRoot hash not found in list of hashes")
		}
	}
}
