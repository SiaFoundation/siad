package renter

import (
	"context"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules/renter/proto"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestRenewContract is a unit test for the worker's RenewContract method.
func TestRenewContract(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a disabled worker. That way no background thread will interfere.
	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Get a transaction builder and add the funding.
	funding := types.SiacoinPrecision
	txnBuilder, err := wt.rt.wallet.StartTransaction()
	if err != nil {
		t.Fatal(err)
	}
	err = txnBuilder.FundSiacoins(funding)
	if err != nil {
		t.Fatal(err)
	}

	// Get the host from the hostdb.
	host, _, err := wt.renter.hostDB.Host(wt.staticHostPubKey)
	if err != nil {
		t.Fatal(err)
	}

	// get the wallet seed
	seed, _, err := wt.rt.wallet.PrimarySeed()
	if err != nil {
		t.Fatal(err)
	}

	// Get some more vars.
	allowance := wt.rt.renter.hostContractor.Allowance()
	bh := wt.staticCache().staticBlockHeight
	rs := proto.DeriveRenterSeed(seed)

	// Define some params for the contract.
	params := proto.ContractParams{
		Allowance:     allowance,
		Host:          host,
		Funding:       funding,
		StartHeight:   bh,
		EndHeight:     bh + allowance.Period,
		RefundAddress: types.UnlockHash{},
		RenterSeed:    rs.EphemeralRenterSeed(bh + allowance.Period),
	}

	// Renew the contract.
	err = wt.RenewContract(context.Background(), params, txnBuilder)
	if err != nil {
		t.Fatal(err)
	}

	// TODO: Check if contract actually was renewed.

	// Close the worker.
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}
}
