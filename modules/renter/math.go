package renter

import (
	"fmt"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
)

// each bucket has a number of items, this amount decays over
// time so we focus on recent event timings. We only update
// a bucket's decay when we visit it to save on performance,
// so we need to track the cumulative amount of decay so far,
// such that we can add the appropriate amount of additional
// decay on the next visit.
type bucketSet struct {
	maxTime  time.Duration
	interval time.Duration

	buckets []float64

	currentPosition int
	itemsLarger     float64
	itemsSmaller    float64

	lastDecay   time.Time
	staticDecay float64

	staticPercentile float64

	mu sync.Mutex
}

func (bs *bucketSet) decayBuckets() {
	var smaller, larger float64
	for i := range bs.buckets {
		bs.buckets[i] *= bs.staticDecay
		if i > bs.currentPosition {
			larger += bs.buckets[i]
		} else {
			smaller += bs.buckets[i]
		}
	}
	bs.itemsSmaller = smaller
	bs.itemsLarger = larger
	bs.lastDecay = time.Now()
}

func (bs *bucketSet) AddDatum(duration time.Duration) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	// Decay all buckets before adding another duration.
	bs.decayBuckets()

	// Decay the bs if more than a second has passed.
	// We need the bucket set to sample at least 10k events
	// so we don't decay more than one seconds worth even if
	// more than one second has passed.
	if time.Since(bs.lastDecay) > time.Second {
		bs.decayBuckets()
	}

	// Add the new data to the appropriate bucket.
	bi := int(duration / bs.interval)

	// Check if the buckets need to be extended.
	if bi >= len(bs.buckets) {
		bs.buckets = append(bs.buckets, make([]float64, bi-len(bs.buckets)+1)...)
	}
	bs.buckets[bi]++

	// Update itemsLarger and itemsSmaller based on our position.
	if bi > bs.currentPosition {
		bs.itemsLarger++
	} else {
		bs.itemsSmaller++
	}

	// Sanity check for zero division.
	if bs.itemsSmaller+bs.itemsLarger == 0 {
		build.Critical("bs.itemsSmaller + bs.itemsLarger == 0 - forgot to seed?")
		return
	}

	// Update our position based on the new data. Have one loop
	// to increment our position as long as it makes sense, and
	// one loop to decrement our position as long as it makes sense.
	// The result should be that we end up in the smallest position where
	// 99.9% of items are smaller than us.
	for bs.currentPosition > 0 && bs.itemsSmaller/(bs.itemsSmaller+bs.itemsLarger) > bs.staticPercentile {
		b := bs.buckets[bs.currentPosition]
		bs.itemsSmaller -= b
		bs.itemsLarger += b
		bs.currentPosition--
	}
	for bs.currentPosition < len(bs.buckets)-1 && bs.itemsSmaller/(bs.itemsSmaller+bs.itemsLarger) < bs.staticPercentile {
		fmt.Println("pos", bs.currentPosition, bs.itemsSmaller, bs.itemsLarger)
		b := bs.buckets[bs.currentPosition]
		bs.itemsSmaller += b
		bs.itemsLarger -= b
		bs.currentPosition++
	}
}

// Estimate returns the current estimate.
func (bs *bucketSet) Estimate() time.Duration {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return time.Duration(bs.currentPosition+1) * bs.interval
}

func newBucketSet(interval time.Duration, decay, percentile float64) *bucketSet {
	return &bucketSet{
		interval:         interval,
		staticDecay:      decay,
		staticPercentile: percentile,
	}
}
