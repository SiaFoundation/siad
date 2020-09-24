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
// - correctly handle shrinking the registry
const (
	// PersistedEntrySize is the size of a marshaled entry on disk.
	PersistedEntrySize = 256

	// registryVersion is the version at the beginning of the registry on disk
	// for future compatibility changes.
	registryVersion = 1
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
	}
)

// mapKey creates a key usable in in-memory maps from the value.
func (v value) mapKey() crypto.Hash {
	return crypto.HashAll(v.key, v.tweak)
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
	b := newBitfield(maxEntries)
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
	r.mu.Lock()
	defer r.mu.Unlock()

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

	v := value{
		key:         pubKey,
		tweak:       rv.Tweak,
		expiry:      expiry,
		staticIndex: -1, // Is set later.
		data:        data,
		revision:    rv.Revision,
	}

	// Check if the entry exists already. If it does and the new revision is
	// smaller than the last one, we update it.
	entry, exists := r.entries[v.mapKey()]
	if exists && v.revision > entry.revision {
		v.staticIndex = entry.staticIndex
	} else if exists {
		return exists, errInvalidRevNum
	} else if !exists {
		// The entry doesn't exist yet. So we need to create it. To do so we search
		// for the first available slot on disk.
		bit, err := r.staticUsage.SetRandom()
		if err != nil {
			return false, errors.AddContext(err, "failed to obtain free slot")
		}
		v.staticIndex = int64(bit)
	}

	// Write the entry to disk.
	err := r.saveEntry(v, true)
	if err != nil && !exists {
		// If an error occurs during saving, unset the reserved index again if
		// it wasn't in use already.
		r.staticUsage.Unset(uint64(v.staticIndex))
	}
	if err != nil {
		return exists, errors.New("failed to save new entry to disk")
	}

	// Update the in-memory map last.
	r.entries[v.mapKey()] = &v
	return exists, nil
}

// Prune deletes all entries from the registry that expire at a height smaller
// than the provided expiry argument.
func (r *Registry) Prune(expiry types.BlockHeight) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Get a slice of entries that is sorted by indices for more optimized disk
	// access.
	type kv struct {
		key   crypto.Hash
		value *value
	}
	entries := make([]kv, 0, len(r.entries))
	for k, v := range r.entries {
		entries = append(entries, kv{
			key:   k,
			value: v,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].value.staticIndex < entries[j].value.staticIndex
	})

	var errs error
	for _, entry := range entries {
		k := entry.key
		v := entry.value
		if v.expiry > expiry {
			continue // not expired
		}
		// Purge the entry.
		if err := r.saveEntry(*v, false); err != nil {
			errs = errors.Compose(errs, err)
			continue
		}
		// Mark the space on disk unused and remove the entry from the in-memory
		// map.
		delete(r.entries, k)
		r.staticUsage.Unset(uint64(v.staticIndex))
	}
	return errs
}
