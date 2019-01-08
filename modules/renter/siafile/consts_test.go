package siafile

import (
	"testing"

	"gitlab.com/NebulousLabs/writeaheadlog"
)

// TestMarshalChunkSize checks marshaledChunkSize against the expected values.
// This guarantees that we can't accidentally change any constants without
// noticing.
func TestMarshalChunkSize(t *testing.T) {
	chunkOverhead := 16 + 2 + 1
	pieceSize := 4 + 4 + 32
	for i := 0; i < 100; i++ {
		if marshaledChunkSize(i) != int64(chunkOverhead+i*pieceSize) {
			t.Fatalf("Expected chunkSize %v but was %v",
				chunkOverhead+i*pieceSize, marshaledChunkSize(i))
		}
	}
}

// TestIsSiaFileUpdate tests the IsSiaFileUpdate method.
func TestIsSiaFileUpdate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	sf := newTestFile()
	insertUpdate := sf.createInsertUpdate(0, []byte{})
	deleteUpdate := sf.createDeleteUpdate()
	randomUpdate := writeaheadlog.Update{}

	if !IsSiaFileUpdate(insertUpdate) {
		t.Error("insertUpdate should be a SiaFileUpdate but wasn't")
	}
	if !IsSiaFileUpdate(deleteUpdate) {
		t.Error("deleteUpdate should be a SiaFileUpdate but wasn't")
	}
	if IsSiaFileUpdate(randomUpdate) {
		t.Error("randomUpdate shouldn't be a SiaFileUpdate but was one")
	}
}
