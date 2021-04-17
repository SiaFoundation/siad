package contractmanager

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// NOTE: I'm not sure how likely it is for this file to fill up to a
// non-negligible size, but having it zero out unused entries in the future and
// reuse them would be a nice-to-have f/u. Even with custom MDM programs, this
// file reaching 4 KiB in size would equal to paying for 17.6 TiB of data
// written to disk and reaching 4 MiB would therefore require paying for writing
// 17 PiB of data. This is quite expensive and will benefit the host more than a
// potential attacker.

const (
	// overflowMapEntrySize is the size of a single entry on the file. The first
	// 64 entries are reserved for the metadata. The remaining entries contain a
	// section id and overflow number which make up for 20 bytes. The remaining
	// bytes are padding for future extensions and to align the entries with
	// physical pages on disk.
	overflowMapEntrySize = 64

	// overflowMapMetadataSize is the size of the reserved metadata.
	overflowMapMetadataSize = 64 * overflowMapEntrySize // 4 kib
)

var (
	// errOverflowMetadataCorrupt is returned when the metadata contains invalid
	// data when loading the file.
	errOverflowMetadataCorrupt = errors.New("metadata of overflow file is not expected")

	// overflowMapVersion is the version data contained within the metadata.
	overflowMapVersion = types.NewSpecifier("Overflow-1.5.5")
)

type (
	// overflowMap is a helper type which manages the persisted overflow map on
	// disk and in memory.
	overflowMap struct {
		entryMap   map[sectorID]overflowEntry
		staticDeps modules.Dependencies
		fileSize   int64

		f  modules.File
		mu sync.Mutex
	}

	// overflowEntry is a single entry in the overflow map.
	overflowEntry struct {
		offset   int64
		overflow uint64
	}
)

// newOverflowMap creates a new map on disk or loads an existing one.
func newOverflowMap(path string, deps modules.Dependencies) (_ *overflowMap, err error) {
	// Open the file or create a new one.
	f, err := deps.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, errors.AddContext(err, "failed to open overflow file")
	}
	// Close the file in case of an error.
	defer func() {
		if err != nil {
			err = errors.Compose(err, f.Close())
		}
	}()

	// Determine size of file.
	stat, err := f.Stat()
	if err != nil {
		return nil, errors.AddContext(err, "failed to stat file")
	}
	size := stat.Size()

	// If it is empty, initialize it. Otherwise load it.
	var entryMap map[sectorID]overflowEntry
	if size == 0 {
		size, entryMap, err = initOverflowFile(f)
	} else {
		entryMap, err = loadOverflowMap(f)
	}
	if err != nil {
		return nil, errors.AddContext(err, "failed to load file")
	}

	// Sanity check. The size should never be smaller than the metadata size.
	if size < overflowMapMetadataSize {
		build.Critical(fmt.Sprintf("unexpected size of overflow.dat file %v < %v", size, overflowMapMetadataSize))
	}

	return &overflowMap{
		f:          f,
		fileSize:   size,
		entryMap:   entryMap,
		staticDeps: deps,
	}, f.Sync() // Return a synced file.
}

// newRawEntry creates a persistable entry of overflowMapEntrySize from a sector
// id and overflow value.
func newRawEntry(sid sectorID, overflow uint64) []byte {
	entry := make([]byte, overflowMapEntrySize)
	copy(entry[:len(sid)], sid[:])
	binary.LittleEndian.PutUint64(entry[len(sid):], overflow)
	return entry
}

// initOverflowFile initializes the overflow file by writing the metadata to
// disk.
func initOverflowFile(f modules.File) (int64, map[sectorID]overflowEntry, error) {
	// Create the entry for the metadata.
	mdSize := int64(overflowMapMetadataSize)
	md := make([]byte, mdSize)

	// Write the version to the beginning of the metadata.
	copy(md[:types.SpecifierLen], overflowMapVersion[:])

	_, err := f.WriteAt(md, 0)
	return mdSize, make(map[sectorID]overflowEntry), errors.AddContext(err, "failed to write overflow metadata")
}

// loadOverflowFile loads an existing overflow map from disk.
func loadOverflowMap(f modules.File) (map[sectorID]overflowEntry, error) {
	// Seek to the beginning of the file.
	_, err := f.Seek(0, io.SeekStart)
	if err != nil {
		return nil, errors.AddContext(err, "failed to seek to beginning of file")
	}

	// Buffer reads for faster loading.
	r := bufio.NewReader(f)

	// Load the metadata.
	md := make([]byte, overflowMapMetadataSize)
	_, err = io.ReadFull(r, md)
	if err != nil {
		return nil, errors.AddContext(err, "failed to load metadata")
	}

	// Compare it to what we expect.
	expectedMD := make([]byte, overflowMapMetadataSize)
	copy(expectedMD[:types.SpecifierLen], overflowMapVersion[:])
	if !bytes.Equal(md, expectedMD) {
		return nil, errOverflowMetadataCorrupt
	}

	// Read the entries.
	entry := make([]byte, overflowMapEntrySize)
	entryMap := make(map[sectorID]overflowEntry)
	offset := int64(overflowMapMetadataSize)
	for {
		// Read the next entry.
		_, err = io.ReadFull(r, entry)
		if errors.Contains(err, io.EOF) {
			break
		} else if err != nil {
			return nil, errors.AddContext(err, "failed to load entry")
		}

		// The first field is the sectorID.
		var sid sectorID
		copy(sid[:], entry[:len(sid)])

		// Followed by the overflow.
		overflow := binary.LittleEndian.Uint64(entry[len(sid) : len(sid)+8])

		// Add them to the map.
		entryMap[sid] = overflowEntry{
			offset:   offset,
			overflow: overflow,
		}

		// Increment the offset.
		offset += overflowMapEntrySize
	}
	return entryMap, nil
}

// Close closes the overflowMap's underlying file handle.
func (of *overflowMap) Close() error {
	return errors.Compose(of.Sync(), of.f.Close())
}

// Get checks the overflowMap for a specific sectorID and returns its overflow.
func (of *overflowMap) Overflow(sid sectorID) (uint64, bool) {
	of.mu.Lock()
	defer of.mu.Unlock()
	entry, exists := of.entryMap[sid]
	return entry.overflow, exists
}

// Sync syncs the underlying file to disk.
func (of *overflowMap) Sync() error {
	return of.f.Sync()
}

// SetOverflow sets the overflow for a specific sectorID on disk.
// NOTE: This doesn't hold a lock while performing the disk i/o. It's up to the
// caller to make sure to never update the overflow for multiple sector ids in
// parallel.
func (of *overflowMap) SetOverflow(sid sectorID, overflow uint64) error {
	// Prepare the entry to persist.
	rawEntry := newRawEntry(sid, overflow)

	// Check if an entry exists on disk already.
	var offset int64
	of.mu.Lock()
	entry, exists := of.entryMap[sid]
	if exists {
		// If it does, we reuse the offset.
		offset = entry.offset
	} else {
		// If it doesn't, we increment the filesize and use it as the offset.
		offset = of.fileSize
		of.fileSize += overflowMapEntrySize
	}

	// Update the entry in the map.
	of.entryMap[sid] = overflowEntry{
		offset:   offset,
		overflow: overflow,
	}
	of.mu.Unlock()

	// Write the entry to disk without holding the lock.
	_, err := of.f.WriteAt(rawEntry, offset)
	return err
}
