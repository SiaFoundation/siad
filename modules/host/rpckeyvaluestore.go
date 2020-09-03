package host

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

const (
	storeEntrySize uint64 = 256
	storeVersion   uint64 = 1
)

var errStoreEntryUnused = errors.New("entry is not in use")

type (
	keyValueStore struct {
		entryMap map[string]*storedEntry
		usage    bitfield
		f        *os.File
	}

	// storedEntry is an entry in the keyValueStore.
	storedEntry struct {
		expiry types.BlockHeight // expiry of the entry
		index  uint64            // index within file
		data   []byte            // stored raw data
	}

	bitfield []uint64
)

func (b bitfield) Len() uint64 {
	return uint64(len(b)) * 64
}

func (b bitfield) IsSet(index uint64) bool {
	// Each index covers 8 bytes which 8 bits each. So 64 bits in total.
	sliceOffset := index / 64
	bitOffset := index % 64

	// Check out-of-bounds.
	if sliceOffset >= uint64(len(b)) {
		return false
	}
	return b[sliceOffset]>>(63-bitOffset)&1 == 1
}

func (b *bitfield) Set(index uint64) {
	// Each index covers 8 bytes which 8 bits each. So 64 bits in total.
	sliceOffset := index / 64
	bitOffset := index % 64

	// Extend bitfield if necessary.
	for sliceOffset >= uint64(len(*b)) {
		*b = append(*b, 0)
	}

	(*b)[sliceOffset] |= 1 << (63 - bitOffset)
}

func (b *bitfield) Unset(index uint64) {
	// Each index covers 8 bytes which 8 bits each. So 64 bits in total.
	sliceOffset := index / 64
	bitOffset := index % 64

	// Extend bitfield if necessary.
	for sliceOffset >= uint64(len(*b)) {
		*b = append(*b, 0)
	}

	(*b)[sliceOffset] &= ^(1 << (63 - bitOffset))
}

func (b *bitfield) Trim() {
	toRemove := 0
	old := *b
	for i := len(old) - 1; i >= 0; i-- {
		if old[i] != 0 {
			break
		}
		toRemove++
	}
	if toRemove > 0 {
		*b = make([]uint64, len(old)-toRemove)
		copy(*b, old[:len(old)-toRemove])
	}
}

func (se storedEntry) Marshal() ([]byte, error) {
	var b [storeEntrySize]byte
	// The first byte is set to 1 to indicate that the entry is in use.
	b[0] = 1
	binary.LittleEndian.PutUint64(b[1:9] , se.index)
	binary.LittleEndian.PutUint64(b[9:17] , se.expiry)
	b[17] = byte(len(se.data))
	n := copy(b[18:], se.data)
	if n != len(se.data) {
		return nil, errors.New("failed to copy full entry")
	}
	return b[:], nil
}

func (se *storedEntry) Unmarshal(b []byte) error {
	if uint64(len(b)) != storeEntrySize {
		return fmt.Errorf("wrong size %v != %v", len(b), storeEntrySize)
	}
	// Check if it's in use. If it's not, don't load it.
	if b[0] == 0 {
		return errStoreEntryUnused
	}
	se.index = binary.LittleEndian.Uint64(b[1:9])
	se.expiry = binary.LittleEndian.Uint64(b[9:17])
	dataLen = int(b[17])
	if dataLen 
	se.data = append([]byte{}, b[18:18+dataLen])
	// Unmarshal the remaining data.
	return encoding.Unmarshal(b[1:], se)
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
		entryMap: make(map[string]*storedEntry),
	}
	// Load the remaining entries.
	for i := uint64(1); i < uint64(fi.Size())/storeEntrySize; i++ {
		_, err := io.ReadFull(r, entry[:])
		if err != nil {
			return nil, errors.AddContext(err, fmt.Sprintf("failed to read entry %v of %v", i, fi.Size()/int64(storeEntrySize)))
		}
		var storedEntry storedEntry
		err = storedEntry.Unmarshal(entry[:])
		if errors.Contains(err, errStoreEntryUnused) {
			continue // ignore unused entries
		} else if err != nil {
			return nil, errors.AddContext(err, fmt.Sprintf("failed to parse entry %v of %v", i, fi.Size()/int64(storeEntrySize)))
		}
		// Add the entry to the store.
		kvs.entryMap[]

	}
	return kvs, nil
}
