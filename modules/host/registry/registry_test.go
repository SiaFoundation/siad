package registry

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// newTestWal is a helper method to create a WAL for testing.
func newTestWAL(path string) *writeaheadlog.WAL {
	_, wal, err := writeaheadlog.New(path)
	if err != nil {
		panic(err)
	}
	return wal
}

// testDir creates a temporary dir for testing.
func testDir(name string) string {
	dir := build.TempDir(name)
	_ = os.RemoveAll(dir)
	err := os.MkdirAll(dir, modules.DefaultDirPerm)
	if err != nil {
		panic(err)
	}
	return dir
}

// TestNew is a unit test for New. It confirms that New can initialize an empty
// registry and load existing items from disk.
func TestNew(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())
	wal := newTestWAL(filepath.Join(dir, "wal"))

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	r, err := New(registryPath, wal, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}

	// No bit should be used.
	for i := uint64(0); i < r.staticUsage.Len(); i++ {
		if r.staticUsage.IsSet(i) {
			t.Fatal("no page should be in use")
		}
	}

	// The first call should simply init it. Check the size and version.
	expected := make([]byte, PersistedEntrySize)
	copy(expected[:], registryVersion[:])
	b, err := ioutil.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b[:PersistedEntrySize], expected) {
		t.Fatal("metadata doesn't match")
	}

	// The entries map should be empty.
	if len(r.entries) != 0 {
		t.Fatal("registry shouldn't contain any entries")
	}

	// Save a random unused entry at the first index and a used entry at the
	// second index.
	_, vUnused, _ := randomValue(1)
	_, vUsed, _ := randomValue(2)
	err = r.saveEntry(vUnused, false)
	if err != nil {
		t.Fatal(err)
	}
	err = r.saveEntry(vUsed, true)
	if err != nil {
		t.Fatal(err)
	}

	// Load the registry again. 'New' should load the used entry from disk but
	// not the unused one.
	r, err = New(registryPath, wal, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.entries) != 1 {
		t.Fatal("registry should contain one entry", len(r.entries))
	}
	v, exists := r.entries[vUsed.mapKey()]
	if !exists || !reflect.DeepEqual(*v, vUsed) {
		t.Log(v)
		t.Log(vUsed)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Loaded page should be in use.
	for i := uint64(0); i < r.staticUsage.Len(); i++ {
		if r.staticUsage.IsSet(i) != (i == uint64(v.staticIndex-1)) {
			t.Fatal("wrong page is set")
		}
	}
}

