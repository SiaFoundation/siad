package registry

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TODO: F/Us
// - use LRU for limited entries in memory, rest on disk
// - optimize locking by locking each entry individually
const (
	// PersistedEntrySize is the size of a marshaled entry on disk.
	PersistedEntrySize = modules.RegistryEntrySize
)

var (
	// errEntryWrongSize is returned when a marshaled entry doesn't have a size
	// of persistedEntrySize. This should never happen.
	errEntryWrongSize = errors.New("marshaled entry has wrong size")
	// errTooMuchData is returned when the data to register is larger than
	// RegistryDataSize.
	errTooMuchData = errors.New("registered data is too large")
	// errInvalidEntry is returned when trying to update an entry that has been
	// invalidated on disk and only exists in memory anymore.
	errInvalidEntry = errors.New("invalid entry")
	// ErrInvalidTruncate is returned if a truncate would lead to data loss.
	ErrInvalidTruncate = errors.New("can't truncate registry below the number of used entries")
	// errPathNotAbsolute is returned if the registry is created from a relative
	// path or if it's migrated to a relative path.
	errPathNotAbsolute = errors.New("registry path needs to be absolute")
	// errSamePath is returned if the registry is about to be migrated to its
	// current path.
	errSamePath = errors.New("registry can't be migrated to its current path")
)

type (
	// Registry is an in-memory key-value store. Renter's can pay the host to
	// register data with a given pubkey and secondary key (tweak).
	Registry struct {
		entries    map[modules.RegistryEntryID]*value
		staticHPK  types.SiaPublicKey
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

		entryType modules.RegistryEntryType

		// utilities
		mu      sync.Mutex
		invalid bool
	}
)

// mapKey creates a key usable in in-memory maps from the value.
func (v *value) mapKey() modules.RegistryEntryID {
	return modules.DeriveRegistryEntryID(v.key, v.tweak)
}

