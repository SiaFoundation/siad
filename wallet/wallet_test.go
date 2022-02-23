package wallet_test

import (
	"testing"
	"time"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/types"

	"go.sia.tech/siad/v2/internal/chainutil"
	"go.sia.tech/siad/v2/internal/walletutil"
	"go.sia.tech/siad/v2/wallet"
)

func TestWallet(t *testing.T) {
	sim := chainutil.NewChainSim()

	cm := chain.NewManager(chainutil.NewEphemeralStore(sim.Genesis), sim.Context)
	store := walletutil.NewEphemeralStore()
	cm.AddSubscriber(store, cm.Tip())
	w := wallet.NewHotWallet(store, wallet.NewSeed())

	// fund the wallet with 100 coins
	ourAddr := w.NextAddress()
	fund := types.SiacoinOutput{Value: types.Siacoins(100), Address: ourAddr}
	if err := cm.AddTipBlock(sim.MineBlockWithSiacoinOutputs(fund)); err != nil {
		t.Fatal(err)
	}

	// wallet should now have a transaction, one element, and a non-zero balance
	if len(store.Transactions(time.Time{}, -1)) != 1 {
		t.Fatal("expected a single transaction, got", store.Transactions(time.Time{}, -1))
	} else if len(store.SpendableSiacoinElements()) != 1 {
		t.Fatal("expected a single spendable element, got", store.SpendableSiacoinElements())
	} else if w.BalanceSiacoin().IsZero() {
		t.Fatal("expected non-zero balance after mining")
	}

	// mine 5 blocks, each containing a transaction that sends some coins to
	// the void and some to ourself
	for i := 0; i < 5; i++ {
		sendAmount := types.Siacoins(7)
		txn := types.Transaction{
			SiacoinOutputs: []types.SiacoinOutput{{
				Address: types.VoidAddress,
				Value:   sendAmount,
			}},
		}
		if toSign, _, err := w.FundTransaction(&txn, sendAmount, nil); err != nil {
			t.Fatal(err)
		} else if err := w.SignTransaction(sim.Context, &txn, toSign); err != nil {
			t.Fatal(err)
		}
		prevBalance := w.BalanceSiacoin()

		if err := cm.AddTipBlock(sim.MineBlockWithTxns(txn)); err != nil {
			t.Fatal(err)
		}

		if !prevBalance.Sub(w.BalanceSiacoin()).Equals(sendAmount) {
			t.Fatal("after send, balance should have decreased accordingly")
		}
	}
}
