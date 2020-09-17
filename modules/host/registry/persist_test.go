package registry

import (
	"bytes"
	"encoding/binary"
	"io/ioutil"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// randomKey creates a random key object for testing.
func randomKey() key {
	var k key
	fastrand.Read(k.key[:])
	fastrand.Read(k.tweak[:])
	return k
}

// randomValue creates a random value object for testing.
func randomValue(index int64) value {
	v := value{
		expiry:      types.BlockHeight(fastrand.Uint64n(math.MaxUint64)),
		staticIndex: index,
		data:        fastrand.Bytes(fastrand.Intn(RegistryDataSize) + 1),
		revision:    fastrand.Uint64n(math.MaxUint64 - 100), // Leave some room for incrementing the revision during tests
	}
	return v
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

// TestPersistedEntryMarshalUnmarshal tests marshaling persistedEntries.
func TestPersistedEntryMarshalUnmarshal(t *testing.T) {
	entry := persistedEntry{
		Key:      [KeySize]byte{1},
		Tweak:    [TweakSize]byte{2},
		Expiry:   3,
		DataLen:  100,
		Data:     [256]byte{4},
		IsUsed:   true,
		Revision: 5,
		Unused:   [167]byte{6},
	}
	b, err := entry.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if uint64(len(b)) != persistedEntrySize {
		t.Fatal("marshaled entry has wrong size")
	}
	var entry2 persistedEntry
	err = entry2.Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entry, entry2) {
		t.Fatal("entries don't match")
	}
}

// TestNewPersistedEntryAndKeyValue is a unit test for newPersistedEntry and
// KeyValue.
func TestNewPersistedEntry(t *testing.T) {
	// Create a random key/value pair that is stored at index 1
	index := int64(1)
	isUsed := true
	k := randomKey()
	v := randomValue(index)
	pe, err := newPersistedEntry(k, v, isUsed)
	if err != nil {
		t.Fatal(err)
	}

	if v.staticIndex != index {
		t.Fatal("index doesn't match")
	}
	if !bytes.Equal(pe.Key[:], k.key[:]) {
		t.Fatal("key doesn't match")
	}
	if !bytes.Equal(pe.Tweak[:], k.tweak[:]) {
		t.Fatal("tweak doesn't match")
	}
	if pe.Expiry != v.expiry {
		t.Fatal("expiry doesn't match")
	}
	if !bytes.Equal(pe.Data[:pe.DataLen], v.data) {
		t.Fatal("data doesn't match")
	}
	if pe.Revision != v.revision {
		t.Fatal("revision doesn't match")
	}
	if pe.IsUsed != isUsed {
		t.Fatal("isUsed doesn't match")
	}

	// Convert the persisted entry back into the key value pair.
	k2, v2, err := pe.KeyValue(index)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(k, k2) {
		t.Log(k)
		t.Log(k2)
		t.Fatal("keys don't match")
	}
	if !reflect.DeepEqual(v, v2) {
		t.Log(v)
		t.Log(v2)
		t.Fatal("values don't match")
	}
}

// TestSaveEntry unit tests the SaveEntry method.
func TestSaveEntry(t *testing.T) {
	dir := testDir(t.Name())
	wal := newTestWAL(filepath.Join(dir, "wal"))

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	r, err := New(registryPath, wal)
	if err != nil {
		t.Fatal(err)
	}

	// Create a pair that is stored at index 2.
	index := int64(2)
	isUsed := true
	k := randomKey()
	v := randomValue(index)
	pe, err := newPersistedEntry(k, v, isUsed)
	if err != nil {
		t.Fatal(err)
	}

	// Save it and read the file afterwards.
	err = r.saveEntry(k, v, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioutil.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}

	// The data should be 3 entries long, the first one being the metadata, then
	// one being all zeros and the third one matching the stored entry.
	if len(b) != 3*persistedEntrySize {
		t.Fatal("file has wrong size")
	}
	zeros := make([]byte, persistedEntrySize)
	if !bytes.Equal(zeros, b[persistedEntrySize:2*persistedEntrySize]) {
		t.Fatal("second entry isn't empty")
	}
	expected, err := pe.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expected, b[2*persistedEntrySize:]) {
		t.Fatal("third entry doesn't match expected entry")
	}
}
