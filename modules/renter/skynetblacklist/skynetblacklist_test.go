package skynetblacklist

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// testDir is a helper function for creating the testing directory
func testDir(name string) string {
	return build.TempDir("skynetblacklist", name)
}

// checkNumPersistedLinks checks that the expected number of links has been
// persisted on disk by checking the size of the persistence file.
func checkNumPersistedLinks(blacklistPath string, numLinks int) error {
	expectedSize := numLinks*int(persistSize) + int(persist.MetadataPageSize)
	if fi, err := os.Stat(blacklistPath); err != nil {
		return errors.AddContext(err, "failed to get blacklist filesize")
	} else if fi.Size() != int64(expectedSize) {
		return fmt.Errorf("expected %v links and to have a filesize of %v but was %v", numLinks, expectedSize, fi.Size())
	}
	return nil
}

// TestPersist tests the persistence of the Skynet blacklist.
func TestPersist(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a new SkynetBlacklist
	testdir := testDir(t.Name())
	pl, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	filename := filepath.Join(testdir, persistFile)
	if filename != pl.staticAop.FilePath() {
		t.Fatalf("Expected filepath %v, was %v", filename, pl.staticAop.FilePath())
	}

	// There should be no skylinks in the blacklist
	if len(pl.merkleRoots) != 0 {
		t.Fatal("Expected blacklist to be empty but found:", len(pl.merkleRoots))
	}

	// Update blacklist
	var skylink modules.Skylink
	add := []modules.Skylink{skylink}
	remove := []modules.Skylink{skylink}
	err = pl.UpdateBlacklist(add, remove)
	if err != nil {
		t.Fatal(err)
	}

	// Blacklist should be empty because we added and then removed the same
	// skylink
	if len(pl.merkleRoots) != 0 {
		t.Fatal("Expected blacklist to be empty but found:", len(pl.merkleRoots))
	}

	// Verify that the correct number of links were persisted to verify no links
	// are being truncated
	if err := checkNumPersistedLinks(filename, 2); err != nil {
		t.Errorf("error verifying correct number of links: %v", err)
	}

	// Add the skylink again
	err = pl.UpdateBlacklist(add, []modules.Skylink{})
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the blacklist now
	if len(pl.merkleRoots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(pl.merkleRoots))
	}
	_, ok := pl.merkleRoots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot %v to be listed in blacklist", skylink.MerkleRoot())
	}

	// Load a new Skynet Blacklist to verify the contents from disk get loaded
	// properly
	pl2, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the correct number of links were persisted to verify no links
	// are being truncated
	if err := checkNumPersistedLinks(filename, 3); err != nil {
		t.Errorf("error verifying correct number of links: %v", err)
	}

	// There should be 1 element in the blacklist
	if len(pl2.merkleRoots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(pl2.merkleRoots))
	}
	_, ok = pl2.merkleRoots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot %v to be listed in blacklist", skylink.MerkleRoot())
	}

	// Add the skylink again
	err = pl2.UpdateBlacklist(add, []modules.Skylink{})
	if err != nil {
		t.Fatal(err)
	}

	// There should still only be 1 element in the blacklist
	if len(pl2.merkleRoots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(pl2.merkleRoots))
	}
	_, ok = pl2.merkleRoots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot %v to be listed in blacklist", skylink.MerkleRoot())
	}

	// Load another new Skynet Blacklist to verify the contents from disk get loaded
	// properly
	pl3, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the correct number of links were persisted to verify no links
	// are being truncated
	if err := checkNumPersistedLinks(filename, 4); err != nil {
		t.Errorf("error verifying correct number of links: %v", err)
	}

	// There should be 1 element in the blacklist
	if len(pl3.merkleRoots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(pl3.merkleRoots))
	}
	_, ok = pl3.merkleRoots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot %v to be listed in blacklist", skylink.MerkleRoot())
	}
}

