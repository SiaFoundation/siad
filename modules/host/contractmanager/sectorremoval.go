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
		mu          sync.Mutex
		sectors     map[sectorID]removalEntry
		entries     int64
		removalFile *os.File

		cm  *ContractManager
		wal *writeAheadLog
	}
)

// writes the removal entry to disk
func (srm *sectorRemovalMap) writeRemovalEntry(id sectorID, count uint64, offset int64) error {
	buf := make([]byte, removalEntrySize)
	copy(buf[:], id[:])
	binary.LittleEndian.PutUint64(buf[12:], count)
	_, err := srm.removalFile.WriteAt(buf, offset)
	return err
}

// managedLoad loads the removal queue from disk. Entries with a count of zero
// and read errors are ignored. The file is compacted while loading.
func (srm *sectorRemovalMap) managedLoad(path string) (err error) {
	srm.mu.Lock()
	defer srm.mu.Unlock()

	srm.removalFile, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "unable to open sector removal queue file")
	}
	br := bufio.NewReader(srm.removalFile)
	buf := make([]byte, removalEntrySize)
	// read each entry from disk. The persistence file is compacted while
	// reading.
	for i := int64(0); ; i++ {
		if _, err := io.ReadFull(br, buf); err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		} else if err != nil {
			return errors.AddContext(err, "error reading sector removal file")
		}

		entry := removalEntry{
			Count:  binary.LittleEndian.Uint64(buf[12:]),
			Offset: srm.entries * removalEntrySize,
		}
		copy(entry.ID[:], buf)
		// skip entries that are no longer needed.
		if entry.Count == 0 {
			continue
		}

		srm.sectors[entry.ID] = entry
		// if the number of read entries is greater than the number of used
		// entries overwrite the unused entries.
		if srm.entries != i {
			if err := srm.writeRemovalEntry(entry.ID, entry.Count, entry.Offset); err != nil {
				return errors.AddContext(err, "error updating sector removal entry")
			}
		}
		// increment the number of entries
		srm.entries++
	}

	// truncate the file to the last entry written then sync to disk.
	if err := srm.removalFile.Truncate(int64(srm.entries * removalEntrySize)); err != nil {
		return errors.AddContext(err, "error truncating sector removal file")
	} else if err := srm.removalFile.Sync(); err != nil {
		return errors.AddContext(err, "error syncing sector removal file")
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

		for id, entry := range srm.sectors {
			toRemove[id] = entry.Count
			removed = append(removed, id)

			// update the persistence file
			if err := srm.writeRemovalEntry(id, 0, entry.Offset); err != nil {
				return errors.AddContext(err, fmt.Sprintf("error updating sector removal entry %v", id))
			}
			// remove the entry from the map
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
		entry, exists := srm.sectors[id]
		if !exists {
			entry = removalEntry{
				ID:     id,
				Offset: srm.entries * removalEntrySize,
			}
			srm.entries++
		}
		entry.Count += count
		srm.sectors[id] = entry
		if err := srm.writeRemovalEntry(entry.ID, entry.Count, entry.Offset); err != nil {
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
		sectors: make(map[sectorID]removalEntry),
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
