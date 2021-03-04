package renter

import (
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
)

// readRegistryStats collects stats about read registry jobs. each bucket has a
// number of items, this amount decays over time so we focus on recent event
// timings. We only update a bucket's decay when we visit it to save on
// performance, so we need to track the cumulative amount of decay so far, such
// that we can add the appropriate amount of additional decay on the next visit.
type readRegistryStats struct {
	buckets          []float64
	currentPosition  int
	interval         time.Duration
	lastDecay        time.Time
	staticDecay      float64
	staticPercentile float64
	total            float64

	mu sync.Mutex
}

// AddDatum adds a new datapoint to the stats.
func (rrs *readRegistryStats) AddDatum(duration time.Duration) {
	rrs.mu.Lock()
	defer rrs.mu.Unlock()

	// Figure out if we need to decay this time. We need the bucket set to
	// sample at least 10k events so we don't decay more than one seconds worth
	// even if more than one second has passed.
	decay := time.Since(rrs.lastDecay) > time.Second
	if decay {
		rrs.lastDecay = time.Now()
	}

	// Check if the buckets need to be extended.
	bi := int(duration / rrs.interval)
	if bi >= len(rrs.buckets) {
		rrs.buckets = append(rrs.buckets, make([]float64, bi-len(rrs.buckets)+1)...)
	}

	// Add the new data to the total and decay it if necessary before doing so.
	if decay {
		rrs.total *= rrs.staticDecay
	}
	rrs.total++

	// Loop over all buckets and find the new current position. It's the first
	// index where smaller / total >= percentile.
	smaller := 0.0
	larger := rrs.total
	rrs.currentPosition = -1
	for i := range rrs.buckets {
		// Decay the bucket if necessary.
		if decay {
			rrs.buckets[i] *= rrs.staticDecay
		}
		// Add to the bucket if necessary.
		if i == bi {
			rrs.buckets[i]++
		}
		// Increment smaller and decrement larger as we continue.
		larger -= rrs.buckets[i]
		smaller += rrs.buckets[i]
		// If the condition is met for the current position, set it.
		if rrs.currentPosition == -1 && smaller/rrs.total >= rrs.staticPercentile {
			rrs.currentPosition = i
		}
	}
	// Sanity check position. It should always be set at this point.
	if rrs.currentPosition == -1 {
		build.Critical("current position wasn't set")
		rrs.currentPosition = 0
		return
	}
}

// Estimate returns the current estimate.
func (rrs *readRegistryStats) Estimate() time.Duration {
	rrs.mu.Lock()
	defer rrs.mu.Unlock()
	return time.Duration(rrs.currentPosition+1) * rrs.interval
}

// newReadRegistryStats creates new stats from a given decay and percentile.
func newReadRegistryStats(interval time.Duration, decay, percentile float64) *readRegistryStats {
	return &readRegistryStats{
		interval:         interval,
		staticDecay:      decay,
		staticPercentile: percentile,
	}
}
