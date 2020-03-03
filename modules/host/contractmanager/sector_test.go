package contractmanager

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// TestHasSector verifies the behavior of the HasSector fucnction.
func TestHasSector(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	cmt, err := newContractManagerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer cmt.panicClose()

	// Add a storage folder
	storageFolderDir := filepath.Join(cmt.persistDir, "storageFolderOne")
	err = os.MkdirAll(storageFolderDir, 0700)
	if err != nil {
		t.Fatal(err)
	}
	err = cmt.cm.AddStorageFolder(storageFolderDir, modules.SectorSize*64)
	if err != nil {
		t.Fatal(err)
	}

	// Fabricate a sector
	root, data := randSector()
	exists := cmt.cm.HasSector(root)
	if exists {
		t.Fatal(fmt.Sprintf("Unexpected HasSector response: %v, sector has not been added yet", exists))
	}

	// Add it to the contract manager.
	err = cmt.cm.AddSector(root, data)
	if err != nil {
		t.Fatal(err)
	}

	exists = cmt.cm.HasSector(root)
	if !exists {
		t.Fatal(fmt.Sprintf("Unexpected HasSector response: %v, sector has been added", exists))
	}
}
