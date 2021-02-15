package renter

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
)

// BenchmarkBubbleMetadata runs a benchmark on the bubble metadata method
//
// Results (goos, goarch, CPU: Benchmark Output: date)
//
// linux, amd64, Intel(R) Core(TM) i7-8550U CPU @ 1.80GHz:  6 | 180163684 ns/op | 249937 B/op | 1606 allocs/op: 03/19/2020
// linux, amd64, Intel(R) Core(TM) i7-8550U CPU @ 1.80GHz: 34 |  34416443 ns/op                                 11/10/2020
//
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
		err := r.managedBubbleMetadata(dirSiaPath)
		if err != nil {
			b.Fatal(err)
		}
	}
}

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

// TestCalculateFileMetadatas probes the calculate file metadata methods of the
// renter.
func TestCalculateFileMetadatas(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}

	// Add files
	var siaPaths []modules.SiaPath
	for i := 0; i < 5; i++ {
		sf, err := rt.renter.newRenterTestFile()
		if err != nil {
			t.Fatal(err)
		}
		siaPath := rt.renter.staticFileSystem.FileSiaPath(sf)
		siaPaths = append(siaPaths, siaPath)
	}

	// Generate host maps
	hostOfflineMap, hostGoodForRenewMap, _, _ := rt.renter.managedRenterContractsAndUtilities()

	// calculate metadatas individually
	var mds1 []bubbledSiaFileMetadata
	for _, siaPath := range siaPaths {
		md, err := rt.renter.managedCalculateFileMetadata(siaPath, hostOfflineMap, hostGoodForRenewMap)
		if err != nil {
			t.Fatal(err)
		}
		mds1 = append(mds1, md)
	}

	// calculate metadatas together
	mds2, err := rt.renter.managedCalculateFileMetadatas(siaPaths)
	if err != nil {
		t.Fatal(err)
	}

	// sort by siapath
	sort.Slice(mds1, func(i, j int) bool {
		return strings.Compare(mds1[i].sp.String(), mds1[j].sp.String()) < 0
	})
	sort.Slice(mds2, func(i, j int) bool {
		return strings.Compare(mds2[i].sp.String(), mds2[j].sp.String()) < 0
	})

	// Compare the two slices of metadatas
	if !reflect.DeepEqual(mds1, mds2) {
		t.Log("mds1:", mds1)
		t.Log("mds2:", mds2)
		t.Fatal("different metadatas")
	}
}

// TestDirectoryMetadatas probes the directory metadata methods of the
// renter.
func TestDirectoryMetadatas(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}

	// Add directories
	var siaPaths []modules.SiaPath
	for i := 0; i < 5; i++ {
		siaPath := modules.RandomSiaPath()
		err = rt.renter.CreateDir(siaPath, modules.DefaultDirPerm)
		if err != nil {
			t.Fatal(err)
		}
		siaPaths = append(siaPaths, siaPath)
	}

	// Get metadatas individually
	var mds1 []bubbledSiaDirMetadata
	for _, siaPath := range siaPaths {
		md, err := rt.renter.managedDirectoryMetadata(siaPath)
		if err != nil {
			t.Fatal(err)
		}
		mds1 = append(mds1, bubbledSiaDirMetadata{
			siaPath,
			md,
		})
	}

	// Get metadatas together
	mds2, err := rt.renter.managedDirectoryMetadatas(siaPaths)
	if err != nil {
		t.Fatal(err)
	}

	// sort by siapath
	sort.Slice(mds1, func(i, j int) bool {
		return strings.Compare(mds1[i].sp.String(), mds1[j].sp.String()) < 0
	})
	sort.Slice(mds2, func(i, j int) bool {
		return strings.Compare(mds2[i].sp.String(), mds2[j].sp.String()) < 0
	})

	// Compare the two slices of metadatas
	if !reflect.DeepEqual(mds1, mds2) {
		t.Log("mds1:", mds1)
		t.Log("mds2:", mds2)
		t.Fatal("different metadatas")
	}
}