// TestUpdate is a unit test for Update. It makes sure new entries are added
// correctly, old ones are updated and that unused slots on disk are filled.
func TestUpdate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())
	wal := newTestWAL(filepath.Join(dir, "wal"))

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	r, err := New(registryPath, wal, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}

	// Register a value.
	rv, v, sk := randomValue(2)
	updated, err := r.Update(rv, v.key, v.expiry)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("key shouldn't have existed before")
	}
	if len(r.entries) != 1 {
		t.Fatal("registry should contain one entry", len(r.entries))
	}
	vExist, exists := r.entries[v.mapKey()]
	if !exists {
		t.Fatal("entry doesn't exist")
	}
	v.staticIndex = vExist.staticIndex
	if !reflect.DeepEqual(*vExist, v) {
		t.Log(v)
		t.Log(*vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Update the same key again. This shouldn't work cause the revision is the
	// same.
	_, err = r.Update(rv, v.key, v.expiry)
	if !errors.Contains(err, errInvalidRevNum) {
		t.Fatal("expected invalid rev number")
	}

	// Try again with a higher revision number. This should work.
	v.revision++
	rv.Revision++
	rv.Sign(sk)
	updated, err = r.Update(rv, v.key, v.expiry)
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("key should have existed before")
	}
	r, err = New(registryPath, wal, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.entries) != 1 {
		t.Fatal("registry should contain one entry", len(r.entries))
	}
	if vExist, exists := r.entries[v.mapKey()]; !exists || !reflect.DeepEqual(*vExist, v) {
		t.Log(v)
		t.Log(*vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Try another update with too much data.
	v.revision++
	rv.Revision++
	v.data = make([]byte, modules.RegistryDataSize+1)
	rv.Data = v.data
	_, err = r.Update(rv, v.key, v.expiry)
	if !errors.Contains(err, errTooMuchData) {
		t.Fatal("expected too much data")
	}
	v.data = make([]byte, modules.RegistryDataSize)

	// Add a second entry.
	rv2, v2, _ := randomValue(2)
	v2.staticIndex = 2 // expected index
	updated, err = r.Update(rv2, v2.key, v2.expiry)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("key shouldn't have existed before")
	}
	if len(r.entries) != 2 {
		t.Fatal("registry should contain two entries", len(r.entries))
	}
	vExist, exists = r.entries[v2.mapKey()]
	if !exists {
		t.Fatal("entry doesn't exist")
	}
	v2.staticIndex = vExist.staticIndex
	if !reflect.DeepEqual(*vExist, v2) {
		t.Log(v2)
		t.Log(*vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Mark the first entry as unused and save it to disk.
	err = r.saveEntry(v, false)
	if err != nil {
		t.Fatal(err)
	}

	// Reload the registry. Only the second entry should exist.
	r, err = New(registryPath, wal, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.entries) != 1 {
		t.Fatal("registry should contain one entries", len(r.entries))
	}
	if vExist, exists := r.entries[v2.mapKey()]; !exists || !reflect.DeepEqual(*vExist, v2) {
		t.Log(v2)
		t.Log(*vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Update the registry with a third entry. It should get the index that the
	// first entry had before.
	rv3, v3, _ := randomValue(2)
	v3.staticIndex = v.staticIndex // expected index
	updated, err = r.Update(rv3, v3.key, v3.expiry)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("key shouldn't have existed before")
	}
	if len(r.entries) != 2 {
		t.Fatal("registry should contain two entries", len(r.entries))
	}
	vExist, exists = r.entries[v3.mapKey()]
	if !exists {
		t.Fatal("entry doesn't exist")
	}
	v3.staticIndex = vExist.staticIndex
	if !reflect.DeepEqual(*vExist, v3) {
		t.Log(v3)
		t.Log(*vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Update the registry with the third entry again but increment the revision
	// number without resigning. This should fail.
	rv3.Revision++
	updated, err = r.Update(rv3, v3.key, v3.expiry)
	if !errors.Contains(err, errInvalidSignature) {
		t.Fatal(err)
	}
}

// TestRegistryLimit checks if the bitfield of the limit enforces its
// preallocated size.
func TestRegistryLimit(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())
	wal := newTestWAL(filepath.Join(dir, "wal"))

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	limit := uint64(128)
	r, err := New(registryPath, wal, limit)
	if err != nil {
		t.Fatal(err)
	}

	// Add entries up until the limit.
	for i := uint64(0); i < limit; i++ {
		rv, v, _ := randomValue(0)
		_, err = r.Update(rv, v.key, v.expiry)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Next one should fail.
	rv, v, _ := randomValue(0)
	_, err = r.Update(rv, v.key, v.expiry)
	if !errors.Contains(err, ErrNoFreeBit) {
		t.Fatal(err)
	}
}

// TestPrune is a unit test for Prune.
func TestPrune(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())
	wal := newTestWAL(filepath.Join(dir, "wal"))

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	r, err := New(registryPath, wal, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}

	// Add 2 entries with different expiries.
	rv1, v1, _ := randomValue(0)
	v1.expiry = 1
	_, err = r.Update(rv1, v1.key, v1.expiry)
	if err != nil {
		t.Fatal(err)
	}
	rv2, v2, _ := randomValue(0)
	v2.expiry = 2
	_, err = r.Update(rv2, v2.key, v2.expiry)
	if err != nil {
		t.Fatal(err)
	}

	// Should have 2 entries.
	if len(r.entries) != 2 {
		t.Fatal("wrong number of entries")
	}

	// Check bitfield.
	inUse := 0
	for i := uint64(0); i < r.staticUsage.Len(); i++ {
		if r.staticUsage.IsSet(i) {
			inUse++
		}
	}
	if inUse != len(r.entries) {
		t.Fatalf("expected %v bits to be in use", len(r.entries))
	}

	// Purge 1 of them.
	err = r.Prune(1)
	if err != nil {
		t.Fatal(err)
	}

	// Should have 1 entry.
	if len(r.entries) != 1 {
		t.Fatal("wrong number of entries")
	}
	vExist, exists := r.entries[v2.mapKey()]
	if !exists {
		t.Fatal("entry doesn't exist")
	}
	v2.staticIndex = vExist.staticIndex
	if !reflect.DeepEqual(*vExist, v2) {
		t.Log(v2)
		t.Log(*vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Check bitfield.
	inUse = 0
	for i := uint64(0); i < r.staticUsage.Len(); i++ {
		if r.staticUsage.IsSet(i) {
			inUse++
		}
	}
	if inUse != len(r.entries) {
		t.Fatalf("expected %v bits to be in use", len(r.entries))
	}

	// Restart.
	_, err = New(registryPath, wal, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}

	// Should have 1 entry.
	if len(r.entries) != 1 {
		t.Fatal("wrong number of entries")
	}
	if vExist, exists := r.entries[v2.mapKey()]; !exists || !reflect.DeepEqual(*vExist, v2) {
		t.Log(v2)
		t.Log(*vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Check bitfield.
	inUse = 0
	for i := uint64(0); i < r.staticUsage.Len(); i++ {
		if r.staticUsage.IsSet(i) {
			inUse++
		}
	}
	if inUse != len(r.entries) {
		t.Fatalf("expected %v bits to be in use", len(r.entries))
	}
}

// TestFullRegistry tests filling up a whole registry, reloading it and pruning
// it.
func TestFullRegistry(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())
	wal := newTestWAL(filepath.Join(dir, "wal"))

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	numEntries := uint64(128)
	r, err := New(registryPath, wal, numEntries)
	if err != nil {
		t.Fatal(err)
	}

	// Fill it completely.
	vals := make([]value, 0, numEntries)
	for i := uint64(0); i < numEntries; i++ {
		rv, v, _ := randomValue(0)
		v.expiry = types.BlockHeight(i)
		u, err := r.Update(rv, v.key, v.expiry)
		if err != nil {
			t.Fatal(err)
		}
		if u {
			t.Fatal("entry shouldn't exist")
		}
		vals = append(vals, v)
	}

	// Try one more entry. This should fail.
	rv, v, _ := randomValue(0)
	_, err = r.Update(rv, v.key, v.expiry)
	if !errors.Contains(err, ErrNoFreeBit) {
		t.Fatal(err)
	}

	// Reload it.
	r, err = New(registryPath, wal, numEntries)
	if err != nil {
		t.Fatal(err)
	}

	// Check number of entries.
	if uint64(len(r.entries)) != numEntries {
		t.Fatal(err)
	}
	for _, val := range vals {
		valExist, exists := r.entries[val.mapKey()]
		if !exists {
			t.Fatal("entry not found")
		}
		val.staticIndex = valExist.staticIndex
		if !reflect.DeepEqual(*valExist, val) {
			t.Fatal("vals don't match")
		}
	}

	// Prune expiry numEntries-1. This should leave half the entries.
	err = r.Prune(types.BlockHeight(numEntries/2 - 1))
	if err != nil {
		t.Fatal(err)
	}

	// Reload it.
	r, err = New(registryPath, wal, numEntries)
	if err != nil {
		t.Fatal(err)
	}

	// Check number of entries. Second half should still be in there.
	if uint64(len(r.entries)) != numEntries/2 {
		t.Fatal(len(r.entries), numEntries/2)
	}
	for _, val := range vals[numEntries/2:] {
		valExist, exists := r.entries[val.mapKey()]
		if !exists {
			t.Fatal("entry not found")
		}
		val.staticIndex = valExist.staticIndex
		if !reflect.DeepEqual(*valExist, val) {
			t.Fatal("vals don't match")
		}
	}
}
