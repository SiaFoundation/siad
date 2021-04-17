package siadir

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/persist"
)

// TestPersist probes the persist subsystem
func TestPersist(t *testing.T) {
	t.Run("NewMetadata", testNewMetadata)

	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	t.Run("CallLoadSiaDirMetadata", testCallLoadSiaDirMetadata)
	t.Run("CreateDirMetadataAll", testCreateDirMetadataAll)
}

// testCallLoadSiaDirMetadata probes the callLoadSiaDirMetadata function
func testCallLoadSiaDirMetadata(t *testing.T) {
	testDir, err := newSiaDirTestDir(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Calling load on an empty path should return IsNotExist
	_, err = callLoadSiaDirMetadata("doesntexist", modules.ProdDependencies)
	if !errors.IsOSNotExist(err) {
		t.Error("unexpected error", err)
	}

	// An empty file, should return corrupt
	emptyFile := filepath.Join(testDir, "emptyFile")
	f, err := os.Create(emptyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = callLoadSiaDirMetadata(emptyFile, modules.ProdDependencies)
	if !errors.Contains(err, ErrCorruptFile) {
		t.Error("unexpected error", err)
	}

	// A file with corrupt data should return invalid checksum
	corruptFile := filepath.Join(testDir, "corruptFile")
	f, err = os.Create(corruptFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write(fastrand.Bytes(100))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = callLoadSiaDirMetadata(corruptFile, modules.ProdDependencies)
	if !errors.Contains(err, ErrInvalidChecksum) {
		t.Error("unexpected error", err)
	}

	// Test happy case
	happyFile := filepath.Join(testDir, modules.SiaDirExtension)
	f, err = os.Create(happyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	err = saveDir(testDir, newMetadata(), modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	_, err = callLoadSiaDirMetadata(happyFile, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
}

// testCreateDirMetadataAll probes the case of a potential infinite loop in
// createDirMetadataAll
func testCreateDirMetadataAll(t *testing.T) {
	testDir, err := newSiaDirTestDir(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Create done chan to protect against blocking test
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(time.Minute):
			t.Error("Test didn't return in time")
		}
	}()

	// Ignoring errors, only checking that the functions return as a regression
	// test for a infinite loop bug.
	createDirMetadataAll(testDir, "", persist.DefaultDiskPermissionsTest, modules.ProdDependencies)
	createDirMetadataAll(testDir, ".", persist.DefaultDiskPermissionsTest, modules.ProdDependencies)
	createDirMetadataAll(testDir, "/", persist.DefaultDiskPermissionsTest, modules.ProdDependencies)
	close(done)

	// Test cleanup. This is needed because the above test cases create a .siadir
	// in the current directory.
	err = os.Remove(modules.SiaDirExtension)
	if err != nil {
		t.Fatal(err)
	}
}

// testNewMetadata probes the newMetadata function
func testNewMetadata(t *testing.T) {
	md := Metadata{
		AggregateHealth:        DefaultDirHealth,
		AggregateMinRedundancy: DefaultDirRedundancy,
		AggregateRemoteHealth:  DefaultDirHealth,
		AggregateStuckHealth:   DefaultDirHealth,

		Health:        DefaultDirHealth,
		MinRedundancy: DefaultDirRedundancy,
		Mode:          modules.DefaultDirPerm,
		RemoteHealth:  DefaultDirHealth,
		StuckHealth:   DefaultDirHealth,
		Version:       metadataVersion,
	}
	mdNew := newMetadata()

	// Sync the time fields
	md.AggregateModTime = mdNew.AggregateModTime
	md.ModTime = mdNew.ModTime

	// Metadatas should match
	if !reflect.DeepEqual(md, mdNew) {
		t.Log("expected:", md)
		t.Log("actual:", mdNew)
		t.Fatal("metadata mismtach")
	}
}
