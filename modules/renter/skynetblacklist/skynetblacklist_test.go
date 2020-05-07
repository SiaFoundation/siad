package skynetblacklist

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
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
	expectedSize := numLinks*int(persistSize) + int(modules.MetadataPageSize)
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
	sb, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	filename := filepath.Join(sb.aop.PersistDir, persistFile)
	if filename != sb.aop.FilePath() {
		t.Fatalf("Expected filepath %v, was %v", filename, sb.aop.FilePath())
	}

	// There should be no skylinks in the blacklist
	if len(sb.merkleroots) != 0 {
		t.Fatal("Expected blacklist to be empty but found:", len(sb.merkleroots))
	}

	// Update blacklist
	var skylink modules.Skylink
	add := []modules.Skylink{skylink}
	remove := []modules.Skylink{skylink}
	err = sb.UpdateSkynetBlacklist(add, remove)
	if err != nil {
		t.Fatal(err)
	}

	// Blacklist should be empty because we added and then removed the same
	// skylink
	if len(sb.merkleroots) != 0 {
		t.Fatal("Expected blacklist to be empty but found:", len(sb.merkleroots))
	}

	// Verify that the correct number of links were persisted to verify no links
	// are being truncated
	if err := checkNumPersistedLinks(filename, 2); err != nil {
		t.Errorf("error verifying correct number of links: %v", err)
	}

	// Add the skylink again
	err = sb.UpdateSkynetBlacklist(add, []modules.Skylink{})
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the blacklist now
	if len(sb.merkleroots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(sb.merkleroots))
	}
	_, ok := sb.merkleroots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot %v to be listed in blacklist", skylink.MerkleRoot())
	}

	// Load a new Skynet Blacklist to verify the contents from disk get loaded
	// properly
	sb2, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the correct number of links were persisted to verify no links
	// are being truncated
	if err := checkNumPersistedLinks(filename, 3); err != nil {
		t.Errorf("error verifying correct number of links: %v", err)
	}

	// There should be 1 element in the blacklist
	if len(sb2.merkleroots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(sb2.merkleroots))
	}
	_, ok = sb2.merkleroots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot %v to be listed in blacklist", skylink.MerkleRoot())
	}

	// Add the skylink again
	err = sb2.UpdateSkynetBlacklist(add, []modules.Skylink{})
	if err != nil {
		t.Fatal(err)
	}

	// There should still only be 1 element in the blacklist
	if len(sb2.merkleroots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(sb2.merkleroots))
	}
	_, ok = sb2.merkleroots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot %v to be listed in blacklist", skylink.MerkleRoot())
	}

	// Load another new Skynet Blacklist to verify the contents from disk get loaded
	// properly
	sb3, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the correct number of links were persisted to verify no links
	// are being truncated
	if err := checkNumPersistedLinks(filename, 4); err != nil {
		t.Errorf("error verifying correct number of links: %v", err)
	}

	// There should be 1 element in the blacklist
	if len(sb3.merkleroots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(sb3.merkleroots))
	}
	_, ok = sb3.merkleroots[skylink.MerkleRoot()]
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
	sb, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	filename := filepath.Join(sb.aop.PersistDir, persistFile)
	if filename != sb.aop.FilePath() {
		t.Fatalf("Expected filepath %v, was %v", filename, sb.aop.FilePath())
	}

	// There should be no skylinks in the blacklist
	if len(sb.merkleroots) != 0 {
		t.Fatal("Expected blacklist to be empty but found:", len(sb.merkleroots))
	}

	// Append a bunch of random data to the end of the blacklist file to test
	// corruption
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, modules.DefaultFilePerm)
	if err != nil {
		t.Fatal(err)
	}
	minNumBytes := int(2 * modules.MetadataPageSize)
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
	if uint64(filesize) <= sb.aop.PersistLength {
		t.Fatalf("Expected file size greater than %v, got %v", sb.aop.PersistLength, filesize)
	}

	// Update blacklist
	var skylink modules.Skylink
	add := []modules.Skylink{skylink}
	remove := []modules.Skylink{skylink}
	err = sb.UpdateSkynetBlacklist(add, remove)
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
	if uint64(filesize) != sb.aop.PersistLength {
		t.Fatalf("Expected file size %v, got %v", sb.aop.PersistLength, filesize)
	}

	// Blacklist should be empty because we added and then removed the same
	// skylink
	if len(sb.merkleroots) != 0 {
		t.Fatal("Expected blacklist to be empty but found:", len(sb.merkleroots))
	}

	// Add the skylink again
	err = sb.UpdateSkynetBlacklist(add, []modules.Skylink{})
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the blacklist now
	if len(sb.merkleroots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(sb.merkleroots))
	}
	_, ok := sb.merkleroots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot %v to be listed in blacklist", skylink.MerkleRoot())
	}

	// Load a new Skynet Blacklist to verify the contents from disk get loaded
	// properly
	sb2, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the blacklist
	if len(sb2.merkleroots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(sb2.merkleroots))
	}
	_, ok = sb2.merkleroots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot %v to be listed in blacklist", skylink.MerkleRoot())
	}

	// Add the skylink again
	err = sb2.UpdateSkynetBlacklist(add, []modules.Skylink{})
	if err != nil {
		t.Fatal(err)
	}

	// There should still only be 1 element in the blacklist
	if len(sb2.merkleroots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(sb2.merkleroots))
	}
	_, ok = sb2.merkleroots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot %v to be listed in blacklist", skylink.MerkleRoot())
	}

	// Load another new Skynet Blacklist to verify the contents from disk get loaded
	// properly
	sb3, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the blacklist
	if len(sb3.merkleroots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(sb3.merkleroots))
	}
	_, ok = sb3.merkleroots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot %v to be listed in blacklist", skylink.MerkleRoot())
	}

	// The final filesize should be equal to the persist length.
	fi, err = os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	filesize = fi.Size()
	if uint64(filesize) != sb3.aop.PersistLength {
		t.Fatalf("Expected file size %v, got %v", sb3.aop.PersistLength, filesize)
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
	blacklisted := false
	err := marshalSia(&buf, merkleRoot, blacklisted)
	if err != nil {
		t.Fatal(err)
	}
	if uint64(buf.Len()) != persistSize {
		t.Fatalf("Expected buf to be of size %v but got %v", persistSize, buf.Len())
	}
	blacklisted = true
	err = marshalSia(&buf, merkleRoot, blacklisted)
	if err != nil {
		t.Fatal(err)
	}
	if uint64(buf.Len()) != 2*persistSize {
		t.Fatalf("Expected buf to be of size %v but got %v", 2*persistSize, buf.Len())
	}

	// Test unmarshalSia, links should unmarshal in the order they were marshalled
	r := bytes.NewBuffer(buf.Bytes())
	mr, bl, err := unmarshalSia(r)
	if err != nil {
		t.Fatal(err)
	}
	if merkleRoot != mr {
		t.Fatalf("MerkleRoots don't match, expected %v, got %v", merkleRoot, mr)
	}
	if bl {
		t.Fatal("expected persisted link to not be blacklisted")
	}
	mr, bl, err = unmarshalSia(r)
	if err != nil {
		t.Fatal(err)
	}
	if merkleRoot != mr {
		t.Fatalf("MerkleRoots don't match, expected %v, got %v", merkleRoot, mr)
	}
	if !bl {
		t.Fatal("expected persisted link to be blacklisted")
	}

	// Test unmarshalBlacklist
	r = bytes.NewBuffer(buf.Bytes())
	blacklist, err := unmarshalObjects(r)
	if err != nil {
		t.Fatal(err)
	}

	// Since the merkleroot is the same the blacklist should only have a length
	// of 1 since the non blacklisted merkleroot was added first
	if len(blacklist) != 1 {
		t.Fatalf("Incorrect number of blacklisted merkleroots, expected %v, got %v", 1, len(blacklist))
	}
	_, ok := blacklist[merkleRoot]
	if !ok {
		t.Fatal("merkleroot not found in blacklist")
	}
}
