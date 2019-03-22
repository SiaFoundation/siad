package wallet

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules/wallet"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestWalletTransactionsSumUpToWalletBalance tests that even with file
// contracts, the transactions returned by the wallet sum up to the wallet's
// balance.
func TestWalletTransactionsSumUpToWalletBalance(t *testing.T) {
	if !build.VLONG {
		t.SkipNow()
	}
	// Create a group for the test.
	groupParams := siatest.GroupParams{
		Hosts:   1,
		Renters: 1,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(walletTestDir(t.Name()), groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Get the renter for the test.
	renter := tg.Renters()[0]
	// Get the renter's confirmed transactions and blockheight.
	confirmedTxns, err := renter.ConfirmedTransactions()
	if err != nil {
		t.Fatal(err)
	}
	bh, err := renter.BlockHeight()
	if err != nil {
		t.Fatal(err)
	}
	// Convert the transactions.
	txns, err := wallet.ComputeValuedTransactions(confirmedTxns, bh)
	if err != nil {
		t.Fatal(err)
	}

	// These transactions should contain one contract for each host and sum up
	// to the renter's wallet's balance.
	var numFC int
	var totalBalance types.Currency
	for _, txn := range txns {
		numFC += len(txn.Transaction.FileContracts)
		totalBalance = totalBalance.Add(txn.ConfirmedIncomingValue)
		totalBalance = totalBalance.Sub(txn.ConfirmedOutgoingValue)
	}
	if numFC != len(tg.Hosts()) {
		t.Fatalf("Expected %v contracts but got %v", numFC, len(tg.Hosts()))
	}

	// Get the renter's wallet's balance and compare it to the sum of the
	// confirmed transactions.
	balance, err := renter.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}
	if totalBalance.Cmp(balance) > 0 {
		t.Fatalf("Expected the summed up balance to be %v but was %v: Diff +%v",
			balance.HumanString(), totalBalance.HumanString(), totalBalance.Sub(balance).HumanString())
	} else if totalBalance.Cmp(balance) < 0 {
		t.Fatalf("Expected the summed up balance to be %v but was %v: Diff -%v",
			balance.HumanString(), totalBalance.HumanString(), balance.Sub(totalBalance).HumanString())
	}

	// Figure out the endheight of the contracts.
	rcs, err := renter.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	endHeight := rcs.ActiveContracts[0].EndHeight
	// Mine enough blocks for all contracts to be renewed.
	m := tg.Miners()[0]
	cg, err := renter.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}
	for i := types.BlockHeight(0); i < endHeight+types.MaturityDelay-cg.Height; i++ {
		if err := m.MineBlock(); err != nil {
			t.Fatal(err)
		}
	}

	// Check that the transactions still add up to the wallet's balance.
	confirmedTxns, err = renter.ConfirmedTransactions()
	if err != nil {
		t.Fatal(err)
	}
	bh, err = renter.BlockHeight()
	if err != nil {
		t.Fatal(err)
	}
	// Convert the transactions.
	txns, err = wallet.ComputeValuedTransactions(confirmedTxns, bh)
	if err != nil {
		t.Fatal(err)
	}
	// These transactions should contain two contracts for each host and sum up
	// to the renter's wallet's balance.
	numFC = 0
	totalBalance = types.ZeroCurrency
	for _, txn := range txns {
		numFC += len(txn.Transaction.FileContracts)
		totalBalance = totalBalance.Add(txn.ConfirmedIncomingValue)
		totalBalance = totalBalance.Sub(txn.ConfirmedOutgoingValue)
	}
	if numFC != 2*len(tg.Hosts()) {
		t.Fatalf("Expected %v contracts but got %v", numFC, len(tg.Hosts()))
	}

	// Get the renter's wallet's balance and compare it to the sum of the
	// confirmed transactions.
	balance, err = renter.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}
	if totalBalance.Cmp(balance) > 0 {
		t.Fatalf("Expected the summed up balance to be %v but was %v: Diff +%v",
			balance.HumanString(), totalBalance.HumanString(), totalBalance.Sub(balance).HumanString())
	} else if totalBalance.Cmp(balance) < 0 {
		t.Fatalf("Expected the summed up balance to be %v but was %v: Diff -%v",
			balance.HumanString(), totalBalance.HumanString(), balance.Sub(totalBalance).HumanString())
	}
}
