package proto

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestRefCounter tests the RefCounter type
func TestRefCounter(t *testing.T) {
	// if testing.Short() {
	// 	t.SkipNow()
	// }

	mockContractID := types.FileContractID(crypto.HashBytes([]byte("contractId")))
	mockSectorsCount := int64(17)
	testDir := build.TempDir(t.Name())
	if err := os.MkdirAll(testDir, 0700); err != nil {
		t.Fatal("failed to create test directory")
	}
	rcFilePath := filepath.Join(testDir, mockContractID.String()+refCounterExtension)
	// create a ref counter
	rc, err := NewRefCounter(rcFilePath, mockSectorsCount)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(" >> header ", rc.RefCounterHeader)
	t.Log(" >> counts ", rc.sectorCounts)

	// TODO: test all the cases:
	// increment, decrement,
	// increment beyond uint16 max value (!!!),
	// decrement to zero (must mark it as garbage)
	// make sure the file is getting truncated
	// make sure we have the right number of counts after truncation (nothing was trucated away that we still needed)

}
