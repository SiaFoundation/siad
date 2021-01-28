package modules

import (
	"testing"
	"time"
)

// BenchmarkHalfLifeDistribution creates a benchmark to estimate how long it
// takes to add a performance number to a half life distribution bucket.
//
// i7 processor: 1600 nanoseconds per new stat.
func BenchmarkHalfLifeDistribution(b *testing.B) {
	hld := NewHalfLifeDistribution()
	for i := 0; i < b.N; i++ {
		hld.AddRequest(time.Millisecond, 0)
	}
}
