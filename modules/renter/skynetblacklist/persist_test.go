package skynetblacklist

import (
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/fastrand"
)

// testDir is a helper function for creating the testing directory
func testDir(name string) string {
	return build.TempDir("skynetblacklist", name)
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

	// There should be 1 element in the blacklist
	if len(sb2.merkleroots) != 1 {
		t.Fatal("Expected 1 element in the blacklist but found:", len(sb2.merkleroots))
	}
	mr, ok = sb.merkleroots[skylink.MerkleRoot()]
	if !ok {
		t.Fatalf("Expected merkleroot listed in blacklist to be %v but found %v", skylink.MerkleRoot(), mr)
	}
}

// TestMarshalSia probes the marshalSia and unmarshalSia methods
func TestMarshalSia(t *testing.T) {
	// Test MarshalSia
	var skylink modules.Skylink
	var buf bytes.Buffer
	pl := persistLink{
		MerkleRoot:  skylink.MerkleRoot(),
		Blacklisted: true,
	}
	err := pl.marshalSia(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if int64(buf.Len()) != persistLinkSize {
		t.Fatalf("Expected buf to be of size %v but got %v", persistLinkSize, buf.Len())
	}
	pl.Blacklisted = false
	err = pl.marshalSia(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if int64(buf.Len()) != 2*persistLinkSize {
		t.Fatalf("Expected buf to be of size %v but got %v", 2*persistLinkSize, buf.Len())
	}

	// Test unmarshalSia, links should unmarshal in the order they were marshalled
	var link persistLink
	r := bytes.NewBuffer(buf.Bytes())
	err = link.unmarshalSia(r)
	if err != nil {
		t.Fatal(err)
	}
	if link.MerkleRoot != skylink.MerkleRoot() {
		t.Fatalf("MerkleRoots don't match, expected %v, got %v", skylink.MerkleRoot(), link.MerkleRoot)
	}
	if !link.Blacklisted {
		t.Fatal("expected persisted link to be blacklisted")
	}
	err = link.unmarshalSia(r)
	if err != nil {
		t.Fatal(err)
	}
	if link.MerkleRoot != skylink.MerkleRoot() {
		t.Fatalf("MerkleRoots don't match, expected %v, got %v", skylink.MerkleRoot(), link.MerkleRoot)
	}
	if link.Blacklisted {
		t.Fatal("expected persisted link not to be blacklisted")
	}

	// Test unmarshalPersistLinks
	persistLinks, err := unmarshalPersistLinks(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if persistLinks[0].MerkleRoot != skylink.MerkleRoot() {
		t.Fatalf("MerkleRoots don't match, expected %v, got %v", skylink.MerkleRoot(), persistLinks[0].MerkleRoot)
	}
	if !persistLinks[0].Blacklisted {
		t.Fatal("expected persisted link to be blacklisted")
	}
	if persistLinks[1].MerkleRoot != skylink.MerkleRoot() {
		t.Fatalf("MerkleRoots don't match, expected %v, got %v", skylink.MerkleRoot(), persistLinks[1].MerkleRoot)
	}
	if persistLinks[1].Blacklisted {
		t.Fatal("expected persisted link not to be blacklisted")
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
	f, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Marshal the metadata.
	metadataBytes := encoding.MarshalAll(metadataPageSize, metadataHeader, metadataVersion)
	_, err = f.Write(metadataBytes)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Sync()
	if err != nil {
		t.Fatal(err)
	}

	// Unmarshal the length and verify it was initialized correctly
	length, err := unmarshalLength(f)
	if err != nil {
		t.Fatal(err)
	}
	if length != metadataPageSize {
		t.Fatalf("incorrect decoded length, got %v expected %v", length, metadataPageSize)
	}

	// Update the length, and write to disk
	length += metadataPageSize
	_, err = f.WriteAt(encoding.Marshal(length), 0)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Sync()
	if err != nil {
		t.Fatal(err)
	}

	// Verify the update was successful
	length, err = unmarshalLength(f)
	if err != nil {
		t.Fatal(err)
	}
	if length != 2*metadataPageSize {
		t.Fatalf("incorrect decoded length, got %v expected %v", length, 2*metadataPageSize)
	}

	// Try unmarshalling all the metadata together and ensure that it did not
	// get corrupted by the length updates
	var header, version string
	_, err = f.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatal(err)
	}
	mdBytes, err := ioutil.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	err = encoding.UnmarshalAll(mdBytes, &length, &header, &version)
	if err != nil {
		t.Fatal(err)
	}
	if length != 2*metadataPageSize {
		t.Fatalf("incorrect decoded length, got %v expected %v", length, 2*metadataPageSize)
	}
	if header != metadataHeader {
		t.Fatalf("bad header, expected %v got %v", metadataHeader, header)
	}
	if version != metadataVersion {
		t.Fatalf("bad version, expected %v got %v", metadataVersion, version)
	}
}
