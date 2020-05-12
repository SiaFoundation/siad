package renter

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem/siafile"
)

// TestWritePiecesPanic is a regression test that ensures WritePieces does not
// panic due to unlocking an unlocked mutex.
func TestWritePiecesPanic(t *testing.T) {
	// Create the minimum inputs
	ddw := &downloadDestinationWriter{
		closed:   false,
		progress: 50,
	}

	// Test case of offset being less then the progress. Ignore the error since
	// we are only concerned with the mutex panic.
	rsc, _ := siafile.NewRSCode(1, 1)
	ddw.WritePieces(rsc, [][]byte{}, 0, 0, 0)
}
