package skynetportals

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
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
	expectedSize := numPortals*int(persistSize) + int(persist.MetadataPageSize)
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
	pl, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	filename := filepath.Join(testdir, persistFile)
	if filename != pl.staticAop.FilePath() {
		t.Fatalf("Expected filepath %v, was %v", filename, pl.staticAop.FilePath())
	}

	// There should be no portals in the list
	if len(pl.portals) != 0 {
		t.Fatal("Expected portals list to be empty but found:", len(pl.portals))
	}

	// Update portals list
	portal := modules.SkynetPortal{
		Address: "localhost:9980",
		Public:  true,
	}
	add := []modules.SkynetPortal{portal}
	remove := []modules.NetAddress{portal.Address}
	err = pl.UpdatePortals(add, remove)
	if err != nil {
		t.Fatal(err)
	}

	// Portals list should be empty because we added and then removed the same
	// portal
	if len(pl.portals) != 0 {
		t.Fatal("Expected portals list to be empty but found:", len(pl.portals))
	}

	// Verify that the correct number of portals were persisted to verify no
	// portals are being truncated
	if err := checkNumPersistedPortals(filename, 2); err != nil {
		t.Errorf("error verifying correct number of portals: %v", err)
	}

	// Add the portal again
	err = pl.UpdatePortals(add, []modules.NetAddress{})
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the portals list now
	if len(pl.portals) != 1 {
		t.Fatal("Expected 1 element in the portals list but found:", len(pl.portals))
	}
	public, ok := pl.portals[portal.Address]
	if public != portal.Public {
		t.Fatalf("Expected publicness of portal listed in portals list to be %v but was %v", portal.Public, public)
	}
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
	}

	// Load a new Skynet Portals List to verify the contents from disk get loaded
	// properly
	pl2, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the correct number of portals were persisted to verify no
	// portals are being truncated
	if err := checkNumPersistedPortals(filename, 3); err != nil {
		t.Fatalf("error verifying correct number of portals: %v", err)
	}

	// There should be 1 element in the portals list
	if len(pl2.portals) != 1 {
		t.Fatal("Expected 1 element in the portals list but found:", len(pl2.portals))
	}
	public, ok = pl2.portals[portal.Address]
	if public != portal.Public {
		t.Fatalf("Expected publicness of portal listed in portals list to be %v but was %v", portal.Public, public)
	}
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
	}

	// Add the portal again
	err = pl2.UpdatePortals(add, []modules.NetAddress{})
	if err != nil {
		t.Fatal(err)
	}

	// There should still only be 1 element in the portal list
	if len(pl2.portals) != 1 {
		t.Fatal("Expected 1 element in the portal list but found:", len(pl2.portals))
	}
	public, ok = pl2.portals[portal.Address]
	if public != portal.Public {
		t.Fatalf("Expected publicness of portal listed in portals list to be %v but was %v", portal.Public, public)
	}
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
	}

	// Load another new Skynet Portals List to verify the contents from disk get
	// loaded properly
	pl3, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the correct number of portals were persisted to verify no
	// portals are being truncated
	if err := checkNumPersistedPortals(filename, 4); err != nil {
		t.Fatalf("error verifying correct number of portals: %v", err)
	}

	// There should be 1 element in the portals list
	if len(pl3.portals) != 1 {
		t.Fatal("Expected 1 element in the portals list but found:", len(pl3.portals))
	}
	public, ok = pl3.portals[portal.Address]
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
	}
}

