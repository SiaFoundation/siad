package contractmanager

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
)

// TestRemovalMapPersistence tests that the removal persistence file
// is written and loaded correctly.
func TestRemovalMapPersistence(t *testing.T) {
	cm, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()

	queueFile := filepath.Join(t.TempDir(), sectorRemovalQueueFile)
	// manually create a new removal queue and initialize the persistence file.
	sq := &sectorRemovalMap{
		entries: make(map[sectorID]removalEntry),
		sectors: make(map[sectorID]struct{}),
		cm:      cm,
		wal:     &cm.wal,
	}
	sq.managedLoad(queueFile)

	// generate random sector IDs
	sectorIDs := make([]sectorID, 1000)
	for i := range sectorIDs {
		copy(sectorIDs[i][:], fastrand.Bytes(12))
	}

	sectorRemovals := make(map[sectorID]uint64)
	// add half of the sectors to the removal queue
	for i := range sectorIDs[:500] {
		sectorRemovals[sectorIDs[i]] = fastrand.Uint64n(1e4) + 1
	}
	if err := sq.AddSectors(sectorRemovals); err != nil {
		t.Fatal(err)
	}

	// helper func to check the internal state of the queue.
	checkInternalFields := func(sectors, entries uint64) error {
		// check that the internal fields are correct
		if uint64(len(sq.sectors)) != sectors {
			return fmt.Errorf("unexpected number of sectors in removal queue, expected %v got %v", sectors, len(sq.sectors))
		} else if uint64(len(sq.entries)) != entries {
			return fmt.Errorf("unexpected number of entries in removal queue, expected %v got %v", entries, sq.entries)
		}

		// check that the ids and counters match the expected values
		for id, count := range sectorRemovals {
			if _, ok := sq.sectors[id]; !ok {
				return fmt.Errorf("sector %v not in removal queue", id)
			} else if sq.entries[id].Count != count {
				return fmt.Errorf("unexpected sector removal count for sector %s, expected %v got %v", hex.EncodeToString(id[:]), count, sq.entries[id].Count)
			}
		}
		return nil
	}

	if err := checkInternalFields(500, 500); err != nil {
		t.Fatal(err)
	}

	// close the removal queue to flush it to disk
	if err := sq.Close(); err != nil {
		t.Fatal(err)
	}

	// check the persisted file size
	fi, err := os.Stat(queueFile)
	if err != nil {
		t.Fatal(err)
	} else if fi.Size() != 500*removalEntrySize {
		t.Fatalf("unexpected file size expected %v got %v", 500*removalEntrySize, fi.Size())
	}

	// reinitialize the queue and load the persistence file from disk
	sq = &sectorRemovalMap{
		entries: make(map[sectorID]removalEntry),
		sectors: make(map[sectorID]struct{}),
		cm:      cm,
		wal:     &cm.wal,
	}
	sq.managedLoad(queueFile)

	// check the internal fields are correct
	if err := checkInternalFields(500, 500); err != nil {
		t.Fatal(err)
	}

	// remove half of the sectors from the removal queue
	removed, err := sq.removeSectors(250)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range removed {
		delete(sectorRemovals, id)
	}

	// check that the internal fields are correct. The number of sectors should
	// change, but the entries should only change when the file is compacted on
	// reload.
	if err := checkInternalFields(250, 500); err != nil {
		t.Fatal(err)
	}

	// add a few of the sectors back to the queue to test duplicate entries
	// are not added to the persistence file.
	readd := make(map[sectorID]uint64)
	for _, id := range removed[:10] {
		sectorRemovals[id] = fastrand.Uint64n(1e4) + 1
		readd[id] = sectorRemovals[id]
	}
	if err := sq.AddSectors(readd); err != nil {
		t.Fatal(err)
	}

	// check that the internal fields are correct. The number of sectors should
	// change, but the entries should only change when the file is compacted on
	// reload.
	if err := checkInternalFields(260, 500); err != nil {
		t.Fatal(err)
	}

	// close the removal queue to flush it to disk
	if err := sq.Close(); err != nil {
		t.Fatal(err)
	}

	// check the persisted file size; the size should not change until the file
	// is reloaded.
	fi, err = os.Stat(queueFile)
	if err != nil {
		t.Fatal(err)
	} else if fi.Size() != 500*removalEntrySize {
		t.Fatalf("unexpected file size expected %v got %v", 500*removalEntrySize, fi.Size())
	}

	// reload the persistence file and verify the file was compacted
	sq = &sectorRemovalMap{
		entries: make(map[sectorID]removalEntry),
		sectors: make(map[sectorID]struct{}),
		cm:      cm,
		wal:     &cm.wal,
	}
	sq.managedLoad(queueFile)

	// check that the internal fields are correct. The entry and sector counts
	// should be the new values; the removed sectors should be gone.
	if err := checkInternalFields(260, 260); err != nil {
		t.Fatal(err)
	}

	// check the persistence was properly compacted
	fi, err = os.Stat(queueFile)
	if err != nil {
		t.Fatal(err)
	} else if fi.Size() != 260*removalEntrySize {
		t.Fatalf("unexpected file size expected %v got %v", 260*removalEntrySize, fi.Size())
	} else if err := sq.Close(); err != nil {
		t.Fatal(err)
	}

	// check that the compacted persistence file still loads correctly
	sq = &sectorRemovalMap{
		entries: make(map[sectorID]removalEntry),
		sectors: make(map[sectorID]struct{}),
		cm:      cm,
		wal:     &cm.wal,
	}
	sq.managedLoad(queueFile)
	defer sq.Close()

	// the internal fields should still have the same values
	if err := checkInternalFields(260, 260); err != nil {
		t.Fatal(err)
	}
}

