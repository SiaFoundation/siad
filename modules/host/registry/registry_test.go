package registry

import (
	"bytes"
	"encoding/binary"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
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

func testDir(name string) string {
	dir := build.TempDir(name)
	_ = os.RemoveAll(dir)
	err := os.MkdirAll(dir, modules.DefaultDirPerm)
	if err != nil {
		panic(err)
	}
	return dir
}

// TestInitRegistry is a unit test for initRegistry.
func TestInitRegistry(t *testing.T) {
	dir := testDir(t.Name())
	wal := newTestWAL(filepath.Join(dir, "wal"))

	// Init the registry.
	registryPath := filepath.Join(dir, "registry")
	f, err := initRegistry(registryPath, wal)
	if err != nil {
		t.Fatal(err)
	}

	// Check the size.
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != int64(persistedEntrySize) {
		t.Fatal("wrong size")
	}

	// Close the file again.
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Compare the contents to what we expect. The version is hardcoded to
	// prevent us from accidentally changing it without breaking this test.
	expected := make([]byte, persistedEntrySize)
	binary.LittleEndian.PutUint64(expected, uint64(1))
	b, err := ioutil.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, expected) {
		t.Fatal("metadata doesn't match")
	}

	// Try to reinit the same registry again. This should fail. We check the
	// string directly since neither os.IsExist nor errors.Contains(err,
	// os.ErrExist) work.
	_, err = initRegistry(registryPath, wal)
	if err == nil || !strings.Contains(err.Error(), "file exists") {
		t.Fatal(err)
	}
}

// TestNew is a unit test for New. It confirms that New can initialize an empty
// registry and load existing items from disk.
func TestNew(t *testing.T) {
	dir := testDir(t.Name())
	wal := newTestWAL(filepath.Join(dir, "wal"))

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	r, err := New(registryPath, wal)
	if err != nil {
		t.Fatal(err)
	}

	// The first call should simply init it. Check the size and version.
	expected := make([]byte, persistedEntrySize)
	binary.LittleEndian.PutUint64(expected, registryVersion)
	b, err := ioutil.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, expected) {
		t.Fatal("metadata doesn't match")
	}

	// The entries map should be empty.
	if len(r.entries) != 0 {
		t.Fatal("registry shouldn't contain any entries")
	}

	// Save a random unused entry at the first index and a used entry at the
	// second index.
	kUnused, vUnused := randomKey(), randomValue(1)
	kUsed, vUsed := randomKey(), randomValue(2)
	err = r.saveEntry(kUnused, vUnused, false)
	if err != nil {
		t.Fatal(err)
	}
	err = r.saveEntry(kUsed, vUsed, true)
	if err != nil {
		t.Fatal(err)
	}

	// Load the registry again. 'New' should load the used entry from disk but
	// not the unused one.
	r, err = New(registryPath, wal)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.entries) != 1 {
		t.Fatal("registry should contain one entry", len(r.entries))
	}
	if v, exists := r.entries[kUsed]; !exists || !reflect.DeepEqual(*v, vUsed) {
		t.Log(v)
		t.Log(vUsed)
		t.Fatal("registry contains wrong key-value pair")
	}
}

// TestUpdate is a unit test for Update. It makes sure new entries are added
// correctly, old ones are updated and that unused slots on disk are filled.
func TestUpdate(t *testing.T) {
	dir := testDir(t.Name())
	wal := newTestWAL(filepath.Join(dir, "wal"))

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	r, err := New(registryPath, wal)
	if err != nil {
		t.Fatal(err)
	}

	// Register a value.
	k, v := randomKey(), randomValue(2)
	v.staticIndex = 1 // expected index
	updated, err := r.Update(k.key, k.tweak, v.expiry, v.revision, v.data)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("key shouldn't have existed before")
	}
	if len(r.entries) != 1 {
		t.Fatal("registry should contain one entry", len(r.entries))
	}
	if vExist, exists := r.entries[k]; !exists || !reflect.DeepEqual(*vExist, v) {
		t.Log(v)
		t.Log(*vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Update the same key again. This shouldn't work cause the revision is the
	// same.
	_, err = r.Update(k.key, k.tweak, v.expiry, v.revision, v.data)
	if !errors.Contains(err, errInvalidRevNum) {
		t.Fatal("expected invalid rev number")
	}

	// Try again with a higher revision number. This should work.
	v.revision++
	updated, err = r.Update(k.key, k.tweak, v.expiry, v.revision, v.data)
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("key should have existed before")
	}
	if len(r.entries) != 1 {
		t.Fatal("registry should contain one entry", len(r.entries))
	}
	if vExist, exists := r.entries[k]; !exists || !reflect.DeepEqual(*vExist, v) {
		t.Log(v)
		t.Log(*vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Try another update with too much data.
	v.revision++
	_, err = r.Update(k.key, k.tweak, v.expiry, v.revision, make([]byte, RegistryDataSize+1))
	if !errors.Contains(err, errTooMuchData) {
		t.Fatal("expected too much data")
	}

	// Add a second entry.
	k2, v2 := randomKey(), randomValue(2)
	v2.staticIndex = 2 // expected index
	updated, err = r.Update(k2.key, k2.tweak, v2.expiry, v2.revision, v2.data)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("key shouldn't have existed before")
	}
	if len(r.entries) != 2 {
		t.Fatal("registry should contain two entries", len(r.entries))
	}
	if vExist, exists := r.entries[k2]; !exists || !reflect.DeepEqual(*vExist, v2) {
		t.Log(v2)
		t.Log(*vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Mark the first entry as unused and save it to disk.
	err = r.saveEntry(k, v, false)
	if err != nil {
		t.Fatal(err)
	}

	// Reload the registry Only the second entry should exist.
	r, err = New(registryPath, wal)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.entries) != 1 {
		t.Fatal("registry should contain one entries", len(r.entries))
	}
	if vExist, exists := r.entries[k2]; !exists || !reflect.DeepEqual(*vExist, v2) {
		t.Log(v2)
		t.Log(*vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Update the registry with a third entry. It should get the index that the
	// first entry had before.
	k3, v3 := randomKey(), randomValue(2)
	v3.staticIndex = v.staticIndex // expected index
	updated, err = r.Update(k3.key, k3.tweak, v3.expiry, v3.revision, v3.data)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("key shouldn't have existed before")
	}
	if len(r.entries) != 2 {
		t.Fatal("registry should contain two entries", len(r.entries))
	}
	if vExist, exists := r.entries[k3]; !exists || !reflect.DeepEqual(*vExist, v3) {
		t.Log(v3)
		t.Log(*vExist)
		t.Fatal("registry contains wrong key-value pair")
	}
}
