package siadir

import (
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// TestReadAndApplyMetadataUpdateMissingDir probes the edge case of a directory
// not being created on disk due to a crash.
func TestReadAndApplyMetadataUpdateMissingDir(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a root dir for testing.
	root, err := newTestDir(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	rootPath := root.path

	// Create a metadata update for a path that doesn't exist.
	path := filepath.Join(rootPath, "a", "b", "c")
	update, err := createMetadataUpdate(path, Metadata{})
	if err != nil {
		t.Fatal(err)
	}

	// Apply it.
	err = readAndApplyMetadataUpdate(modules.ProdDependencies, update)
	if err != nil {
		t.Fatal(err)
	}
}

// TestDeleteUpdateRegression is a regression test that ensure apply updates
// won't panic when called with a set of updates with the last one being
// a delete update.
func TestDeleteUpdateRegression(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaDir
	sd, err := newTestDir(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Apply updates with the last update as a delete update. This use to trigger
	// a panic. No need to check the return value as we are only concerned with
	// the panic.
	update := sd.createDeleteUpdate()
	sd.createAndApplyTransaction(update, update)
}
