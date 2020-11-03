package renter

import (
	"encoding/binary"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestRegistryCache tests the in-memory registry type.
func TestRegistryCache(t *testing.T) {
	numEntries := uint64(100)
	cacheSize := numEntries * cachedEntryEstimatedSize

	// Create the cache and check its maxEntries field.
	cache := newRegistryCache(cacheSize)
	if cache.maxEntries != numEntries {
		t.Fatalf("maxEntries %v != %v", cache.maxEntries, numEntries)
	}

	// Get a public key.
	pk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       fastrand.Bytes(crypto.PublicKeySize),
	}

	// Declare a helper to create registry values.
	registryValue := func(n, revNum uint64) modules.SignedRegistryValue {
		var tweak crypto.Hash
		binary.LittleEndian.PutUint64(tweak[:], n)
		return modules.NewSignedRegistryValue(tweak, []byte{}, revNum, crypto.Signature{})
	}

	// Set an entry.
	rv := registryValue(0, 0)
	cache.Set(pk, rv)
	if len(cache.entryMap) != 1 || len(cache.entryList) != 1 {
		t.Fatal("map and list should both have 1 element")
	}

	// Set it again with a higher revision.
	rv = registryValue(0, 1)
	cache.Set(pk, rv)
	if len(cache.entryMap) != 1 || len(cache.entryList) != 1 {
		t.Fatal("map and list should both have 1 element")
	}

	// Make sure the value can be retrieved.
	readRev, exists := cache.Get(pk, rv.Tweak)
	if !exists || readRev != 1 {
		t.Fatal("get returned wrong value", exists, readRev)
	}

	// Fill up the cache with numEntries-1 more entries. All have revision
	// number 1.
	for i := uint64(1); i < numEntries; i++ {
		cache.Set(pk, registryValue(i, 1))
	}
	if uint64(len(cache.entryMap)) != numEntries || uint64(len(cache.entryList)) != numEntries {
		t.Fatal("map and list should both have numEntries element")
	}

	// Add one more element. This time with revision number 2.
	rv = registryValue(numEntries, 2)
	cache.Set(pk, rv)

	// The datastructures should still have the same length since a random
	// element was evicted.
	if uint64(len(cache.entryMap)) != numEntries || uint64(len(cache.entryList)) != numEntries {
		t.Fatal("map and list should both have numEntries element")
	}

	// Both datastructures should contain an entry with number 2.
	found := false
	for _, v := range cache.entryMap {
		if v.revision == 2 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("new entry wasn't found")
	}
	found = false
	for _, v := range cache.entryList {
		if v.revision == 2 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("new entry wasn't found")
	}

	// Get should return the same entry.
	readRev, exists = cache.Get(pk, rv.Tweak)
	if !exists || readRev != 2 {
		t.Fatal("get returned wrong value", exists, readRev)
	}
}
