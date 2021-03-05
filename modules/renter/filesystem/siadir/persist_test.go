package siadir

import (
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
)

func TestPersist(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	t.Run("CreateDirMetadataAll", testCreateDirMetadataAll)
}

// testCreateDirMetadataAll probes the case of a potential infinite loop in
// createDirMetadataAll
func testCreateDirMetadataAll(t *testing.T) {
	// Ignoring errors, only checking that the functions return as a regression
	// test for a infinite loop bug.
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(time.Minute):
			t.Error("Test didn't return in time")
		}
	}()
	createDirMetadataAll("path", "", persist.DefaultDiskPermissionsTest, modules.ProdDependencies)
	createDirMetadataAll("path", ".", persist.DefaultDiskPermissionsTest, modules.ProdDependencies)
	createDirMetadataAll("path", "/", persist.DefaultDiskPermissionsTest, modules.ProdDependencies)
	close(done)
}
