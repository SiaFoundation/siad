package siadir

import (
	"testing"

	"go.sia.tech/siad/modules"
)

// BenchmarkSaveSiaDir runs a benchmark on the saveDir method of the siadir
// package
//
// Results (goos, goarch, CPU: Benchmark Output: date)
//
// linux, amd64, Intel(R) Core(TM) i7-8550U CPU @ 1.80GHz: 62574 |  17407 ns/op 03/08/2021
func BenchmarkSaveSiaDir(b *testing.B) {
	// Get a test directory
	testDir, err := newSiaDirTestDir(b.Name())
	if err != nil {
		b.Fatal(err)
	}

	// Define metadata
	md := randomMetadata()
	deps := modules.ProdDependencies

	// Reset Timer
	b.ResetTimer()

	// Run Benchmark
	for n := 0; n < b.N; n++ {
		err := saveDir(testDir, md, deps)
		if err != nil {
			b.Fatal(err)
		}
	}
}
