package contractmanager

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
)

const removalEntrySize = 20

type (
	removalEntry struct {
		ID sectorID
		// Count refers to the number of references that should be removed from
		// the contract manager.
		Count uint64
		// Offset refers to the position in the persistence file of the entry
		Offset int64
	}

	sectorRemovalMap struct {
		mu sync.Mutex
		// sectors tracks sector IDs that need to be removed
		sectors map[sectorID]struct{}
		// entries tracks the current state of the persistence file
		entries     map[sectorID]removalEntry
		removalFile *os.File

		cm  *ContractManager
		wal *writeAheadLog
	}
)

func writeRemovalEntry(w io.WriterAt, id sectorID, count uint64, offset int64) error {
	buf := make([]byte, removalEntrySize)
	copy(buf[:], id[:])
	binary.LittleEndian.PutUint64(buf[12:], count)
	_, err := w.WriteAt(buf, offset)
	return err
}

// compactPersistFile writes the current state of the sector removal map to a temp
// file then atomically renames the temp file.
func (srm *sectorRemovalMap) compactPersistFile(path string) error {
	tmpFile := path + ".tmp"
	rftmp, err := os.Create(tmpFile)
	if err != nil {
		return errors.AddContext(err, "unable to create temporary sector removal queue file")
	}
	defer rftmp.Close()
	bw := bufio.NewWriter(rftmp)

	// write each entry to the temp file
	var entryCount int64
	buf := make([]byte, removalEntrySize)
	for id, entry := range srm.entries {
		// update the file offset for the entry
		entry.Offset = entryCount * removalEntrySize
		srm.entries[id] = entry
		// encode the entry and write to the temp file
		copy(buf[:], id[:])
		binary.LittleEndian.PutUint64(buf[12:], entry.Count)
		if _, err := bw.Write(buf); err != nil {
			return errors.AddContext(err, "error writing sector removal temp file")
		}
		entryCount++
	}

	// close the current file and rename the temp file
	if err := srm.removalFile.Close(); err != nil {
		return errors.AddContext(err, "error closing sector removal file")
	}

	// flush the writer and close the temp file
	if err := bw.Flush(); err != nil {
		return errors.AddContext(err, "error flushing sector removal temp file")
	} else if err := rftmp.Sync(); err != nil {
		return errors.AddContext(err, "error closing sector removal temp file")
	} else if err := rftmp.Close(); err != nil {
		return errors.AddContext(err, "error closing sector removal temp file")
	}

	// rename the temp file
	if err := os.Rename(tmpFile, path); err != nil {
		return errors.AddContext(err, "error renaming sector removal temp file")
	}

	// reopen the persistence file
	srm.removalFile, err = os.OpenFile(path, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "unable to open sector removal queue file")
	}
	return nil
}

// managedLoad loads the removal queue from disk. Entries with a count of zero
// and read errors are ignored. The file is compacted while loading.
func (srm *sectorRemovalMap) managedLoad(path string) (err error) {
	srm.mu.Lock()
	defer srm.mu.Unlock()

	// open the persistence file or create a new one
	srm.removalFile, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "unable to open sector removal queue file")
	}

	// read all entries from disk.
	br := bufio.NewReader(srm.removalFile)
	buf := make([]byte, removalEntrySize)
	var hasUnusedEntries bool
	for i := int64(0); ; i++ {
		if _, err := io.ReadFull(br, buf); err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		} else if err != nil {
			return errors.AddContext(err, "error reading sector removal file")
		}

		var id sectorID
		copy(id[:], buf)

		entry, ok := srm.entries[id]
		if !ok {
			entry = removalEntry{
				ID:     id,
				Offset: i * removalEntrySize,
			}
		}
		entry.Count += binary.LittleEndian.Uint64(buf[12:])

		// skip entries that are no longer needed.
		if entry.Count == 0 {
			hasUnusedEntries = true
			continue
		}

		srm.sectors[entry.ID] = struct{}{}
		srm.entries[id] = entry
	}

	// if there are unused entries, compact the file
	if hasUnusedEntries {
		if err := srm.compactPersistFile(path); err != nil {
			return errors.AddContext(err, "error compacting sector removal file")
		}
	}
	return nil
}

