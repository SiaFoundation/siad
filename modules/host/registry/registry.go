package registry

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// TODO: F/Us
// - cap max entries (only LRU in memory rest on disk)
// - purge expired entries

const (
	// KeySize is the size of a registered key.
	KeySize = crypto.PublicKeySize

	// TweakSize is the size of the tweak which can be used to register multiple
	// values for the same pubkey.
	TweakSize = crypto.HashSize

	// RegistryDataSize is the amount of arbitrary data a renter can register in
	// the registry.
	RegistryDataSize = 256

	persistedEntrySize = 512
	registryVersion    = 1
)

var (
	errEntryUnused    = errors.New("entry is not in use")
	errEntryWrongSize = errors.New("marshaled entry has wrong size")
	errInvalidRevNum  = errors.New("provided revision number is invalid")
	errTooMuchData    = errors.New("registered data is too large")
)

type (
	// Registry is an in-memory key-value store. Renter's can pay the
	Registry struct {
		entries     map[key]*value
		staticUsage bitfield
		staticPath  string
		staticWAL   *writeaheadlog.WAL
		mu          sync.Mutex
	}

	key struct {
		key   crypto.PublicKey
		tweak crypto.Hash
	}

	value struct {
		expiry      types.BlockHeight // expiry of the entry
		staticIndex int64             // index within file

		data     []byte // stored raw data
		revision uint64
	}
)

// initRegistry initializes a registry at the specified path using the provided
// wal.
func initRegistry(path string, wal *writeaheadlog.WAL) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, modules.DefaultFilePerm)
	if err != nil {
		return nil, errors.AddContext(err, "failed to create new file for key/value store")
	}

	// The first entry is reserved for metadata. Right now only the version
	// number.
	initData := make([]byte, persistedEntrySize)
	binary.LittleEndian.PutUint64(initData, registryVersion)

	// Write data to disk in an ACID way.
	initUpdate := writeaheadlog.WriteAtUpdate(path, 0, initData)
	err = wal.CreateAndApplyTransaction(writeaheadlog.ApplyUpdates, initUpdate)
	if err != nil {
		err = errors.Compose(err, f.Close()) // close the file on error
		return nil, errors.AddContext(err, "failed to apply init update")
	}
	return f, nil
}

// New creates a new registry or opens an existing one.
func New(path string, wal *writeaheadlog.WAL) (_ *Registry, err error) {
	f, err := os.OpenFile(path, os.O_RDWR, modules.DefaultFilePerm)
	if os.IsNotExist(err) {
		// try creating a new one
		f, err = initRegistry(path, wal)
	}
	if err != nil {
		return nil, errors.AddContext(err, "failed to open store")
	}
	defer func() {
		if err != nil {
			err = errors.Compose(err, f.Close())
		}
	}()
	// Check size.
	fi, err := f.Stat()
	if err != nil {
		return nil, errors.AddContext(err, "failed to sanity check store size")
	}
	if fi.Size()%int64(persistedEntrySize) != 0 || fi.Size() == 0 {
		return nil, errors.New("expected size of store to be multiple of entry size and not 0")
	}
	// Prepare the reader by seeking to the beginning of the file.
	_, err = f.Seek(0, io.SeekStart)
	if err != nil {
		return nil, errors.AddContext(err, "failed to seek to start of store file")
	}
	r := bufio.NewReader(f)
	// Check version. We only have one so far so we can compare to that
	// directly.
	var entry [persistedEntrySize]byte
	_, err = io.ReadFull(r, entry[:])
	if err != nil {
		return nil, errors.AddContext(err, "failed to read metadata page")
	}
	version := binary.LittleEndian.Uint64(entry[:])
	if version != registryVersion {
		return nil, fmt.Errorf("expected store version %v but got %v", registryVersion, version)
	}
	// Create the registry.
	reg := &Registry{
		entries:    make(map[key]*value),
		staticPath: path,
		staticWAL:  wal,
	}
	// The first page is always in use.
	reg.staticUsage.Set(0)
	// Load the remaining entries.
	for index := int64(1); index < fi.Size()/persistedEntrySize; index++ {
		_, err := io.ReadFull(r, entry[:])
		if err != nil {
			return nil, errors.AddContext(err, fmt.Sprintf("failed to read entry %v of %v", index, fi.Size()/int64(persistedEntrySize)))
		}
		var se persistedEntry
		err = se.Unmarshal(entry[:])
		if err != nil {
			return nil, errors.AddContext(err, fmt.Sprintf("failed to parse entry %v of %v", index, fi.Size()/int64(persistedEntrySize)))
		}
		if !se.IsUsed {
			continue // ignore unused entries
		}
		// Add the entry to the store.
		k, v, err := se.KeyValue(index)
		if err != nil {
			return nil, errors.AddContext(err, fmt.Sprintf("failed to get key-value pair from entry %v of %v", index, fi.Size()/int64(persistedEntrySize)))
		}
		reg.entries[k] = &v
	}
	return reg, nil
}

// Update adds an entry to the registry or if it exists already, updates it.
func (r *Registry) Update(pubKey crypto.PublicKey, tweak crypto.Hash, expiry types.BlockHeight, revision uint64, data []byte) (_ bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check the data against the limit.
	if len(data) > RegistryDataSize {
		return false, errTooMuchData
	}

	k := key{
		key:   pubKey,
		tweak: tweak,
	}
	v := value{
		expiry:      expiry,
		staticIndex: -1, // Is set later.
		data:        data,
		revision:    revision,
	}

	// Check if the entry exists already. If it does and the new revision is
	// smaller than the last one, we update it.
	entry, exists := r.entries[k]
	if exists && revision > entry.revision {
		v.staticIndex = entry.staticIndex
		r.entries[k] = &v
		return true, nil
	} else if exists {
		return false, errInvalidRevNum
	}

	// The entry doesn't exist yet. So we need to create it. To do so we search
	// for the first available slot on disk.
	v.staticIndex = int64(r.staticUsage.SetFirst())

	// If an error occurs during execution, unset the reserved index again.
	defer func() {
		if err != nil {
			r.staticUsage.Unset(uint64(v.staticIndex))
		}
	}()

	// Write the entry to disk.
	err = r.saveEntry(k, v, true)
	if err != nil {
		return false, errors.New("failed to save new entry to disk")
	}

	// Update the in-memory map last.
	r.entries[k] = &v
	return false, nil
}
