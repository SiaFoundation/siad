package siadir

import (
	"testing"

	"gitlab.com/NebulousLabs/writeaheadlog"
)

// TestIsSiaDirUpdate tests the IsSiaDirUpdate method.
func TestIsSiaDirUpdate(t *testing.T) {
	sd, err := newTestDir()
	if err != nil {
		t.Fatal(err)
	}
	metadataUpdate := createMetadataUpdate([]byte{})
	deleteUpdate := sd.createDeleteUpdate()
	randomUpdate := writeaheadlog.Update{}

	if !IsSiaDirUpdate(metadataUpdate) {
		t.Error("metadataUpdate should be a SiaDirUpdate but wasn't")
	}
	if !IsSiaDirUpdate(deleteUpdate) {
		t.Error("deleteUpdate should be a SiaDirUpdate but wasn't")
	}
	if IsSiaDirUpdate(randomUpdate) {
		t.Error("randomUpdate shouldn't be a SiaDirUpdate but was one")
	}
}
