package registry

import (
	"bytes"
	"io"
	"io/ioutil"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// testingDefaultMaxEntries defines sane maxEntries for testing.
const testingDefaultMaxEntries = 640

// randomValue creates a random value object for testing and returns it both as
// the RegistryValue and value representation.
func randomValue(index int64) (modules.SignedRegistryValue, *value, crypto.SecretKey) {
	// Create in-memory value first.
	sk, pk := crypto.GenerateKeyPair()
	v := value{
		expiry:      types.BlockHeight(fastrand.Uint64n(math.MaxUint32)),
		staticIndex: index,
		data:        fastrand.Bytes(fastrand.Intn(modules.RegistryDataSize) + 1),
		revision:    fastrand.Uint64n(math.MaxUint64 - 100), // Leave some room for incrementing the revision during tests
		entryType:   modules.RegistryTypeWithoutPubkey,
	}
	v.key.Algorithm = types.SignatureEd25519
	v.key.Key = pk[:]
	fastrand.Read(v.tweak[:])

	// Then the RegistryValue.
	rv := modules.NewRegistryValue(v.tweak, v.data, v.revision, v.entryType).Sign(sk)
	v.signature = rv.Signature
	return rv, &v, sk
}

// TestPubKeyCompression tests converting a types.SiaPublicKey to a
// compressedPublicKey and back.
func TestPubKeyCompression(t *testing.T) {
	t.Parallel()

	// Create a valid key.
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       fastrand.Bytes(crypto.PublicKeySize),
	}
	// Convert the key.
	cpk, err := newCompressedPublicKey(spk)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cpk.Key[:], spk.Key) {
		t.Fatal("keys don't match")
	}
	if cpk.Algorithm != signatureEd25519 {
		t.Fatal("wrong algo")
	}
	// Convert it back.
	spk2, err := newSiaPublicKey(cpk)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(spk, spk2) {
		t.Fatal("spks don't match")
	}
}

// TestInitRegistry is a unit test for initRegistry.
func TestInitRegistry(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())

	// Init the registry.
	registryPath := filepath.Join(dir, "registry")
	f, err := initRegistry(registryPath, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}

	// Check the size.
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != int64(PersistedEntrySize*testingDefaultMaxEntries) {
		t.Fatal("wrong size")
	}

	// Close the file again.
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Compare the contents to what we expect. The version is hardcoded to
	// prevent us from accidentally changing it without breaking this test.
	expected := make([]byte, PersistedEntrySize)
	v := types.Specifier{'1', '.', '6', '.', '0', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	copy(expected[:], v[:])
	b, err := ioutil.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b[:PersistedEntrySize], expected) {
		t.Fatal("metadata doesn't match", b[:PersistedEntrySize], expected)
	}

	// Try to reinit the same registry again. This should fail. We check the
	// string directly since neither os.IsExist nor errors.Contains(err,
	// os.ErrExist) work.
	_, err = initRegistry(registryPath, testingDefaultMaxEntries)
	if err == nil || !strings.Contains(err.Error(), "file exists") {
		t.Fatal(err)
	}
}

// TestPersistedEntryMarshalUnmarshal tests marshaling persistedEntries.
func TestPersistedEntryMarshalUnmarshal(t *testing.T) {
	t.Parallel()
	entry := persistedEntry{
		Key: compressedPublicKey{
			Algorithm: signatureEd25519,
		},
		Expiry:   compressedBlockHeight(fastrand.Uint64n(math.MaxUint32)),
		DataLen:  modules.RegistryDataSize,
		Revision: fastrand.Uint64n(math.MaxUint64),
		Type:     modules.RegistryTypeWithoutPubkey,
	}
	fastrand.Read(entry.Key.Key[:])
	fastrand.Read(entry.Tweak[:])
	fastrand.Read(entry.Data[:])
	fastrand.Read(entry.Signature[:])

	// Marshal
	b, err := entry.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if uint64(len(b)) != PersistedEntrySize {
		t.Fatal("marshaled entry has wrong size", len(b), PersistedEntrySize)
	}
	// Unmarshal
	var entry2 persistedEntry
	err = entry2.Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	// Check
	if !reflect.DeepEqual(entry, entry2) {
		t.Log(entry)
		t.Log(entry2)
		t.Fatal("entries don't match")
	}

	// Try again with too much data.
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("should have triggered sanity check")
			}
		}()
		entry.DataLen++
		_, err = entry.Marshal()
		if err != nil {
			t.Fatal(err)
		}
	}()
	// Manually corrupt the length.
	b[141] = 255
	err = entry2.Unmarshal(b)
	if !errors.Contains(err, errTooMuchData) {
		t.Fatal(err)
	}
}

// TestNewPersistedEntryAndKeyValue is a unit test for newPersistedEntry and
// KeyValue.
func TestNewPersistedEntry(t *testing.T) {
	t.Parallel()
	// Create a random key/value pair that is stored at index 1
	index := int64(1)
	_, v, _ := randomValue(index)
	pe, err := newPersistedEntry(v)
	if err != nil {
		t.Fatal(err)
	}

	if v.staticIndex != index {
		t.Fatal("index doesn't match")
	}
	if !bytes.Equal(pe.Key.Key[:], v.key.Key) {
		t.Fatal("key doesn't match")
	}
	if pe.Key.Algorithm != signatureEd25519 {
		t.Fatal("wrong algo")
	}
	if !bytes.Equal(pe.Tweak[:], v.tweak[:]) {
		t.Fatal("tweak doesn't match")
	}
	if types.BlockHeight(pe.Expiry) != v.expiry {
		t.Fatal("expiry doesn't match")
	}
	if !bytes.Equal(pe.Data[:pe.DataLen], v.data) {
		t.Fatal("data doesn't match")
	}
	if pe.Revision != v.revision {
		t.Fatal("revision doesn't match")
	}

	// Convert the persisted entry back into the key value pair.
	v2, err := pe.Value(index)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v, v2) {
		t.Log(v)
		t.Log(v2)
		t.Fatal("values don't match")
	}
}

// TestSaveEntry unit tests the SaveEntry method.
func TestSaveEntry(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	r, err := New(registryPath, testingDefaultMaxEntries, types.SiaPublicKey{})
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// Create a pair that is stored at index 2.
	index := int64(2)
	_, v, _ := randomValue(index)
	pe, err := newPersistedEntry(v)
	if err != nil {
		t.Fatal(err)
	}

	// Save it and read the file afterwards.
	err = r.staticSaveEntry(v, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioutil.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}

	// The data should be testingDefaultMaxEntries entries long, the first one
	// being the metadata, then one being all zeros and the third one matching
	// the stored entry. Everything after that should be zeros again.
	if len(b) != testingDefaultMaxEntries*PersistedEntrySize {
		t.Fatal("file has wrong size")
	}
	zeros := make([]byte, PersistedEntrySize)
	if !bytes.Equal(zeros, b[PersistedEntrySize:2*PersistedEntrySize]) {
		t.Fatal("second entry isn't empty")
	}
	expected, err := pe.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expected, b[2*PersistedEntrySize:3*PersistedEntrySize]) {
		t.Fatal("third entry doesn't match expected entry")
	}
	if !bytes.Equal(make([]byte, (testingDefaultMaxEntries-3)*PersistedEntrySize), b[3*PersistedEntrySize:]) {
		t.Fatal("remaining data should be zeros")
	}
}
