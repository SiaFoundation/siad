package registry

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// TODO: F/Us
// - use LRU for limited entries in memory, rest on disk
// - optimize locking by locking each entry individually
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
	// ErrInvalidTruncate is returned if a truncate would lead to data loss.
	ErrInvalidTruncate = errors.New("can't truncate registry below the number of used entries")
)

type (
	// Registry is an in-memory key-value store. Renter's can pay the host to
	// register data with a given pubkey and secondary key (tweak).
	Registry struct {
		entries    map[crypto.Hash]*value
		staticPath string
		staticFile *os.File
		usage      bitfield
		mu         sync.Mutex
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

// valueMapKey creates a key usable in in-memory maps from a value's key and
// tweak.
func valueMapKey(key types.SiaPublicKey, tweak crypto.Hash) crypto.Hash {
	return crypto.HashAll(key, tweak)
}

// mapKey creates a key usable in in-memory maps from the value.
func (v *value) mapKey() crypto.Hash {
	return valueMapKey(v.key, v.tweak)
}

// update updates a value with a new revision, expiry and data.
func (v *value) update(rv modules.SignedRegistryValue, newExpiry types.BlockHeight, init bool) error {
	// Check if the entry has been invalidated. This should only ever be the
	// case when an entry is updated at the same time as its pruned so its
	// incredibly unlikely to happen. Usually entries would be updated long
	// before their expiry.
	if v.invalid {
		return errInvalidEntry
	}

	// Check if the new revision number is valid.
	if rv.Revision <= v.revision && !init {
		s := fmt.Sprintf("%v <= %v", rv.Revision, v.revision)
		return errors.AddContext(errInvalidRevNum, s)
	}

	// Update the entry.
	v.expiry = newExpiry
	v.data = rv.Data
	v.revision = rv.Revision
	v.signature = rv.Signature
	return nil
}

// Cap returns the capacity of the registry.
func (r *Registry) Cap() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usage.Len()
}

// Close closes the registry and its underlying resources.
func (r *Registry) Close() error {
	return r.staticFile.Close()
}

// Get fetches the data associated with a key and tweak from the registry.
func (r *Registry) Get(pubKey types.SiaPublicKey, tweak crypto.Hash) (modules.SignedRegistryValue, bool) {
	r.mu.Lock()
	v, ok := r.entries[valueMapKey(pubKey, tweak)]
	r.mu.Unlock()
	if !ok {
		return modules.SignedRegistryValue{}, false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return modules.NewSignedRegistryValue(v.tweak, v.data, v.revision, v.signature), true
}

// Len returns the length of the registry.
func (r *Registry) Len() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return uint64(len(r.entries))
}

// Truncate resizes the registry.
func (r *Registry) Truncate(newMaxEntries uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if truncating is possible.
	if newMaxEntries < uint64(len(r.entries)) {
		return ErrInvalidTruncate
	}

	// Create a new bitfield and mark all the existing entries within its range
	// and remember the ones that aren't.
	var entriesToMove []*value
	newUsage, err := newBitfield(newMaxEntries)
	if err != nil {
		return errors.AddContext(err, "failed to create new bitfield")
	}
	for _, entry := range r.entries {
		entry.mu.Lock()
		defer entry.mu.Unlock()
		// Check if entry is valid.
		if entry.invalid {
			continue // ignore invalid entries
		}
		// Check if entry is already in a valid spot.
		bit := uint64(entry.staticIndex - 1)
		if bit < newMaxEntries {
			err = newUsage.Set(bit)
			if err != nil {
				return errors.AddContext(err, "failed to set bit in new bitfield for existing index")
			}
			continue
		}
		// If not, remember the entry and keep it locked.
		entriesToMove = append(entriesToMove, entry)
	}
	// Loop over the unset bits of the new bitfield and move the entries
	// accordingly.
	for i := uint64(0); len(entriesToMove) > 0; i++ {
		if i >= newUsage.Len() {
			err := errors.New("entriesToMove is longer than free entries, this shouldn't happen")
			build.Critical(err)
			return err
		}
		if newUsage.IsSet(i) {
			continue // already in use
		}
		err = newUsage.Set(i)
		if err != nil {
			return errors.AddContext(err, "failed to set bit in new bitfield for new index")
		}
		// Move entry to new location.
		var v *value
		v, entriesToMove = entriesToMove[0], entriesToMove[1:]
		v.staticIndex = int64(i) + 1
		err = r.staticSaveEntry(v, true)
		if err != nil {
			return errors.AddContext(err, "failed to save value at new location")
		}
	}
	// Replace the usage bitfield.
	r.usage = newUsage

	// Truncate the file.
	return r.staticFile.Truncate(int64(PersistedEntrySize * (newMaxEntries + 1)))
}

// New creates a new registry or opens an existing one.
func New(path string, maxEntries uint64) (_ *Registry, err error) {
	f, err := os.OpenFile(path, os.O_RDWR, modules.DefaultFilePerm)
	if os.IsNotExist(err) {
		// try creating a new one
		f, err = initRegistry(path, maxEntries)
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
		staticFile: f,
		staticPath: path,
		usage:      b,
	}
	// Load the remaining entries.
	reg.entries, err = loadRegistryEntries(r, fi.Size()/PersistedEntrySize, b)
	return reg, nil
}

// Update adds an entry to the registry or if it exists already, updates it.
// This will also verify the revision number of the new value and the signature.
func (r *Registry) Update(rv modules.SignedRegistryValue, pubKey types.SiaPublicKey, expiry types.BlockHeight) (bool, error) {
	// Check the data against the limit.
	if len(rv.Data) > modules.RegistryDataSize {
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
	err = entry.update(rv, expiry, !exists)
	if err != nil {
		entry.mu.Unlock()
		return false, errors.AddContext(err, "failed to update entry")
	}

	// Write the entry to disk.
	err = r.staticSaveEntry(entry, true)
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
	err := r.usage.Unset(uint64(v.staticIndex) - 1)
	if err != nil {
		build.Critical("managedDeleteFromMemory: unsetting an index should never fail")
	}
	// Delete the entry from the map.
	delete(r.entries, v.mapKey())
}

// newValue creates a new value and assigns it a free bit from the bitfield. It
// adds the new value to the registry as well.
func (r *Registry) newValue(rv modules.SignedRegistryValue, pubKey types.SiaPublicKey, expiry types.BlockHeight) (*value, error) {
	bit, err := r.usage.SetRandom()
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
		signature:   rv.Signature,
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
		if err := r.staticSaveEntry(entry, false); err != nil {
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