func TestMarkSectorsForRemoval(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := t.TempDir()
	cm, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		cm.Close()
	})

	// add a storage folder to the contract manager
	if err := cm.AddStorageFolder(t.TempDir(), modules.SectorSize*1024); err != nil {
		t.Fatal(err)
	}

	// generate random physical sectors
	sectors := 256
	sectorCounts := make(map[crypto.Hash]uint64)

	var wg sync.WaitGroup
	var mu sync.Mutex
	wg.Add(sectors)
	sema := make(chan struct{}, 100)
	for i := 0; i < sectors; i++ {
		sema <- struct{}{}
		go func(i int) {
			defer func() {
				wg.Done()
				<-sema
			}()

			buf := make([]byte, modules.SectorSize)
			fastrand.Read(buf[:128])
			root := crypto.MerkleRoot(buf)
			if err := cm.AddSector(root, buf); err != nil {
				panic(err)
			}

			// add virtual copies of the sector
			n := fastrand.Uint64n(50)
			for j := uint64(0); j < n; j++ {
				if err := cm.AddSector(root, buf); err != nil {
					panic(err)
				}
			}

			// update the counter for the sector
			mu.Lock()
			sectorCounts[root] += n + 1
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// helper function to check the contract manager's internal sector count
	// matches the expected counts
	verifySectorCounts := func() error {
		cm.sectorMu.Lock()
		defer cm.sectorMu.Unlock()

		for id, count := range sectorCounts {
			if cm.sectorLocations[cm.managedSectorID(id)].count != count {
				return fmt.Errorf("unexpected sector count for sector %v, expected %v got %v", id, count, cm.sectorLocations[cm.managedSectorID(id)].count)
			}
		}
		return nil
	}

	// check the internal sector counts match the expected values
	if err := verifySectorCounts(); err != nil {
		t.Fatal(err)
	}

	// remove half of each sectors virtual sectors
	var toRemove []crypto.Hash
	for id, count := range sectorCounts {
		n := count / 2
		for i := uint64(0); i < n; i++ {
			toRemove = append(toRemove, id)
		}
		sectorCounts[id] -= n
	}
	if err := cm.MarkSectorsForRemoval(toRemove); err != nil {
		t.Fatal(err)
	}

	// check the internal sector counts do not match the post-removal values
	if err := verifySectorCounts(); err == nil {
		t.Fatal("sector counts should not match immediately after MarkSectorsForRemoval")
	}

	// wait for the removal sync loop to complete
	err = build.Retry(20, time.Second, func() error {
		// check the internal sector counts match the expected values
		if err := verifySectorCounts(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// remove the remaining sectors
	toRemove = toRemove[:0]
	for id, count := range sectorCounts {
		for i := uint64(0); i < count; i++ {
			toRemove = append(toRemove, id)
		}
		sectorCounts[id] -= count
	}
	if err := cm.MarkSectorsForRemoval(toRemove); err != nil {
		t.Fatal(err)
	}

	// immediately close the contract manager so that the removal process is
	// interrupted
	cm.Close()

	// reinitialize the contract manager
	cm, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}

	// check the internal sector counts do not match the post-removal values
	if err := verifySectorCounts(); err == nil {
		t.Fatal("sector counts should not match immediately after MarkSectorsForRemoval")
	}

	// wait for the removal sync loop to complete
	err = build.Retry(20, time.Second, func() error {
		// check the internal sector counts match the expected values
		if err := verifySectorCounts(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
