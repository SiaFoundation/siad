package registry

import (
	"encoding/binary"
	"fmt"
	"os"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

type (
	persistedEntry struct {
		// key data
		Key   [KeySize]byte
		Tweak [TweakSize]byte

		// value data
		Expiry   types.BlockHeight
		DataLen  uint64
		Data     [256]byte
		Revision uint64

		// data related to persistence
		IsUsed bool
		Unused [167]byte // unused bytes for potential future fields
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

// newPersistedEntry turns a key-value pair into a persistedEntry.
func newPersistedEntry(key key, value value, isUsed bool) (persistedEntry, error) {
	if len(value.data) > RegistryDataSize {
		build.Critical("newPersistedEntry was called with too much data")
		return persistedEntry{}, errors.New("value's data is too large")
	}
	pe := persistedEntry{
		Key:   key.key,
		Tweak: key.tweak,

		DataLen:  uint64(len(value.data)),
		Expiry:   value.expiry,
		Revision: value.revision,

		IsUsed: isUsed,
	}
	copy(pe.Data[:], value.data)
	return pe, nil
}

// KeyValue converts a persistedEntry into a key-value pair.
func (entry persistedEntry) KeyValue(index int64) (key, value, error) {
	if entry.DataLen > RegistryDataSize {
		err := errors.New("KeyValue: entry has a too big data len")
		build.Critical(err)
		return key{}, value{}, err
	}
	return key{
			key:   entry.Key,
			tweak: entry.Tweak,
		}, value{
			expiry:      entry.Expiry,
			data:        entry.Data[:entry.DataLen],
			revision:    entry.Revision,
			staticIndex: index,
		}, nil
}

// Marshal marshals a persistedEntry.
func (entry persistedEntry) Marshal() ([]byte, error) {
	data := encoding.Marshal(entry)
	if uint64(len(data)) != persistedEntrySize {
		fmt.Println("data", len(data), persistedEntrySize)
		build.Critical(errEntryWrongSize)
		return nil, errEntryWrongSize
	}
	return data, nil
}

// Unmarshal unmarshals a persistedEntry.
func (entry *persistedEntry) Unmarshal(b []byte) error {
	return encoding.Unmarshal(b, entry)
}

// saveEntry stores a key-value pair on disk in an ACID fashion.
func (r *Registry) saveEntry(k key, v value, isUsed bool) error {
	entry, err := newPersistedEntry(k, v, isUsed)
	if err != nil {
		return errors.AddContext(err, "Save: failed to get persistedEntry from key-value pair")
	}
	b, err := entry.Marshal()
	if err != nil {
		return errors.AddContext(err, "Save: failed to marshal persistedEntry")
	}
	update := writeaheadlog.WriteAtUpdate(r.staticPath, v.staticIndex*persistedEntrySize, b)
	return r.staticWAL.CreateAndApplyTransaction(writeaheadlog.ApplyUpdates, update)
}
