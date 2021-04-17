package renter

import (
	"sync"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

type (
	// registryRevisionCache is a helper type to cache information about registry values
	// in memory. It decides randomly which entries to evict to make it more
	// unpredictable for the host.
	registryRevisionCache struct {
		entryMap   map[crypto.Hash]*cachedEntry
		entryList  []*cachedEntry
		maxEntries uint64
		mu         sync.Mutex
	}

	// cachedEntry describes a single cached entry. To make sure we can cache as
	// many entries as possible, this only contains the necessary information.
	cachedEntry struct {
		key      crypto.Hash
		revision uint64
	}
)

// cachedEntryEstimatedSize is the estimated size of a cachedEntry in memory.
// hash + revision + overhead of 2 pointers
const cachedEntryEstimatedSize = 32 + 8 + 16

// newRegistryCache creates a new registry cache.
func newRegistryCache(size uint64) *registryRevisionCache {
	return &registryRevisionCache{
		entryMap:   make(map[crypto.Hash]*cachedEntry),
		entryList:  nil,
		maxEntries: size / cachedEntryEstimatedSize,
	}
}

// Get fetches an entry from the cache.
func (rc *registryRevisionCache) Get(pubKey types.SiaPublicKey, tweak crypto.Hash) (uint64, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	mapKey := crypto.HashAll(pubKey, tweak)
	cachedEntry, exists := rc.entryMap[mapKey]
	if !exists {
		return 0, false
	}
	return cachedEntry.revision, true
}

// Set sets an entry in the registry. When 'force' is false, settings a lower
// revision number will be a no-op.
func (rc *registryRevisionCache) Set(pubKey types.SiaPublicKey, rv modules.SignedRegistryValue, force bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	// Check if entry already exists.
	mapKey := crypto.HashAll(pubKey, rv.Tweak)
	ce, exists := rc.entryMap[mapKey]

	// If it does, update the revision.
	if exists && (rv.Revision > ce.revision || force) {
		ce.revision = rv.Revision
		return
	} else if exists {
		return
	}

	// If it doesn't, create a new one.
	ce = &cachedEntry{
		key:      mapKey,
		revision: rv.Revision,
	}
	rc.entryMap[mapKey] = ce
	rc.entryList = append(rc.entryList, ce)

	// Make sure we stay within maxEntries.
	for uint64(len(rc.entryList)) > rc.maxEntries {
		// Figure out which entry to delete.
		idx := fastrand.Intn(len(rc.entryList))
		toDelete := rc.entryList[idx]

		// Delete it from the map.
		delete(rc.entryMap, toDelete.key)

		// Delete it from the list.
		rc.entryList[idx] = rc.entryList[len(rc.entryList)-1]
		rc.entryList = rc.entryList[:len(rc.entryList)-1]
	}
}
