package renter

import (
	"fmt"
	"testing"

	"gitlab.com/NebulousLabs/Sia/siatest"
)

// TestAccounts tests the renter's ephemeral accounts on the host.
func TestAccounts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:   2,
		Miners:  1,
		Renters: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Confirm contract end heights were set properly
	r := tg.Renters()[0]
	rc, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(len(rc.ActiveContracts))

	// Post default allowance - this should trigger workers
	r.RenterPostAllowance(siatest.DefaultAllowance)

	// TODO
}
