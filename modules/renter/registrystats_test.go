package renter

import (
	"testing"
	"time"

	"gitlab.com/NebulousLabs/fastrand"
)

// TestReadRegistryStatsNoDecay is a unit test for the registry stats without
// decay.
func TestReadRegistryStatsNoDecay(t *testing.T) {
	decay := 1.0
	percentile := 0.5
	interval := time.Millisecond

	// Add 0ms measurement. This results in the following bucket.
	// pos:   [x]
	// est:   [1]
	// count: [1]
	// The 50th percentile should be 1.
	bs := newReadRegistryStats(0, interval, decay, percentile)
	err := bs.AddDatum(0)
	if err != nil {
		t.Fatal(err)
	}
	if bs.Estimate() != time.Millisecond {
		t.Fatal("wrong measurement", bs.Estimate())
	}
	if len(bs.staticBuckets) != 1 {
		t.Fatal("wrong number of buckets", len(bs.staticBuckets))
	}
	if bs.currentPosition != 0 {
		t.Fatal("wrong position", bs.currentPosition)
	}

	// Add interval measurement. This results in the following buckets.
	// pos:   [   x]
	// est:   [1, 2]
	// count: [0, 1]
	// The 50th percentile should be 2.
	bs = newReadRegistryStats(interval, interval, decay, percentile)
	err = bs.AddDatum(interval)
	if err != nil {
		t.Fatal(err)
	}
	if bs.Estimate() != 2*time.Millisecond {
		t.Fatal("wrong measurement", bs.Estimate())
	}
	if len(bs.staticBuckets) != 2 {
		t.Fatal("wrong number of buckets", len(bs.staticBuckets))
	}
	if bs.currentPosition != 1 {
		t.Fatal("wrong position", bs.currentPosition)
	}

	// Add larger than interval measurement.
	// pos:   [      x]
	// est:   [1, 2, 3]
	// count: [0, 0, 1]
	// The 50th percentile should be 3.
	bs = newReadRegistryStats(2*interval, interval, decay, percentile)
	err = bs.AddDatum(2 * interval)
	if err != nil {
		t.Fatal(err)
	}
	if bs.Estimate() != 3*interval {
		t.Fatal("wrong measurement", bs.Estimate())
	}
	if len(bs.staticBuckets) != 3 {
		t.Fatal("wrong number of buckets", len(bs.staticBuckets))
	}
	if bs.currentPosition != 2 {
		t.Fatal("wrong position", bs.currentPosition)
	}

	// Add measurements 0..99 exactly once.
	// pos:   [             x]
	// est:   [1, 2, ..., 100]
	// count: [1, 1, ...,   1]
	// The 50th percentile should be 50ms.
	bs = newReadRegistryStats(99*time.Millisecond, interval, decay, percentile)
	for i := 0; i <= 99; i++ {
		err = bs.AddDatum(time.Duration(i) * time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
	}
	if bs.Estimate() != 50*time.Millisecond {
		t.Fatal("wrong measurement", bs.Estimate())
	}
	if len(bs.staticBuckets) != 100 {
		t.Fatal("wrong number of buckets", len(bs.staticBuckets))
	}
	if bs.currentPosition != 49 {
		t.Fatal("wrong position", bs.currentPosition)
	}

	// Add 10 measurements for 0, 9 for 1, 8 for 2 and so on.
	// pos:   [         x               ]
	// est:   [1, 2, 3, 4, 5, 6, 7, 8, 9]
	// count: [9, 8, 7, 6, 5, 4, 3, 2, 1]
	// The total is 45 and 50% of that is 22.5. So the smallest number where 50%
	// of values are smaller than us is at index 3 where the smaller items sum
	// up to 35. Index 2 means we are in the 3-4ms bucket. So the result is 4ms.
	bs = newReadRegistryStats(9*time.Millisecond, interval, decay, percentile)
	for i := 0; i < 10; i++ {
		for j := 0; j < 10-i; j++ {
			err = bs.AddDatum(time.Duration(i) * time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if bs.Estimate() != 4*time.Millisecond {
		t.Fatal("wrong measurement", bs.Estimate())
	}
	if len(bs.staticBuckets) != 10 {
		t.Fatal("wrong number of buckets", len(bs.staticBuckets))
	}
	if bs.currentPosition != 3 {
		t.Fatal("wrong position", bs.currentPosition)
	}
}

// TestReadRegistryStatsDecay tests the decay of the read registry stats object.
func TestReadRegistryStatsDecay(t *testing.T) {
	decay := 0.5
	percentile := 0.5
	interval := time.Millisecond

	// Add 10 datapoints to 10 buckets.
	bs := newReadRegistryStats(10*time.Millisecond, interval, decay, percentile)
	for i := 0; i < 10; i++ {
		for j := 0; j < 10; j++ {
			err := bs.AddDatum(time.Millisecond * time.Duration(i))
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	// Expect 11 buckets.
	if len(bs.staticBuckets) != 11 {
		t.Fatal("wrong number of buckets", len(bs.staticBuckets))
	}

	// Expect a total of 100.
	if bs.total != 100 {
		t.Fatal("wrong total", bs.total)
	}

	// Sleep for the decay interval.
	time.Sleep(readRegistryStatsDecayInterval)

	// Add one more datapoint to bucket 11.
	err := bs.AddDatum(time.Millisecond * time.Duration(10))
	if err != nil {
		t.Fatal(err)
	}

	// Total should be 100*0.5 + 1 == 51
	if bs.total != 51 {
		t.Fatal("wrong total", bs.total)
	}
	if bs.Estimate() != 6*time.Millisecond {
		t.Fatal("wrong estimate", bs.Estimate())
	}
}

// BenchmarkAddDatum benchmarks AddDatum.
//
// maxTime | interval |   ops |                    cpu
//
//	5 min |      1ms |  3222 | i9-9880H CPU @ 2.30GHz
//	5 min |     10ms | 32263 | i9-9880H CPU @ 2.30GHz
func BenchmarkAddDatum(b *testing.B) {
	// Create stats with at most 5 minute measurements.
	// Add n datapoints to 5000 buckets.
	maxTime := 5 * time.Minute        // 5 minutes
	interval := 10 * time.Millisecond // 300,000 buckets
	bs := newReadRegistryStats(maxTime, interval, 0.95, 0.999)

	// Add one entry in the last bucket to allocate the slice.
	err := bs.AddDatum(maxTime - interval) // off-by-one
	if err != nil {
		b.Fatal(err)
	}

	// Sanity check buckets.
	if time.Duration(len(bs.staticBuckets)) != (maxTime/interval)+1 {
		b.Fatal("wrong number of buckets", len(bs.staticBuckets))
	}

	// Pregenerate n datapoints to add.
	datapoints := make([]time.Duration, b.N)
	for i := range datapoints {
		datapoints[i] = time.Duration(fastrand.Intn(len(bs.staticBuckets))) * interval
	}

	// Reset the timer.
	b.ResetTimer()

	// Run AddDatum.
	for _, dp := range datapoints {
		err := bs.AddDatum(dp)
		if err != nil {
			b.Fatal(err)
		}
		_ = bs.Estimate()
	}
}
