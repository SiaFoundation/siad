package registry

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

const (
	persistedEntrySize uint64 = 512
	storeVersion       uint64 = 1
)

var (
	errEntryUnused   = errors.New("entry is not in use")
	errEntryTooLarge = errors.New("marshaled entry is too large")
)

type (
	// Registry is an in-memory key-value store. Renter's can pay the
	Registry struct {
		entries map[key]*value
		usage   bitfield
		f       *os.File
	}

	key struct {
		key   [32]byte
		tweak uint64
	}

	value struct {
		// fields relevant to host
		expiry types.BlockHeight // expiry of the entry
		index  uint64            // index within file

		data [256]byte // stored raw data
	}

	persistedEntry struct {
		// key data
		key   [32]byte
		tweak [32]byte

		// value data
		expiry types.BlockHeight
		data   [256]byte

		// data related to persistence
		used   bool
		unused [191]byte
	}
)

func (entry persistedEntry) Marshal() ([]byte, error) {
	data := encoding.Marshal(entry)
	if len(data) > persistedEntrySize {
		build.Critical(errEntryTooLarge)
		return nil, errEntryTooLarge
	}
	return data, nil
}

func (entry *persistedEntry) Unmarshal(b []byte) error {
	return encoding.Unmarshal(b, entry)
}

func initKeyValueStore(path string, wal *writeaheadlog.WAL) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, modules.DefaultFilePerm)
	if err != nil {
		return nil, errors.AddContext(err, "failed to create new file for key/value store")
	}

	// The first entry is reserved for metadata. Right now only the version
	// number.
	initData := make([]byte, storeEntrySize)
	binary.LittleEndian.PutUint64(initData, storeVersion)

	// Write data to disk in an ACID way.
	initUpdate := writeaheadlog.WriteAtUpdate(path, 0, initData)
	err = wal.CreateAndApplyTransaction(writeaheadlog.ApplyUpdates, initUpdate)
	if err != nil {
		err = errors.Compose(err, f.Close()) // close the file on error
		return nil, errors.AddContext(err, "failed to apply init update")
	}
	return f, nil
}

func openKeyValueStore(path string, wal *writeaheadlog.WAL) (_ *keyValueStore, err error) {
	f, err := os.OpenFile(path, os.O_RDWR, modules.DefaultFilePerm)
	if os.IsNotExist(err) {
		// try creating a new one
		f, err = initKeyValueStore(path, wal)
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
	if fi.Size()%int64(storeEntrySize) != 0 || fi.Size() == 0 {
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
	var entry [storeEntrySize]byte
	_, err = io.ReadFull(r, entry[:])
	if err != nil {
		return nil, errors.AddContext(err, "failed to read metadata page")
	}
	version := binary.LittleEndian.Uint64(entry[:])
	if version != storeVersion {
		return nil, fmt.Errorf("expected store version %v but got %v", storeVersion, version)
	}
	// Create the store.
	kvs := &keyValueStore{
		entryMap: make(map[entryKey]*storedEntry),
	}
	// Load the remaining entries.
	for i := uint64(1); i < uint64(fi.Size())/storeEntrySize; i++ {
		_, err := io.ReadFull(r, entry[:])
		if err != nil {
			return nil, errors.AddContext(err, fmt.Sprintf("failed to read entry %v of %v", i, fi.Size()/int64(storeEntrySize)))
		}
		var se storedEntry
		err = se.Unmarshal(entry[:])
		if errors.Contains(err, errStoreEntryUnused) {
			continue // ignore unused entries
		} else if err != nil {
			return nil, errors.AddContext(err, fmt.Sprintf("failed to parse entry %v of %v", i, fi.Size()/int64(storeEntrySize)))
		}
		// Add the entry to the store.
		kvs.entryMap[entryKey{
			key:   nil,
			tweak: 0,
		}] = &se

	}
	return kvs, nil
}
