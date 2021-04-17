package renter

import (
	"testing"

	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestHasSectorJobExpectedBandwidth is a unit test that verifies our HS job
// bandwidth estimates are given in a way we never execute a program and run out
// of budget.
func TestHasSectorJobExpectedBandwidth(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a new worker tester
	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := wt.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
	w := wt.worker
	pt := wt.staticPriceTable().staticPriceTable

	// numPacketsRequiredForSectors is a helper function that executes a HS
	// program with the given amount of sectors and returns the amount of
	// packets needed to cover both the upload and download bandwidth of the
	// program.
	numPacketsRequiredForSectors := func(numSectors int) (uint64, uint64) {
		// build sectors
		sectors := make([]crypto.Hash, numSectors)
		for i := 0; i < numSectors; i++ {
			sectors[i] = crypto.Hash{1, 2, 3}
		}

		// build program
		pb := modules.NewProgramBuilder(&pt, 0)
		for _, sector := range sectors {
			pb.AddHasSectorInstruction(sector)
		}
		p, data := pb.Program()
		cost, _, _ := pb.Cost(true)

		// build job
		jhs := new(jobHasSector)
		jhs.staticSectors = sectors

		// calculate cost
		ulBandwidth, dlBandwidth := jhs.callExpectedBandwidth()
		bandwidthCost := modules.MDMBandwidthCost(pt, ulBandwidth, dlBandwidth)
		cost = cost.Add(bandwidthCost)

		// execute the program
		_, limit, err := w.managedExecuteProgram(p, data, types.FileContractID{}, categoryDownload, cost)
		if err != nil {
			t.Fatal(err)
		}

		return limit.Downloaded() / 1460, limit.Uploaded() / 1460
	}

	// expect 1 root to only require a single packet on both up and download
	dl, ul := numPacketsRequiredForSectors(1)
	if dl != 1 || ul != 1 {
		t.Fatal("unexpected")
	}

	// expect 12 roots to not exceed the threshold (which is at 13) on download
	dl, ul = numPacketsRequiredForSectors(12)
	if dl != 1 || ul != 1 {
		t.Fatal("unexpected")
	}

	// expect 13 roots to push us over the threshold, and require an extra
	// packet on download
	dl, ul = numPacketsRequiredForSectors(13)
	if dl != 2 || ul != 1 {
		t.Fatal("unexpected")
	}

	// expect 16 roots to not exceed the threshold (which is at 17) on upload
	dl, ul = numPacketsRequiredForSectors(16)
	if dl != 2 || ul != 1 {
		t.Fatal("unexpected")
	}

	// expect 17 roots to push us over the threshold, and require an extra
	// packet on upload
	dl, ul = numPacketsRequiredForSectors(17)
	if dl != 2 || ul != 2 {
		t.Fatal("unexpected")
	}
}
