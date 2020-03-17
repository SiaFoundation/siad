package skynetblacklist

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// testDir is a helper function for creating the testing directory
func testDir(name string) string {
	return build.TempDir("skynetblacklist", name)
}

// checkNumPersistedLinks checks that the expected number of links has been
// persisted on disk by attempting to read that amount of data from disk
func checkNumPersistedLinks(testdir string, numLinks int) error {
	f, err := os.Open(filepath.Join(testdir, persistFile))
	if err != nil {
		return err
	}
	defer f.Close()

	buf := make([]byte, numLinks*int(persistMerkleRootSize))
	_, err = f.ReadAt(buf, metadataPageSize)
	if err != nil {
		return err
	}
	return nil
}

// TestPersist tests the persistence of the SkynetBlacklist
func TestPersist(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Creat a new SkynetBlacklist
	testdir := testDir(t.Name())
	sb, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// There should be no skylinks in the blacklist
	if len(sb.merkleroots) != 0 {
		t.Fatal("Expected blacklist to be empty but found:", len(sb.merkleroots))
	}

	// Append a bunch of random data to the end of the blacklist file to test
	// corruption
	filename := filepath.Join(sb.staticPersistDir, persistFile)
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, modules.DefaultFilePerm)
	if err != nil {
		t.Fatal(err)
	}
	minNumBytes := int(metadataPageSize)
	_, err = f.Write(fastrand.Bytes(minNumBytes + fastrand.Intn(minNumBytes)))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Close()
	if err != nil {
		t.Fatal(err)
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

	// Add the skylink again
	err = sb.UpdateSkynetBlacklist(add, []modules.Skylink{})
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the blacklist now
	if len(sb.merkleroots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(sb.merkleroots))
	}
	mr, ok := sb.merkleroots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot listed in blacklist to be %v but found %v", skylink.MerkleRoot(), mr)
	}

	// Load a new Skynet Blacklist to verify the contents from disk get loaded
	// properly
	sb2, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the correct number of links were persisted to verify no links
	// are being truncated
	if err := checkNumPersistedLinks(testdir, 3); err != nil {
		t.Fatalf("error verifying correct number of links: %v", err)
	}

	// There should be 1 element in the blacklist
	if len(sb2.merkleroots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(sb2.merkleroots))
	}
	mr, ok = sb2.merkleroots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot listed in blacklist to be %v but found %v", skylink.MerkleRoot(), mr)
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
	mr, ok = sb2.merkleroots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot listed in blacklist to be %v but found %v", skylink.MerkleRoot(), mr)
	}

	// Load another new Skynet Blacklist to verify the contents from disk get loaded
	// properly
	sb3, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the correct number of links were persisted to verify no links
	// are being truncated
	if err := checkNumPersistedLinks(testdir, 4); err != nil {
		t.Fatalf("error verifying correct number of links: %v", err)
	}

	// There should be 1 element in the blacklist
	if len(sb3.merkleroots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(sb3.merkleroots))
	}
	mr, ok = sb3.merkleroots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot listed in blacklist to be %v but found %v", skylink.MerkleRoot(), mr)
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
	if int64(buf.Len()) != persistMerkleRootSize {
		t.Fatalf("Expected buf to be of size %v but got %v", persistMerkleRootSize, buf.Len())
	}
	blacklisted = true
	err = marshalSia(&buf, merkleRoot, blacklisted)
	if err != nil {
		t.Fatal(err)
	}
	if int64(buf.Len()) != 2*persistMerkleRootSize {
		t.Fatalf("Expected buf to be of size %v but got %v", 2*persistMerkleRootSize, buf.Len())
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
		t.Fatal("expected persisted link to be blacklisted")
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

	// Test unmarshalPersistLinks
	r = bytes.NewBuffer(buf.Bytes())
	blacklist, err := unmarshalBlacklist(r, 2)
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

// TestMarshalMetadata verifies that the marshalling and unmarshaling of the
// metadata and length provides the expected results
func TestMarshalMetadata(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create persist file
	testdir := testDir(t.Name())
	err := os.MkdirAll(testdir, modules.DefaultDirPerm)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(testdir, persistFile)
	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, modules.DefaultFilePerm)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Create empty struct of a skynet blacklist and set the length. Not using
	// the New method to avoid overwritten persist file on disk
	sb := SkynetBlacklist{}
	sb.persistLength = metadataPageSize

	// Marshal the metadata and write to disk
	metadataBytes, err := sb.marshalMetadata()
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write(metadataBytes)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Sync()
	if err != nil {
		t.Fatal(err)
	}

	// Update the length, and write to disk
	lengthOffset := int64(2 * types.SpecifierLen)
	lengthBytes := encoding.Marshal(2 * metadataPageSize)
	_, err = f.WriteAt(lengthBytes, lengthOffset)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Sync()
	if err != nil {
		t.Fatal(err)
	}

	// Try unmarshaling the metadata to ensure that it did not get corrupted by
	// the length updates
	metadataSize := lengthOffset + lengthSize
	mdBytes := make([]byte, metadataSize)
	_, err = f.ReadAt(mdBytes, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The header and the version are checked during the unmarshaling of the
	// metadata
	err = sb.unmarshalMetadata(mdBytes)
	if err != nil {
		t.Fatal(err)
	}
	if sb.persistLength != 2*metadataPageSize {
		t.Fatalf("incorrect decoded length, got %v expected %v", sb.persistLength, 2*metadataPageSize)
	}

	// Write an incorrect version and verify that unmarshaling the metadata will
	// fail for unmarshaling a bad version
	badVersion := types.NewSpecifier("badversion")
	badBytes, err := badVersion.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(badBytes, types.SpecifierLen)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Sync()
	if err != nil {
		t.Fatal(err)
	}
	mdBytes = make([]byte, metadataSize)
	_, err = f.ReadAt(mdBytes, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = sb.unmarshalMetadata(mdBytes)
	if err != errWrongVersion {
		t.Fatalf("Expected %v got %v", errWrongVersion, err)
	}

	// Write an incorrect header and verify that unmarshaling the metadata will
	// fail for unmarshaling a bad header
	badHeader := types.NewSpecifier("badheader")
	badBytes, err = badHeader.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(badBytes, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Sync()
	if err != nil {
		t.Fatal(err)
	}
	mdBytes = make([]byte, metadataSize)
	_, err = f.ReadAt(mdBytes, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = sb.unmarshalMetadata(mdBytes)
	if err != errWrongHeader {
		t.Fatalf("Expected %v got %v", errWrongHeader, err)
	}
}