// TestPersistCorruption tests the persistence of the Skynet portal list when
// corruption occurs.
func TestPersistCorruption(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a new SkynetPortalList
	testdir := testDir(t.Name())
	pl, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	filename := filepath.Join(testdir, persistFile)
	if filename != pl.staticAop.FilePath() {
		t.Fatalf("Expected filepath %v, was %v", filename, pl.staticAop.FilePath())
	}

	// There should be no portals in the list
	if len(pl.portals) != 0 {
		t.Fatal("Expected portals list to be empty but found:", len(pl.portals))
	}

	// Append a bunch of random data to the end of the portals list file to test
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

	// Update portals list
	portal := modules.SkynetPortal{
		Address: "localhost:9980",
		Public:  true,
	}
	add := []modules.SkynetPortal{portal}
	remove := []modules.NetAddress{portal.Address}
	err = pl.UpdatePortals(add, remove)
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

	// Portals list should be empty because we added and then removed the same
	// portal
	if len(pl.portals) != 0 {
		t.Fatal("Expected portals list to be empty but found:", len(pl.portals))
	}

	// Add the portal again
	err = pl.UpdatePortals(add, []modules.NetAddress{})
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the portals list now
	if len(pl.portals) != 1 {
		t.Fatal("Expected 1 element in the portals list but found:", len(pl.portals))
	}
	public, ok := pl.portals[portal.Address]
	if public != portal.Public {
		t.Fatalf("Expected publicness of portal listed in portals list to be %v but was %v", portal.Public, public)
	}
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
	}

	// Load a new Skynet Portals List to verify the contents from disk get loaded
	// properly
	pl2, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the portals list
	if len(pl2.portals) != 1 {
		t.Fatal("Expected 1 element in the portals list but found:", len(pl2.portals))
	}
	public, ok = pl2.portals[portal.Address]
	if public != portal.Public {
		t.Fatalf("Expected publicness of portal listed in portals list to be %v but was %v", portal.Public, public)
	}
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
	}

	// Add the portal again
	err = pl2.UpdatePortals(add, []modules.NetAddress{})
	if err != nil {
		t.Fatal(err)
	}

	// There should still only be 1 element in the portal list
	if len(pl2.portals) != 1 {
		t.Fatal("Expected 1 element in the portal list but found:", len(pl2.portals))
	}
	public, ok = pl2.portals[portal.Address]
	if public != portal.Public {
		t.Fatalf("Expected publicness of portal listed in portals list to be %v but was %v", portal.Public, public)
	}
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
	}

	// Load another new Skynet Portals List to verify the contents from disk get
	// loaded properly
	pl3, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 element in the portals list
	if len(pl3.portals) != 1 {
		t.Fatal("Expected 1 element in the portals list but found:", len(pl3.portals))
	}
	public, ok = pl3.portals[portal.Address]
	if !ok {
		t.Fatalf("Expected address %v to be listed in portals list", portal.Address)
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
	listed := false
	public := portal.Public
	pe := persistEntry{address, public, listed}
	err := pe.MarshalSia(&buf)
	if err != nil {
		t.Fatal(err)
	}
	pe.listed = true
	err = pe.MarshalSia(&buf)
	if err != nil {
		t.Fatal(err)
	}

	// Test UnmarshalSia, portals should unmarshal in the order they were
	// marshalled.
	r := bytes.NewBuffer(buf.Bytes())
	err = pe.UnmarshalSia(r)
	if err != nil {
		t.Fatal(err)
	}
	if address != pe.address {
		t.Fatalf("Addresses don't match, expected %v, got %v", address, pe.address)
	}
	if public != pe.public {
		t.Fatalf("Publicness doesn't match, expected %v, got %v", public, pe.public)
	}
	if pe.listed {
		t.Fatal("expected persisted portal to not be listed")
	}
	err = pe.UnmarshalSia(r)
	if err != nil {
		t.Fatal(err)
	}
	if public != pe.public {
		t.Fatalf("Publicness doesn't match, expected %v, got %v", public, pe.public)
	}
	if address != pe.address {
		t.Fatalf("Addresses don't match, expected %v, got %v", address, pe.address)
	}
	if !pe.listed {
		t.Fatal("expected persisted portal to be listed")
	}

	// Test unmarshalPersistPortals
	portals, err := unmarshalObjects(buf.Bytes())
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
