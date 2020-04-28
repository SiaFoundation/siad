package skynetportals

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// testDir is a helper function for creating the testing directory
func testDir(name string) string {
	return build.TempDir("skynetportals", name)
}

// checkNumPersistedPortals checks that the expected number of portals has been
// persisted on disk by checking the size of the persistence file.
func checkNumPersistedPortals(portalsPath string, numPortals int) error {
	expectedSize := numPortals*int(persistPortalSize) + int(metadataPageSize)
	if fi, err := os.Stat(portalsPath); err != nil {
		return errors.AddContext(err, "failed to get portal list filesize")
	} else if fi.Size() != int64(expectedSize) {
		return fmt.Errorf("expected %v portals to have a filesize of %v but was %v", numPortals, expectedSize, fi.Size())
	}
	return nil
}

// TestPersist tests the persistence of the Skynet portals list.
func TestPersist(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a new SkynetPortals
	testdir := testDir(t.Name())
	sp, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	filename := filepath.Join(sp.staticPersistDir, persistFile)

	// There should be no portals in the list
	if len(sp.portals) != 0 {
		t.Fatal("Expected portals list to be empty but found:", len(sp.portals))
	}

	// Update portals list
	portal := modules.SkynetPortal{
		Address: "localhost:9980",
		Public:  true,
	}
	add := []modules.SkynetPortal{portal}
	remove := []modules.NetAddress{portal.Address}
	err = sp.UpdateSkynetPortals(add, remove)
	if err != nil {
		t.Fatal(err)
	}

	// Portals list should be empty because we added and then removed the same
	// portal
	if len(sp.portals) != 0 {
		t.Fatal("Expected portals list to be empty but found:", len(sp.portals))
	}

	// Verify that the correct number of portals were persisted to verify no
	// portals are being truncated
	if err := checkNumPersistedPortals(filename, 2); err != nil {
		t.Errorf("error verifying correct number of portals: %v", err)
	}

	// Add the portal again
	err = sp.UpdateSkynetPortals(add, []modules.NetAddress{})
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the portals list now
	if len(sp.portals) != 1 {
		t.Fatal("Expected 1 element in the portals list but found:", len(sp.portals))
	}
	public, ok := sp.portals[portal.Address]
	if public != portal.Public {
		t.Fatalf("Expected publicness of portal listed in portals list to be %v but was %v", portal.Public, public)
	}
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
	}

	// Load a new Skynet Portals List to verify the contents from disk get loaded
	// properly
	sp2, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the correct number of portals were persisted to verify no
	// portals are being truncated
	if err := checkNumPersistedPortals(filename, 3); err != nil {
		t.Fatalf("error verifying correct number of portals: %v", err)
	}

	// There should be 1 element in the portals list
	if len(sp2.portals) != 1 {
		t.Fatal("Expected 1 element in the portals list but found:", len(sp2.portals))
	}
	public, ok = sp2.portals[portal.Address]
	if public != portal.Public {
		t.Fatalf("Expected publicness of portal listed in portals list to be %v but was %v", portal.Public, public)
	}
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
	}

	// Add the portal again
	err = sp2.UpdateSkynetPortals(add, []modules.NetAddress{})
	if err != nil {
		t.Fatal(err)
	}

	// There should still only be 1 element in the portal list
	if len(sp2.portals) != 1 {
		t.Fatal("Expected 1 element in the portal list but found:", len(sp2.portals))
	}
	public, ok = sp2.portals[portal.Address]
	if public != portal.Public {
		t.Fatalf("Expected publicness of portal listed in portals list to be %v but was %v", portal.Public, public)
	}
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
	}

	// Load another new Skynet Portals List to verify the contents from disk get
	// loaded properly
	sp3, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the correct number of portals were persisted to verify no
	// portals are being truncated
	if err := checkNumPersistedPortals(filename, 4); err != nil {
		t.Fatalf("error verifying correct number of portals: %v", err)
	}

	// There should be 1 element in the portals list
	if len(sp3.portals) != 1 {
		t.Fatal("Expected 1 element in the portals list but found:", len(sp3.portals))
	}
	public, ok = sp3.portals[portal.Address]
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
	}
}

