package renter

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/siatest/dependencies"
)

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

	// calculate metadatas individually
	var mds1 []bubbledSiaFileMetadata
	for _, siaPath := range siaPaths {
		md, err := rt.renter.managedCachedFileMetadata(siaPath)
		if err != nil {
			t.Fatal(err)
		}
		mds1 = append(mds1, md)
	}

	// calculate metadatas together
	mds2, err := rt.renter.managedCachedFileMetadatas(siaPaths)
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
