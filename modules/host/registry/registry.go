package registry

import (
	"bufio"
	"io"
	"os"
	"sort"
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// TODO: F/Us
// - use LRU for limited entries in memory, rest on disk
// - optimize locking by locking each entry individually
// - correctly handle growing/shrinking the registry
const (
	// PersistedEntrySize is the size of a marshaled entry on disk.
	PersistedEntrySize = 256
)

var (
	// errEntryWrongSize is returned when a marshaled entry doesn't have a size
	// of persistedEntrySize. This should never happen.
	errEntryWrongSize = errors.New("marshaled entry has wrong size")
	// errInvalidRevNum is returned when the revision number of the data to
	// register isn't greater than the known revision number.
	errInvalidRevNum = errors.New("provided revision number is invalid")
	// errInvalidSignature is returned when the signature doesn't match a
	// registry value.
	errInvalidSignature = errors.New("provided signature is invalid")
	// errTooMuchData is returned when the data to register is larger than
	// RegistryDataSize.
	errTooMuchData = errors.New("registered data is too large")
	// errInvalidEntry is returned when trying to update an entry that has been
	// invalidated on disk and only exists in memory anymore.
	errInvalidEntry = errors.New("invalid entry")
)

type (
	// Registry is an in-memory key-value store. Renter's can pay the host to
	// register data with a given pubkey and secondary key (tweak).
	Registry struct {
		entries     map[crypto.Hash]*value
		staticUsage bitfield
		staticPath  string
		staticWAL   *writeaheadlog.WAL
		mu          sync.Mutex
	}

	// values represents the value associated with a registered key.
	value struct {
		// key
		key   types.SiaPublicKey
		tweak crypto.Hash

		expiry      types.BlockHeight // expiry of the entry
		staticIndex int64             // index within file

		// value
		data      []byte // stored raw data
		revision  uint64
		signature crypto.Signature

		// utilities
		mu      sync.Mutex
		invalid bool
	}
)

// mapKey creates a key usable in in-memory maps from the value.
func (v *value) mapKey() crypto.Hash {
	return valueMapKey(v.key, v.tweak)
}

// valueMapKey creates a key usable in in-memory maps from a value's key and
// tweak.
func valueMapKey(key types.SiaPublicKey, tweak crypto.Hash) crypto.Hash {
	return crypto.HashAll(key, tweak)
}

// Get fetches the data associated with a key and tweak from the registry.
func (r *Registry) Get(pubKey types.SiaPublicKey, tweak crypto.Hash) ([]byte, bool) {
	r.mu.Lock()
	v, ok := r.entries[valueMapKey(pubKey, tweak)]
	r.mu.Unlock()
	if !ok {
		return nil, false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.data, true
}

// New creates a new registry or opens an existing one.
func New(path string, wal *writeaheadlog.WAL, maxEntries uint64) (_ *Registry, err error) {
	f, err := os.OpenFile(path, os.O_RDWR, modules.DefaultFilePerm)
	if os.IsNotExist(err) {
		// try creating a new one
		f, err = initRegistry(path, wal, maxEntries)
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
	if fi.Size()%int64(PersistedEntrySize) != 0 || fi.Size() == 0 {
		return nil, errors.New("expected size of store to be multiple of entry size and not 0")
	}
	// Prepare the reader by seeking to the beginning of the file.
	_, err = f.Seek(0, io.SeekStart)
	if err != nil {
		return nil, errors.AddContext(err, "failed to seek to start of store file")
	}
	r := bufio.NewReader(f)
	// Create a bitfield to track the used pages.
	b, err := newBitfield(maxEntries)
	if err != nil {
		return nil, errors.AddContext(err, "failed to create bitfield")
	}
	// Load and verify the metadata.
	err = loadRegistryMetadata(r, b)
	if err != nil {
		return nil, errors.AddContext(err, "failed to load and verify metadata")
	}
	// Create the registry.
	reg := &Registry{
		staticPath:  path,
		staticWAL:   wal,
		staticUsage: b,
	}
	// Load the remaining entries.
	reg.entries, err = loadRegistryEntries(r, fi.Size()/PersistedEntrySize, b)
	return reg, nil
}

// Update adds an entry to the registry or if it exists already, updates it.
func (r *Registry) Update(rv modules.RegistryValue, pubKey types.SiaPublicKey, expiry types.BlockHeight) (bool, error) {
	// Check the data against the limit.
	data := rv.Data
	if len(data) > modules.RegistryDataSize {
		return false, errTooMuchData
	}

	// Check the signature against the pubkey.
	if err := rv.Verify(pubKey.ToPublicKey()); err != nil {
		err = errors.Compose(err, errInvalidSignature)
		return false, errors.AddContext(err, "Update: failed to verify signature")
	}

	// Lock the registry until we have found the existing entry or a new index
	// on disk to save a new entry. Don't hold the lock during disk I/O.
	r.mu.Lock()

	// Check if the entry exists already. If it does and the new revision is
	// larger than the last one, we update it.
	var err error
	entry, exists := r.entries[valueMapKey(pubKey, rv.Tweak)]
	if !exists {
		// If it doesn't exist we create a new entry.
		entry, err = r.newValue(rv, pubKey, expiry)
		if err != nil {
			r.mu.Unlock()
			return false, errors.AddContext(err, "failed to create new value")
		}
	}

	// Release the global lock before acquiring the entry lock.
	r.mu.Unlock()

	// Update the entry.
	entry.mu.Lock()
	err = entry.update(rv.Revision, expiry, rv.Data, !exists)
	if err != nil {
		entry.mu.Unlock()
		return false, errors.AddContext(err, "failed to update entry")
	}

	// Write the entry to disk.
	err = r.managedSaveEntry(entry, true)
	if err != nil {
		// If an error occurs during saving and the error was just created, we
		// invalidate it, delete it from the registry and free its index.
		entry.invalid = !exists
		entry.mu.Unlock()
		if !exists {
			r.managedDeleteFromMemory(entry)
		}
		return exists, errors.New("failed to save new entry to disk")
	}
	entry.mu.Unlock()
	return exists, nil
}

// managedDeleteFromMemory deletes an entry from the registry by freeing its
// index in the bitfield and removing it from the map. This does not invalidate
// the entry itself or delete it from disk.
func (r *Registry) managedDeleteFromMemory(v *value) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Unset the index.
	r.staticUsage.Unset(uint64(v.staticIndex) - 1)
	// Delete the entry from the map.
	delete(r.entries, v.mapKey())
}

// newValue creates a new value and assigns it a free bit from the bitfield. It
// adds the new value to the registry as well.
func (r *Registry) newValue(rv modules.RegistryValue, pubKey types.SiaPublicKey, expiry types.BlockHeight) (*value, error) {
	bit, err := r.staticUsage.SetRandom()
	if err != nil {
		return nil, errors.AddContext(err, "failed to obtain free slot")
	}
	v := &value{
		key:         pubKey,
		tweak:       rv.Tweak,
		expiry:      expiry,
		staticIndex: int64(bit) + 1,
		data:        rv.Data,
		revision:    rv.Revision,
	}
	r.entries[v.mapKey()] = v
	return v, nil
}

// Prune deletes all entries from the registry that expire at a height smaller
// than or equal to the provided expiry argument.
func (r *Registry) Prune(expiry types.BlockHeight) (uint64, error) {
	// Get a slice of entries. We only hold the lock during the map access.
	r.mu.Lock()
	entries := make([]*value, 0, len(r.entries))
	for _, v := range r.entries {
		entries = append(entries, v)
	}
	r.mu.Unlock()

	// Sort the entries without holding the lock.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].staticIndex < entries[j].staticIndex
	})

	// Loop over them and delete the ones that are expired.
	var errs error
	var pruned uint64
	for _, entry := range entries {
		// Lock the entry.
		entry.mu.Lock()

		// Ignore invalid entries.
		if entry.invalid {
			entry.mu.Unlock()
			continue // already deleted
		}
		// Ignore non-expired entries.
		if entry.expiry > expiry {
			entry.mu.Unlock()
			continue // not expired
		}
		// Delete the entry from disk.
		if err := r.managedSaveEntry(entry, false); err != nil {
			errs = errors.Compose(errs, err)
			entry.mu.Unlock()
			continue
		}
		// Invalidate the entry.
		entry.invalid = true
		entry.mu.Unlock()
		// Delete the entry from the registry.
		r.managedDeleteFromMemory(entry)
		pruned++
	}
	return pruned, errs
}

// update updates a value with a new revision, expiry and data.
func (v *value) update(newRevision uint64, newExpiry types.BlockHeight, newData []byte, init bool) error {
	// Check if the entry has been invalidated. This should only ever be the
	// case when an entry is updated at the same time as its pruned so its
	// incredibly unlikely to happen. Usually entries would be updated long
	// before their expiry.
	if v.invalid {
		return errInvalidEntry
	}

	// Check if the new revision number is valid.
	if newRevision <= v.revision && !init {
		return errInvalidRevNum
	}

	// Update the entry.
	v.expiry = newExpiry
	v.data = newData
	v.revision = newRevision
	return nil
}