// removeSectors adds sectors from the map to the WAL
func (srm *sectorRemovalMap) removeSectors(max int) ([]sectorID, error) {
	removed := make([]sectorID, 0, max)
	toRemove := make(map[sectorID]uint64)

	err := func() error {
		srm.mu.Lock()
		defer srm.mu.Unlock()

		if len(srm.sectors) == 0 {
			return nil
		}

		for id := range srm.sectors {
			entry, ok := srm.entries[id]
			if !ok {
				panic("sector removal map is missing an entry for a sector")
			}
			toRemove[id] = entry.Count
			removed = append(removed, id)

			// update the persistence file
			if err := writeRemovalEntry(srm.removalFile, id, 0, entry.Offset); err != nil {
				return errors.AddContext(err, fmt.Sprintf("error updating sector removal entry %v", id))
			}
			// update the in memory counter and remove the sector from the map
			entry.Count = 0
			srm.entries[id] = entry
			delete(srm.sectors, id)
			if len(toRemove) >= max {
				break
			}
		}
		return nil
	}()
	if err != nil {
		return nil, err
	}

	// lock all sectors that are being removed
	for id := range toRemove {
		srm.wal.managedLockSector(id)
		defer srm.wal.managedUnlockSector(id)
	}
	return removed, srm.wal.managedRemoveSectors(toRemove)
}

// threadedRemoveSectors is a gouroutine that periodically removes marked
// sectors from the host. This reduces lock contention by combining sector
// removals into one WAL operation and allows other functions to acquire the
// lock between batches. Like RemoveSectorBatch, this operation is not ACID; the
// design of the manager will be fixed in v2.
func (srm *sectorRemovalMap) threadedRemoveSectors() {
	for {
		select {
		case <-srm.cm.tg.StopChan():
			return
		case <-time.After(5 * time.Second):
			err := func() error {
				err := srm.cm.tg.Add()
				if err != nil {
					return errors.AddContext(err, "unable to add remove sector thread")
				}
				defer srm.cm.tg.Done()

				// ~3hr per physical TiB
				if _, err := srm.removeSectors(128); err != nil {
					srm.cm.log.Println("unable to remove sectors:", err)
				}
				return nil
			}()
			if err != nil {
				return
			}
		}
	}
}

// AddSectors adds a batch of sectors to the removal map. Only the map's lock is
// required since the actual removal is handled separately by
// threadedRemoveSectors.
func (srm *sectorRemovalMap) AddSectors(sectors map[sectorID]uint64) error {
	srm.mu.Lock()
	defer srm.mu.Unlock()

	for id, count := range sectors {
		entry, exists := srm.entries[id]
		if !exists {
			entry = removalEntry{
				ID:     id,
				Offset: int64(len(srm.entries) * removalEntrySize),
			}
		}
		entry.Count += count
		srm.entries[id] = entry
		srm.sectors[id] = struct{}{}
		if err := writeRemovalEntry(srm.removalFile, entry.ID, entry.Count, entry.Offset); err != nil {
			return errors.AddContext(err, fmt.Sprintf("error updating sector %v removal entry", id))
		}
	}
	return nil
}

// Close will sync and close the sector removal map.
func (srm *sectorRemovalMap) Close() error {
	srm.mu.Lock()
	defer srm.mu.Unlock()
	return errors.Compose(srm.removalFile.Sync(), srm.removalFile.Close())
}

// newSectorRemovalMap initializes a new sector removal queue to batch removal
// requests over time.
func newSectorRemovalMap(path string, cm *ContractManager) (*sectorRemovalMap, error) {
	srm := &sectorRemovalMap{
		sectors: make(map[sectorID]struct{}),
		entries: make(map[sectorID]removalEntry),
		cm:      cm,
		wal:     &cm.wal,
	}

	// load the initial state of the removal queue
	if err := srm.managedLoad(path); err != nil {
		return nil, errors.AddContext(err, "unable to load sector removal queue")
	}
	// start the removal go routine
	go srm.threadedRemoveSectors()
	return srm, nil
}

// MarkSectorsForRemoval adds sector roots to the removal map. Does not
// require the WAL lock or sector lock to be held since it is only queuing the
// removal.
func (cm *ContractManager) MarkSectorsForRemoval(sectorRoots []crypto.Hash) error {
	toRemove := make(map[sectorID]uint64)
	for _, id := range sectorRoots {
		toRemove[cm.managedSectorID(id)]++
	}
	return cm.sectorRemoval.AddSectors(toRemove)
}