// update updates a value with a new revision, expiry and data.
func (v *value) update(rv modules.SignedRegistryValue, newExpiry types.BlockHeight, init bool, hpk types.SiaPublicKey) error {
	// Check if the entry has been invalidated. This should only ever be the
	// case when an entry is updated at the same time as its pruned so its
	// incredibly unlikely to happen. Usually entries would be updated long
	// before their expiry.
	if v.invalid {
		return errInvalidEntry
	}

	// Check if the new revision number is valid.
	oldRV := modules.NewSignedRegistryValue(v.tweak, v.data, v.revision, v.signature, v.entryType)
	if !init {
		s := fmt.Sprintf("%v <= %v", oldRV.Revision, rv.Revision)
		update, err := oldRV.ShouldUpdateWith(&rv.RegistryValue, hpk)
		if err != nil {
			return errors.AddContext(err, s)
		}
		if !update {
			return nil
		}
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
func (r *Registry) Get(sid modules.RegistryEntryID) (types.SiaPublicKey, modules.SignedRegistryValue, bool) {
	r.mu.Lock()
	v, ok := r.entries[sid]
	r.mu.Unlock()
	if !ok {
		return types.SiaPublicKey{}, modules.SignedRegistryValue{}, false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.key, modules.NewSignedRegistryValue(v.tweak, v.data, v.revision, v.signature, v.entryType), true
}

// Len returns the length of the registry.
func (r *Registry) Len() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return uint64(len(r.entries))
}

// Truncate resizes the registry. If 'force' was specified, it will allow to
// shrink the registry below its current size. This will cause random values to
// be lost.
func (r *Registry) Truncate(newMaxEntries uint64, force bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if truncating is possible.
	if !force && newMaxEntries < uint64(len(r.entries)) {
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
	for i := uint64(0); i < newUsage.Len() && len(entriesToMove) > 0; i++ {
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

	// If 'force' wasn't specified, there should be no more entries to move.
	if !force && len(entriesToMove) > 0 {
		err := errors.New("entriesToMove is longer than free entries, this shouldn't happen")
		build.Critical(err)
		return err
	} else if force {
		// If 'force' was specified, the remaining entries need to be removed from
		// the in-memory map.
		for _, entry := range entriesToMove {
			delete(r.entries, entry.mapKey())
		}
	}

	// Replace the usage bitfield.
	r.usage = newUsage

	// Truncate the file.
	return r.staticFile.Truncate(int64(PersistedEntrySize * (newMaxEntries + 1)))
}

// New creates a new registry or opens an existing one.
func New(path string, maxEntries uint64, hpk types.SiaPublicKey) (_ *Registry, err error) {
	// The path should be an absolute path.
	if !filepath.IsAbs(path) {
		return nil, errPathNotAbsolute
	}
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
	compatV100 := false
	err = loadRegistryMetadata(r, b)
	compatV100 = errors.Contains(err, errCompat100)
	if err != nil && !compatV100 {
		return nil, errors.AddContext(err, "failed to load and verify metadata")
	}
	// Create the registry.
	reg := &Registry{
		staticFile: f,
		staticHPK:  hpk,
		staticPath: path,
		usage:      b,
	}
	// Load the remaining entries.
	reg.entries, err = loadRegistryEntries(r, fi.Size()/PersistedEntrySize, b, compatV100)
	if err != nil {
		return nil, errors.AddContext(err, "failed to load registry entries")
	}
	// If an upgrade happened, sync the body and upgrade the metadata
	// afterwards. Then sync again.
	if compatV100 {
		for _, entry := range reg.entries {
			err = reg.staticSaveEntry(entry, true)
			if err != nil {
				return nil, errors.AddContext(err, "failed to save entry")
			}
		}
		err = reg.staticFile.Sync()
		if err != nil {
			return nil, errors.AddContext(err, "failed to sync file after upgrade")
		}
		err = writeMetadata(reg.staticFile)
		if err != nil {
			return nil, errors.AddContext(err, "failed to update metadata")
		}
		err = reg.staticFile.Sync()
		if err != nil {
			return nil, errors.AddContext(err, "failed to sync file after writing metadata")
		}
	}
	return reg, nil
}

// Update adds an entry to the registry or if it exists already, updates it.
// This will also verify the revision number of the new value and the signature.
// If an existing entry was updated it will return that entry, otherwise it
// returns the default value for a SignedRevisionValue.
func (r *Registry) Update(rv modules.SignedRegistryValue, pubKey types.SiaPublicKey, expiry types.BlockHeight) (srv modules.SignedRegistryValue, _ error) {
	// Check the data against the limit.
	if len(rv.Data) > modules.RegistryDataSize {
		return modules.SignedRegistryValue{}, errTooMuchData
	}

	// Verify the registry value.
	if err := rv.Verify(pubKey.ToPublicKey()); err != nil {
		return modules.SignedRegistryValue{}, err
	}

	// Lock the registry until we have found the existing entry or a new index
	// on disk to save a new entry. Don't hold the lock during disk I/O.
	r.mu.Lock()

	// Check if the entry exists already. If it does and the new revision is
	// larger than the last one, we update it.
	entry, exists := r.entries[modules.DeriveRegistryEntryID(pubKey, rv.Tweak)]
	var err error
	if !exists {
		// If it doesn't exist we create a new entry.
		entry, err = r.newValue(rv, pubKey, expiry)
		if err != nil {
			r.mu.Unlock()
			return modules.SignedRegistryValue{}, errors.AddContext(err, "failed to create new value")
		}
	}

	// Release the global lock before acquiring the entry lock.
	r.mu.Unlock()

	entry.mu.Lock()
	// If the entry existed, remember it before updating it.
	if exists {
		srv = modules.NewSignedRegistryValue(entry.tweak, entry.data, entry.revision, entry.signature, entry.entryType)
	}
	// Update the entry.
	err = entry.update(rv, expiry, !exists, r.staticHPK)
	if err != nil {
		entry.mu.Unlock()
		return srv, errors.AddContext(err, "failed to update entry")
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
		return modules.SignedRegistryValue{}, errors.New("failed to save new entry to disk")
	}
	entry.mu.Unlock()
	return srv, nil
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
	switch rv.Type {
	case modules.RegistryTypeInvalid:
		return nil, modules.ErrInvalidRegistryEntryType
	case modules.RegistryTypeWithPubkey:
	case modules.RegistryTypeWithoutPubkey:
	default:
		return nil, modules.ErrInvalidRegistryEntryType
	}
	v := &value{
		entryType:   rv.Type,
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

// Migrate migrates the registry to a new location.
func (r *Registry) Migrate(path string) error {
	// Return an error if the paths match.
	if !filepath.IsAbs(path) {
		return errPathNotAbsolute
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Return an error if the registry is about to be migrated to the current
	// path.
	if path == r.staticPath {
		return errSamePath
	}

	// Create the file at the new location only if it doesn't exist yet.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "Migrate: failed to create file at new location")
	}

	// Lock all existing entries and unlock them when migration is complete.
	for _, entry := range r.entries {
		entry.mu.Lock()
		defer entry.mu.Unlock()
	}

	// Seek to the beginning of the file.
	_, err = r.staticFile.Seek(0, os.SEEK_SET)
	if err != nil {
		return errors.AddContext(err, "Migrate: failed to seek to beginning of file")
	}

	// Copy the file.
	_, err = io.Copy(f, r.staticFile)
	if err != nil {
		return errors.AddContext(err, "Migrate: failed to copy file to new location")
	}

	// Sync it.
	err = f.Sync()
	if err != nil {
		return errors.AddContext(err, "Migrate: failed to sync copied file to disk")
	}

	// Update the in-memory state.
	oldPath := r.staticPath
	oldFile := r.staticFile
	r.staticFile = f
	r.staticPath = path

	// Cleanup old file.
	err = oldFile.Close()
	if err != nil {
		return errors.AddContext(err, "Migrate: failed to close old file handle")
	}
	err = os.Remove(oldPath)
	if err != nil {
		return errors.AddContext(err, "Migrate: failed to delete old file")
	}
	return nil
}
