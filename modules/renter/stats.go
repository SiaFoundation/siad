package renter

import (
	"strings"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// SkynetStats returns the SkynetStats of the renter. Depending on the input,
// either cached stats will be returned or a full disk scan will either be
// started or if it's already ongoing, waited for.
func (r *Renter) SkynetStats(cached bool) (modules.SkynetStats, error) {
	for {
		r.statsMu.Lock()
		var isScanning bool
		select {
		case <-r.statsChan:
			isScanning = false
		default:
			isScanning = true
		}

		if cached && r.stats != nil {
			// If a cached value is good enough, we use that if available.
			stats := *r.stats
			r.statsMu.Unlock()
			return stats, nil
		} else if isScanning {
			// Otherwise, if a scan is happening, we wait for that to finish.
			c := r.statsChan
			r.statsMu.Unlock()
			<-c
			cached = true // a cached value is good enough now
			continue
		} else {
			// We don't have a recent enough value and no scan is happening. We
			// need to start one.
			c := make(chan struct{})
			defer close(c)
			r.statsChan = c
			r.statsMu.Unlock()

			// Trigger the scan.
			s, err := r.managedStatsScan()
			if err != nil {
				return modules.SkynetStats{}, err
			}

			// Set the new value.
			r.statsMu.Lock()
			r.stats = &s
			r.statsMu.Unlock()
			return s, err
		}
	}
}

// managedRemoveFileFromSkynetStats adds a file of the given size to the stats.
func (r *Renter) managedAddFileToSkynetStats(size uint64, extended bool) {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	if r.stats != nil {
		if !extended {
			r.stats.NumFiles++
		}
		r.stats.TotalSize += size
	}
}

// managedRemoveFileFromSkynetStats removes a file of the given size from the
// stats.
func (r *Renter) managedRemoveFileFromSkynetStats(size uint64, extended bool) {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	if r.stats != nil {
		if !extended {
			r.stats.NumFiles--
		}
		r.stats.TotalSize -= size
	}
}

// managedStatsScan scans the renter's filesystem and returns the SkynetStats.
func (r *Renter) managedStatsScan() (modules.SkynetStats, error) {
	var mu sync.Mutex
	var stats modules.SkynetStats
	err := r.FileList(modules.SkynetFolder, true, true, func(f modules.FileInfo) {
		mu.Lock()
		defer mu.Unlock()
		// do not double-count large files by counting both the header file and
		// the extended file
		if !strings.HasSuffix(f.Name(), modules.ExtendedSuffix) {
			stats.NumFiles++
		}
		stats.TotalSize += f.Filesize
	})
	return stats, err
}

// statsCacheInvalidationInterval is the interval at which the renter
// invalidates the cached skynet stats.
var statsCacheInvalidationInterval = build.Select(build.Var{
	Dev:      5 * time.Minute,
	Standard: 8 * time.Hour,
	Testing:  5 * time.Second,
}).(time.Duration)

// threadedInvalidateStatsCache periodically clears the stats. That way a call
// to the stats endpoint requires rebuilding them from disk.
// NOTE: In a perfect world this is not necessary since we would just update the
// in-memory cache whenever the state on disk changes. Unfortunately there are
// quite a few edge cases around it, including users manually deleting files.
// That's why this routine makes sure that the stats resync periodically.
func (r *Renter) threadedInvalidateStatsCache() {
	err := r.tg.Add()
	if err != nil {
		return
	}
	defer r.tg.Done()

	// Dependency injection to disable cache invalidation.
	if r.deps.Disrupt("DisableInvalidateStatsCache") {
		return
	}

	t := time.NewTicker(statsCacheInvalidationInterval)
	defer t.Stop()
	for {
		select {
		case <-r.tg.StopChan():
			return
		case <-t.C:
		}

		r.statsMu.Lock()
		r.stats = nil
		r.statsMu.Unlock()
	}
}
