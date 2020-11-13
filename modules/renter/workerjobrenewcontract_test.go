package renter

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/proto"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestRenewContract is a unit test for the worker's RenewContract method.
func TestRenewContract(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a worker.
	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Close the worker.
	defer func() {
		if err := wt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

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

	// Get the wallet seed
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

	// Get the old contract and revision.
	oldContract, ok := wt.renter.hostContractor.ContractByPublicKey(wt.staticHostPubKey)
	if !ok {
		t.Fatal("oldContract not found")
	}
	oldRevision := oldContract.Transaction.FileContractRevisions[0]

	// Renew the contract.
	err = wt.RenewContract(context.Background(), params, txnBuilder)
	if err != nil {
		t.Fatal(err)
	}

	// Mine a block to mine the contract and trigger maintenance.
	b, err := wt.rt.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Check if the right txn was mined. It should contain both a revision and
	// contract.
	found := false
	var fcr types.FileContractRevision
	for _, txn := range b.Transactions {
		if len(txn.FileContractRevisions) == 1 && len(txn.FileContracts) == 1 {
			fcr = txn.FileContractRevisions[0]
			found = true
			break
		}
	}
	if !found {
		t.Fatal("txn containing both the final revision and contract wasn't mined")
	}

	// Compute the expected base costs.
	pt := wt.staticPriceTable().staticPriceTable
	params.PriceTable = &pt
	basePrice, baseCollateral := modules.RenewBaseCosts(oldRevision, params.PriceTable, params.EndHeight)
	fmt.Println("base", basePrice, baseCollateral)
	fmt.Println("diff", oldRevision.ValidRenterPayout().Sub(fcr.ValidRenterPayout()))
	fmt.Println("diff", fcr.ValidHostPayout().Sub(oldRevision.ValidHostPayout()))

	// Check final revision.
	if fcr.ParentID != oldContract.ID {
		t.Fatalf("expected fcr to have parent %v but was %v", oldContract.ID, fcr.ParentID)
	}
	if !reflect.DeepEqual(fcr.NewMissedProofOutputs, fcr.NewValidProofOutputs) {
		t.Fatal("expected valid outputs to match missed ones")
	}
	if fcr.NewFileSize != 0 {
		t.Fatal("size should be 0", fcr.NewFileSize)
	}
	if fcr.NewFileMerkleRoot != (crypto.Hash{}) {
		t.Fatal("size should be 0", fcr.NewFileSize)
	}
}