// TestPersistCorruption tests the persistence of the Skynet blacklist when corruption occurs.
func TestPersistCorruption(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a new SkynetBlacklist
	testdir := testDir(t.Name())
	pl, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	filename := filepath.Join(testdir, persistFile)
	if filename != pl.staticAop.FilePath() {
		t.Fatalf("Expected filepath %v, was %v", filename, pl.staticAop.FilePath())
	}

	// There should be no skylinks in the blacklist
	if len(pl.merkleRoots) != 0 {
		t.Fatal("Expected blacklist to be empty but found:", len(pl.merkleRoots))
	}

	// Append a bunch of random data to the end of the blacklist file to test
	// corruption
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, modules.DefaultFilePerm)
	if err != nil {
		t.Fatal(err)
	}
	minNumBytes := int(2 * persist.MetadataPageSize)
	_, err = f.Write(fastrand.Bytes(minNumBytes + fastrand.Intn(minNumBytes)))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Close()
	if err != nil {
		t.Fatal(err)
	}

	// The filesize with corruption should be greater than the persist length.
	fi, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	filesize := fi.Size()
	if uint64(filesize) <= pl.staticAop.PersistLength() {
		t.Fatalf("Expected file size greater than %v, got %v", pl.staticAop.PersistLength(), filesize)
	}

	// Update blacklist
	var skylink modules.Skylink
	add := []modules.Skylink{skylink}
	remove := []modules.Skylink{skylink}
	err = pl.UpdateBlacklist(add, remove)
	if err != nil {
		t.Fatal(err)
	}

	// The filesize should be equal to the persist length now due to the
	// truncate when updating.
	fi, err = os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	filesize = fi.Size()
	if uint64(filesize) != pl.staticAop.PersistLength() {
		t.Fatalf("Expected file size %v, got %v", pl.staticAop.PersistLength(), filesize)
	}

	// Blacklist should be empty because we added and then removed the same
	// skylink
	if len(pl.merkleRoots) != 0 {
		t.Fatal("Expected blacklist to be empty but found:", len(pl.merkleRoots))
	}

	// Add the skylink again
	err = pl.UpdateBlacklist(add, []modules.Skylink{})
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the blacklist now
	if len(pl.merkleRoots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(pl.merkleRoots))
	}
	_, ok := pl.merkleRoots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot %v to be listed in blacklist", skylink.MerkleRoot())
	}

	// Load a new Skynet Blacklist to verify the contents from disk get loaded
	// properly
	pl2, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the blacklist
	if len(pl2.merkleRoots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(pl2.merkleRoots))
	}
	_, ok = pl2.merkleRoots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot %v to be listed in blacklist", skylink.MerkleRoot())
	}

	// Add the skylink again
	err = pl2.UpdateBlacklist(add, []modules.Skylink{})
	if err != nil {
		t.Fatal(err)
	}

	// There should still only be 1 element in the blacklist
	if len(pl2.merkleRoots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(pl2.merkleRoots))
	}
	_, ok = pl2.merkleRoots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot %v to be listed in blacklist", skylink.MerkleRoot())
	}

	// Load another new Skynet Blacklist to verify the contents from disk get loaded
	// properly
	pl3, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the blacklist
	if len(pl3.merkleRoots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(pl3.merkleRoots))
	}
	_, ok = pl3.merkleRoots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot %v to be listed in blacklist", skylink.MerkleRoot())
	}

	// The final filesize should be equal to the persist length.
	fi, err = os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	filesize = fi.Size()
	if uint64(filesize) != pl3.staticAop.PersistLength() {
		t.Fatalf("Expected file size %v, got %v", pl3.staticAop.PersistLength(), filesize)
	}

	// Verify that the correct number of links were persisted to verify no links
	// are being truncated
	if err = checkNumPersistedLinks(filename, 4); err != nil {
		t.Errorf("error verifying correct number of links: %v", err)
	}
}

// TestMarshalSia probes the marshalSia and unmarshalSia methods
func TestMarshalSia(t *testing.T) {
	// Test MarshalSia
	var skylink modules.Skylink
	var buf bytes.Buffer
	merkleRoot := skylink.MerkleRoot()
	listed := false
	ll := persistEntry{merkleRoot, listed}
	writtenBytes := encoding.Marshal(ll)
	buf.Write(writtenBytes)
	if uint64(buf.Len()) != persistSize {
		t.Fatalf("Expected buf to be of size %v but got %v", persistSize, buf.Len())
	}
	ll.Listed = true
	writtenBytes = encoding.Marshal(ll)
	buf.Write(writtenBytes)
	if uint64(buf.Len()) != 2*persistSize {
		t.Fatalf("Expected buf to be of size %v but got %v", 2*persistSize, buf.Len())
	}

	readBytes := buf.Bytes()
	if uint64(len(readBytes)) != 2*persistSize {
		t.Fatalf("Expected %v read bytes but got %v", 2*persistSize, len(readBytes))
	}
	err := encoding.Unmarshal(readBytes[:persistSize], &ll)
	if err != nil {
		t.Fatal(err)
	}
	if merkleRoot != ll.MerkleRoot {
		t.Fatalf("MerkleRoots don't match, expected %v, got %v", merkleRoot, ll.MerkleRoot)
	}
	if ll.Listed {
		t.Fatal("expected persisted link to not be blacklisted")
	}
	err = encoding.Unmarshal(readBytes[persistSize:2*persistSize], &ll)
	if err != nil {
		t.Fatal(err)
	}
	if merkleRoot != ll.MerkleRoot {
		t.Fatalf("MerkleRoots don't match, expected %v, got %v", merkleRoot, ll.MerkleRoot)
	}
	if !ll.Listed {
		t.Fatal("expected persisted link to be blacklisted")
	}

	// Test unmarshalBlacklist
	blacklist, err := unmarshalObjects(&buf)
	if err != nil {
		t.Fatal(err)
	}

	// Since the merkleroot is the same the blacklist should only have a length
	// of 1 since the non blacklisted merkleroot was added first
	if len(blacklist) != 1 {
		t.Fatalf("Incorrect number of blacklisted merkleRoots, expected %v, got %v", 1, len(blacklist))
	}
	_, ok := blacklist[merkleRoot]
	if !ok {
		t.Fatal("merkleroot not found in blacklist")
	}
}