// TestPersistCorruption tests the persistence of the Skynet blacklist when
// corruption occurs.
func TestPersistCorruption(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a new SkynetBlacklist
	testdir := testDir(t.Name())
	sp, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	filename := filepath.Join(sp.staticPersistDir, persistFile)

	// There should be no portals in the list
	if len(sp.portals) != 0 {
		t.Fatal("Expected portals list to be empty but found:", len(sp.portals))
	}

	// Append a bunch of random data to the end of the portals list file to test
	// corruption
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, modules.DefaultFilePerm)
	if err != nil {
		t.Fatal(err)
	}
	minNumBytes := int(2 * metadataPageSize)
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
	if filesize <= sp.persistLength {
		t.Fatalf("Expected file size greater than %v, got %v", sp.persistLength, filesize)
	}

	// Update portals list
	portal := modules.SkynetPortal{
		Address: "localhost:9980",
		Public:  true,
	}
	add := []modules.SkynetPortal{portal}
	remove := []modules.NetAddress{portal.Address}
	err = sp.UpdateSkynetPortals(add, remove)
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
	if filesize != sp.persistLength {
		t.Fatalf("Expected file size %v, got %v", sp.persistLength, filesize)
	}

	// Portals list should be empty because we added and then removed the same
	// portal
	if len(sp.portals) != 0 {
		t.Fatal("Expected portals list to be empty but found:", len(sp.portals))
	}

	// Add the portal again
	err = sp.UpdateSkynetPortals(add, []modules.NetAddress{})
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the portals list now
	if len(sp.portals) != 1 {
		t.Fatal("Expected 1 element in the portals list but found:", len(sp.portals))
	}
	public, ok := sp.portals[portal.Address]
	if public != portal.Public {
		t.Fatalf("Expected publicness of portal listed in portals list to be %v but was %v", portal.Public, public)
	}
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
	}

	// Load a new Skynet Portals List to verify the contents from disk get loaded
	// properly
	sp2, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the portals list
	if len(sp2.portals) != 1 {
		t.Fatal("Expected 1 element in the portals list but found:", len(sp2.portals))
	}
	public, ok = sp2.portals[portal.Address]
	if public != portal.Public {
		t.Fatalf("Expected publicness of portal listed in portals list to be %v but was %v", portal.Public, public)
	}
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
	}

	// Add the portal again
	err = sp2.UpdateSkynetPortals(add, []modules.NetAddress{})
	if err != nil {
		t.Fatal(err)
	}

	// There should still only be 1 element in the portal list
	if len(sp2.portals) != 1 {
		t.Fatal("Expected 1 element in the portal list but found:", len(sp2.portals))
	}
	public, ok = sp2.portals[portal.Address]
	if public != portal.Public {
		t.Fatalf("Expected publicness of portal listed in portals list to be %v but was %v", portal.Public, public)
	}
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
	}

	// Load another new Skynet Portals List to verify the contents from disk get
	// loaded properly
	sp3, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the portals list
	if len(sp3.portals) != 1 {
		t.Fatal("Expected 1 element in the portals list but found:", len(sp3.portals))
	}
	public, ok = sp3.portals[portal.Address]
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
	}

	// The final filesize should be equal to the persist length.
	fi, err = os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	filesize = fi.Size()
	if filesize != sp3.persistLength {
		t.Fatalf("Expected file size %v, got %v", sp3.persistLength, filesize)
	}

	// Verify that the correct number of portals were persisted to verify no
	// portals are being truncated
	if err := checkNumPersistedPortals(filename, 4); err != nil {
		t.Fatalf("error verifying correct number of portals: %v", err)
	}
}

// TestMarshalSia probes the marshalSia and unmarshalSia methods
func TestMarshalSia(t *testing.T) {
	// Test MarshalSia
	portal := modules.SkynetPortal{
		Address: modules.NetAddress("localhost:9980"),
		Public:  true,
	}
	var buf bytes.Buffer
	address := portal.Address
	public := portal.Public
	listed := false
	err := marshalSia(&buf, address, public, listed)
	if err != nil {
		t.Fatal(err)
	}
	listed = true
	err = marshalSia(&buf, address, public, listed)
	if err != nil {
		t.Fatal(err)
	}

	// Test unmarshalSia, portals should unmarshal in the order they were marshalled
	r := bytes.NewBuffer(buf.Bytes())
	addr, p, l, err := unmarshalSia(r)
	if err != nil {
		t.Fatal(err)
	}
	if address != addr {
		t.Fatalf("Addresses don't match, expected %v, got %v", address, addr)
	}
	if public != p {
		t.Fatalf("Publicness doesn't match, expected %v, got %v", public, p)
	}
	if l {
		t.Fatal("expected persisted portal to not be listed")
	}
	addr, p, l, err = unmarshalSia(r)
	if err != nil {
		t.Fatal(err)
	}
	if public != p {
		t.Fatalf("Publicness doesn't match, expected %v, got %v", public, p)
	}
	if address != addr {
		t.Fatalf("Addresses don't match, expected %v, got %v", address, addr)
	}
	if !l {
		t.Fatal("expected persisted portal to be listed")
	}

	// Test unmarshalPersistPortals
	r = bytes.NewBuffer(buf.Bytes())
	portals, err := unmarshalPortals(r)
	if err != nil {
		t.Fatal(err)
	}

	// Since the address is the same the portals list should only have a length
	// of 1 since the non listed address was added first.
	if len(portals) != 1 {
		t.Fatalf("Incorrect number of listed addresses, expected %v, got %v", 1, len(portals))
	}
	_, ok := portals[address]
	if !ok {
		t.Fatal("address not found in portals list")
	}
}

// TestMarshalMetadata verifies that the marshaling and unmarshaling of the
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

	// Create empty struct of a skynet portals list and set the length. Not
	// using the New method to avoid overwriting the persist file on disk.
	sp := SkynetPortals{}
	sp.persistLength = metadataPageSize

	// Marshal the metadata and write to disk
	metadataBytes, err := sp.marshalMetadata()
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
	err = sp.unmarshalMetadata(mdBytes)
	if err != nil {
		t.Fatal(err)
	}
	if sp.persistLength != 2*metadataPageSize {
		t.Fatalf("incorrect decoded length, got %v expected %v", sp.persistLength, 2*metadataPageSize)
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
	err = sp.unmarshalMetadata(mdBytes)
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
	err = sp.unmarshalMetadata(mdBytes)
	if err != errWrongHeader {
		t.Fatalf("Expected %v got %v", errWrongHeader, err)
	}
}
