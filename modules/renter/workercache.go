package renter

import (
	"fmt"
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

// managedUpdateCache performs the actual worker cache update. The function is
// managed because it calls exported functions on the hostdb and on the
// consensus set.
//
// NOTE: The concurrency around the atomicCacheUpdating value is a little bit
// annoying. You can't just use 'defer atmoic.StoreUint64()` because you need to
// update the value before calling tg.AfterFunc at the end of the function.
func (w *worker) managedUpdateCache() {
	// Check if there is already a cache update in progress. If not, atomically
	// signal that a cache update is in progress.
	if !atomic.CompareAndSwapUint64(&w.atomicCacheUpdating, 0, 1) {
		return
	}

	// Grab the host to check the version.
	host, ok, err := w.renter.hostDB.Host(w.staticHostPubKey)
	if !ok || err != nil {
		w.renter.log.Printf("Worker %v could not update the cache, hostdb found host %v, with error: %v, worker being killed", w.staticHostPubKeyStr, ok, err)
		w.managedKill()
		atomic.StoreUint64(&w.atomicCacheUpdating, 0)
		return
	}

	// Grab the renter contract from the host contractor.
	renterContract, exists := w.renter.hostContractor.ContractByPublicKey(w.staticHostPubKey)
	if !exists {
		w.renter.log.Printf("Worker %v could not update the cache, host not found in contractor, worker being killed", w.staticHostPubKeyStr)
		w.managedKill()
		atomic.StoreUint64(&w.atomicCacheUpdating, 0)
		return
	}

	// Grab the current cache object.
	current := w.staticCache()

	// Create the cache object.
	newCache := &workerCache{
		staticBlockHeight:     w.renter.cs.Height(),
		staticContractID:      renterContract.ID,
		staticContractUtility: renterContract.Utility,
		staticHostVersion:     host.Version,
		staticSynced:          w.renter.cs.Synced(),

		staticLastUpdate: time.Now(),
	}

	// Atomically store the cache object in the worker.
	ptr := unsafe.Pointer(newCache)
	atomic.StorePointer(&w.atomicCache, ptr)

	// If the renter goes from being unsynced to being synced, we want to
	// validate the host blockheight on the price table object and take
	// appropriate actions when we find the host is unsynced.
	var hostUnsynced bool
	if !current.staticSynced && newCache.staticSynced {
		rbh := newCache.staticBlockHeight
		hbh := w.staticPriceTable().staticPriceTable.HostBlockHeight
		if rbh >= priceTableHostBlockHeightLeeWay {
			if hbh < rbh-priceTableHostBlockHeightLeeWay || hbh > rbh+priceTableHostBlockHeightLeeWay {
				w.mu.Lock()
				w.cooldownUntil = cooldownUntil(w.consecutiveFailures)
				w.consecutiveFailures++
				w.recentErr = fmt.Errorf("worker for host %v is being put on cooldown because the host is unsynced, renter height: %v host height: %v", w.staticHostPubKeyStr, rbh, hbh)
				w.recentErrTime = time.Now()
				w.renter.log.Println(w.recentErr)
				w.mu.Unlock()
				hostUnsynced = true
			}
		}
	}
	if !hostUnsynced {
		w.mu.Lock()
		w.consecutiveFailures = 0
		w.mu.Unlock()
	}

	// Wake the worker when the cache needs to be updated again. Note that we
	// need to signal the cache update is complete before waking the worker,
	// just in case a bizarre race condition means that the worker wakes
	// immediately, then sees that an update is in progress, then fails to
	// update its cache.
	atomic.StoreUint64(&w.atomicCacheUpdating, 0)
	w.renter.tg.AfterFunc(workerCacheUpdateFrequency, func() {
		w.staticWake()
	})
}

// staticTryUpdateCache will perform a cache update on the worker.
//
// 'false' will be returned if the cache cannot be updated, signaling that the
// worker should exit.
func (w *worker) staticTryUpdateCache() {
	// Check if an update is necessary.
	cache := w.staticCache()
	if cache != nil && time.Since(cache.staticLastUpdate) < workerCacheUpdateFrequency {
		return
	}

	// Get the new cache in a goroutine. This is because the cache update grabs
	// a lock on the consensus object, which can sometimes take a while if there
	// are new blocks being processed or a reorg being processed.
	w.renter.tg.Launch(w.managedUpdateCache)
}

// staticCache returns the current worker cache object.
func (w *worker) staticCache() *workerCache {
	ptr := atomic.LoadPointer(&w.atomicCache)
	return (*workerCache)(ptr)
}
