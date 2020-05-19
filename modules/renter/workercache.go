package renter

import (
	"sync/atomic"
	"time"
	"unsafe"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

var (
	// workerCacheUpdateFrequency specifies how much time must pass before the
	// worker updates its cache.
	workerCacheUpdateFrequency = build.Select(build.Var{
		Dev:      time.Second * 5,
		Standard: time.Minute,
		Testing:  time.Second,
	}).(time.Duration)
)

type (
	// workerCache contains all of the cached values for the worker. Every field
	// must be static because this object is saved and loaded using
	// atomic.Pointer.
	workerCache struct {
		staticBlockHeight     types.BlockHeight
		staticContractID      types.FileContractID
		staticContractUtility modules.ContractUtility
		staticHostVersion     string
		staticSynced          bool

		staticLastUpdate time.Time
	}
)

// staticUpdatedCache performs the actual worker cache update.
func (w *worker) staticUpdatedCache() *workerCache {
	// Grab the host to check the version.
	host, ok, err := w.renter.hostDB.Host(w.staticHostPubKey)
	if !ok || err != nil {
		w.renter.log.Printf("Worker %v could not update the cache, hostdb found host %v, with error: %v", w.staticHostPubKeyStr, ok, err)
		return nil
	}

	// Grab the renter contract from the host contractor.
	renterContract, exists := w.renter.hostContractor.ContractByPublicKey(w.staticHostPubKey)
	if !exists {
		w.renter.log.Printf("Worker %v could not update the cache, host not found in contractor", w.staticHostPubKeyStr)
		return nil
	}

	// Create the cache object.
	return &workerCache{
		staticBlockHeight:     w.renter.cs.Height(),
		staticContractID:      renterContract.ID,
		staticContractUtility: renterContract.Utility,
		staticHostVersion:     host.Version,
		staticSynced:          w.renter.cs.Synced(),

		staticLastUpdate: time.Now(),
	}
}

// staticTryUpdateCache will perform a cache update on the worker.
//
// 'false' will be returned if the cache cannot be updated, signaling that the
// worker should exit.
func (w *worker) staticTryUpdateCache() bool {
	// Check if an update is necessary. If not, return success.
	cache := w.staticCache()
	if cache != nil && time.Since(cache.staticLastUpdate) < workerCacheUpdateFrequency {
		return true
	}

	// Get the new cache.
	newCache := w.staticUpdatedCache()
	if newCache == nil {
		return false
	}

	// Wake the worker when the cache needs to be updated again.
	w.renter.tg.AfterFunc(workerCacheUpdateFrequency, func() {
		w.staticWake()
	})

	// Atomically store the cache object in the worker.
	ptr := unsafe.Pointer(newCache)
	atomic.StorePointer(&w.atomicCache, ptr)
	return true
}

// staticCache returns the current worker cache object.
func (w *worker) staticCache() *workerCache {
	ptr := atomic.LoadPointer(&w.atomicCache)
	return (*workerCache)(ptr)
}
