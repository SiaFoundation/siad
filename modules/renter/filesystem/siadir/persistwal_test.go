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
