package renter

import (
	"fmt"
	"testing"
	"time"
)

// TestReadRegistryStats is a unit test for the registry stats.
func TestReadRegistryStats(t *testing.T) {
	decay := 1.0
	percentile := 0.5
	interval := time.Millisecond

	// Add 0ms measurement. This results in the following bucket.
	// pos:   [x]
	// est:   [1]
	// count: [1]
	// The 50th percentile should be 1.
	bs := newReadRegistryStats(interval, decay, percentile)
	bs.AddDatum(0)
	if bs.Estimate() != time.Millisecond {
		t.Fatal("wrong measurement", bs.Estimate())
	}
	if len(bs.buckets) != 1 {
		t.Fatal("wrong number of buckets", len(bs.buckets))
	}
	if bs.currentPosition != 0 {
		t.Fatal("wrong position", bs.currentPosition)
	}

	// Add interval measurement. This results in the following buckets.
	// pos:   [   x]
	// est:   [1, 2]
	// count: [0, 1]
	// The 50th percentile should be 2.
	bs = newReadRegistryStats(interval, decay, percentile)
	bs.AddDatum(interval)
	if bs.Estimate() != 2*time.Millisecond {
		t.Fatal("wrong measurement", bs.Estimate())
	}
	if len(bs.buckets) != 2 {
		t.Fatal("wrong number of buckets", len(bs.buckets))
	}
	if bs.currentPosition != 1 {
		t.Fatal("wrong position", bs.currentPosition)
	}

	// Add larger than interval measurement.
	// pos:   [      x]
	// est:   [1, 2, 3]
	// count: [0, 0, 1]
	// The 50th percentile should be 3.
	bs = newReadRegistryStats(interval, decay, percentile)
	bs.AddDatum(2 * interval)
	if bs.Estimate() != 3*interval {
		t.Fatal("wrong measurement", bs.Estimate())
	}
	if len(bs.buckets) != 3 {
		t.Fatal("wrong number of buckets", len(bs.buckets))
	}
	if bs.currentPosition != 2 {
		t.Fatal("wrong position", bs.currentPosition)
	}

	// Add measurements 0..99 exactly once.
	// pos:   [             x]
	// est:   [1, 2, ..., 100]
	// count: [1, 1, ...,   1]
	// The 50th percentile should be 50ms.
	bs = newReadRegistryStats(interval, decay, percentile)
	for i := 0; i <= 99; i++ {
		bs.AddDatum(time.Duration(i) * time.Millisecond)
	}
	if bs.Estimate() != 50*time.Millisecond {
		t.Fatal("wrong measurement", bs.Estimate())
	}
	if len(bs.buckets) != 100 {
		t.Fatal("wrong number of buckets", len(bs.buckets))
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
	bs = newReadRegistryStats(interval, decay, percentile)
	for i := 0; i < 10; i++ {
		for j := 0; j < 10-i; j++ {
			fmt.Println("adding", time.Duration(i)*time.Millisecond)
			bs.AddDatum(time.Duration(i) * time.Millisecond)
		}
	}
	if bs.Estimate() != 4*time.Millisecond {
		t.Fatal("wrong measurement", bs.Estimate())
	}
	if len(bs.buckets) != 10 {
		t.Fatal("wrong number of buckets", len(bs.buckets))
	}
	if bs.currentPosition != 3 {
		t.Fatal("wrong position", bs.currentPosition)
	}
}
