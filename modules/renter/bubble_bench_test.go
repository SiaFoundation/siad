package renter

import (
	"fmt"
	"path/filepath"
	"testing"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/siatest/dependencies"
)

// newBenchmarkRenterWithDependency creates a renter to be used for benchmarks
// on renter methods
func newBenchmarkRenterWithDependency(name string, deps modules.Dependencies) (*Renter, error) {
	testdir := build.TempDir("renter", name)
	rt, err := newRenterTesterNoRenter(testdir)
	if err != nil {
		return nil, err
	}
	r, err := newRenterWithDependency(rt.gateway, rt.cs, rt.wallet, rt.tpool, rt.mux, filepath.Join(testdir, modules.RenterDir), deps)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// BenchmarkBubbleMetadata runs a benchmark on the perform bubble metadata method
//
// Results (goos, goarch, CPU: Benchmark Output: date)
//
// linux, amd64, Intel(R) Core(TM) i7-8550U CPU @ 1.80GHz:  6 | 180163684 ns/op | 249937 B/op | 1606 allocs/op: 03/19/2020
// linux, amd64, Intel(R) Core(TM) i7-8550U CPU @ 1.80GHz: 34 |  34416443 ns/op                                 11/10/2020
// linux, amd64, Intel(R) Core(TM) i7-8550U CPU @ 1.80GHz: 15 |  75880486 ns/op                                 02/26/2021
// linux, amd64, Intel(R) Core(TM) i7-8550U CPU @ 1.80GHz: 19 |  59483353 ns/op                                 03/05/2021
func BenchmarkBubbleMetadata(b *testing.B) {
	r, err := newBenchmarkRenterWithDependency(b.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := r.Close(); err != nil {
			b.Fatal(err)
		}
	}()

	// Create Directory
	dirSiaPath, err := modules.NewSiaPath("root")
	if err != nil {
		b.Fatal(err)
	}
	err = r.CreateDir(dirSiaPath, modules.DefaultDirPerm)
	if err != nil {
		b.Fatal(err)
	}

	// Create and add 5 files
	rsc, _ := modules.NewRSCode(1, 1)
	for i := 0; i < 5; i++ {
		fileSiaPath, err := dirSiaPath.Join(fmt.Sprintf("file%v", i))
		if err != nil {
			b.Fatal(err)
		}
		up := modules.FileUploadParams{
			Source:      "",
			SiaPath:     fileSiaPath,
			ErasureCode: rsc,
		}
		err = r.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, persist.DefaultDiskPermissionsTest, up.DisablePartialChunk)
		if err != nil {
			b.Log("Dir", dirSiaPath)
			b.Log("File", fileSiaPath)
			b.Fatal(err)
		}
	}
	// Reset Timer
	b.ResetTimer()

	// Run Benchmark
	for n := 0; n < b.N; n++ {
		err := r.staticBubbleScheduler.managedPerformBubbleUpdate(dirSiaPath)
		if err != nil {
			b.Fatal(err)
		}
	}
}
