package siafile

import (
	"bytes"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/errors"
)

// TestSnapshot tests if a snapshot is created correctly from a SiaFile.
func TestSnapshot(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a random file for testing and create a snapshot from it.
	sf := newTestFile()
	snap := sf.Snapshot()

	// Make sure the snapshot has the same fields as the SiaFile.
	if len(sf.staticChunks) != len(snap.staticChunks) {
		t.Errorf("expected %v chunks but got %v", len(sf.staticChunks), len(snap.staticChunks))
	}
	if sf.staticMetadata.StaticFileSize != snap.staticFileSize {
		t.Errorf("staticFileSize was %v but should be %v",
			snap.staticFileSize, sf.staticMetadata.StaticFileSize)
	}
	if sf.staticMetadata.StaticPieceSize != snap.staticPieceSize {
		t.Errorf("staticPieceSize was %v but should be %v",
			snap.staticPieceSize, sf.staticMetadata.StaticPieceSize)
	}
	if sf.staticMetadata.staticErasureCode.MinPieces() != snap.staticErasureCode.MinPieces() {
		t.Errorf("minPieces was %v but should be %v",
			sf.staticMetadata.staticErasureCode.MinPieces(), snap.staticErasureCode.MinPieces())
	}
	if sf.staticMetadata.staticErasureCode.NumPieces() != snap.staticErasureCode.NumPieces() {
		t.Errorf("numPieces was %v but should be %v",
			sf.staticMetadata.staticErasureCode.NumPieces(), snap.staticErasureCode.NumPieces())
	}
	if !bytes.Equal(sf.staticMetadata.StaticMasterKey, snap.staticMasterKey.Key()) {
		t.Error("masterkeys don't match")
	}
	if sf.staticMetadata.StaticMasterKeyType != snap.staticMasterKey.Type() {
		t.Error("masterkey types don't match")
	}
	if sf.staticMetadata.Mode != snap.staticMode {
		t.Error("modes don't match")
	}
	if !reflect.DeepEqual(sf.pubKeyTable, snap.staticPubKeyTable) {
		t.Error("pubkeytables don't match")
	}
	if sf.staticMetadata.SiaPath != snap.staticSiaPath {
		t.Error("siapaths don't match")
	}
	// Compare the pieces.
	for i := range sf.staticChunks {
		sfPieces, err1 := sf.Pieces(uint64(i))
		snapPieces, err2 := snap.Pieces(uint64(i))
		if err := errors.Compose(err1, err2); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(sfPieces, snapPieces) {
			t.Error("Pieces don't match")
		}
	}
}
