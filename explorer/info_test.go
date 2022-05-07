package explorer_test

import (
	"encoding/binary"
	"math"
	"reflect"
	"testing"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/explorer"
	"go.sia.tech/siad/v2/internal/chainutil"
	"go.sia.tech/siad/v2/internal/explorerutil"
	"go.sia.tech/siad/v2/internal/walletutil"
)

func testingKeypair(seed uint64) (types.PublicKey, types.PrivateKey) {
	var b [32]byte
	binary.LittleEndian.PutUint64(b[:], seed)
	privkey := types.NewPrivateKeyFromSeed(b)
	return privkey.PublicKey(), privkey
}

func TestSiacoinElements(t *testing.T) {
	sim := chainutil.NewChainSim()
	cm := chain.NewManager(chainutil.NewEphemeralStore(sim.Genesis), sim.State)

	explorerStore, err := explorerutil.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	e := explorer.NewExplorer(sim.Genesis.State, explorerStore)
	cm.AddSubscriber(e, cm.Tip())

	w := walletutil.NewTestingWallet(cm.TipState())
	cm.AddSubscriber(w, cm.Tip())

	// fund the wallet with 100 coins
	ourAddr := w.NewAddress()
	fund := types.SiacoinOutput{Value: types.Siacoins(100), Address: ourAddr}
	if err := cm.AddTipBlock(sim.MineBlockWithSiacoinOutputs(fund)); err != nil {
		t.Fatal(err)
	}

	// wallet should now have a transaction, one element, and a non-zero balance

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

		if err := w.FundAndSign(&txn); err != nil {
			t.Fatal(err)
		}

		if err := cm.AddTipBlock(sim.MineBlockWithTxns(txn)); err != nil {
			t.Fatal(err)
		}

		changeAddr := txn.SiacoinOutputs[len(txn.SiacoinOutputs)-1].Address

		balance, err := e.SiacoinBalance(changeAddr)
		if err != nil {
			t.Fatal(err)
		}
		if !w.Balance().Equals(balance) {
			t.Fatal("balances don't equal")
		}

		outputs, err := e.UnspentSiacoinElements(changeAddr)
		if err != nil {
			t.Fatal(err)
		}
		if len(outputs) != 1 {
			t.Fatal("wrong amount of outputs")
		}
		elem, err := e.SiacoinElement(outputs[0])
		if err != nil {
			t.Fatal(err)
		}
		if !w.Balance().Equals(elem.Value) {
			t.Fatal("output value doesn't equal balance")
		}
		txns, err := e.Transactions(changeAddr, math.MaxInt64, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(txns) != 1 {
			t.Fatal("wrong number of transactions")
		}
		if txn.ID() != txns[0] {
			t.Fatal("wrong transaction")
		}
		txns0, err := e.Transaction(txns[0])
		if err != nil {
			t.Fatal(err)
		}
		if txn.ID() != txns0.ID() {
			t.Fatal("wrong transaction")
		}
	}
}

func TestChainStatsSiacoins(t *testing.T) {
	sim := chainutil.NewChainSim()
	cm := chain.NewManager(chainutil.NewEphemeralStore(sim.Genesis), sim.State)

	explorerStore, err := explorerutil.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	e := explorer.NewExplorer(sim.Genesis.State, explorerStore)
	cm.AddSubscriber(e, cm.Tip())

	w := walletutil.NewTestingWallet(cm.TipState())
	cm.AddSubscriber(w, cm.Tip())

	// fund the wallet with 100 coins
	ourAddr := w.NewAddress()
	fund := types.SiacoinOutput{Value: types.Siacoins(100), Address: ourAddr}
	if err := cm.AddTipBlock(sim.MineBlockWithSiacoinOutputs(fund)); err != nil {
		t.Fatal(err)
	}

	// empty block with nothing beside miner reward
	if err := cm.AddTipBlock(sim.MineBlockWithTxns()); err != nil {
		t.Fatal(err)
	}
	stats, err := e.ChainStatsLatest()
	if err != nil {
		t.Fatal(err)
	}
	expected := explorer.ChainStats{
		// don't compare these
		Block: stats.Block,

		SpentSiacoinsCount:  0,
		SpentSiafundsCount:  0,
		ActiveContractCost:  types.ZeroCurrency,
		ActiveContractCount: 0,
		ActiveContractSize:  0,
		TotalContractCost:   types.ZeroCurrency,
		TotalContractSize:   0,
		TotalRevisionVolume: 0,
	}
	if !reflect.DeepEqual(stats, expected) {
		t.Fatal("chainstats don't match")
	}

	for i := 0; i < 5; i++ {
		sendAmount := types.Siacoins(7)
		txn := types.Transaction{
			SiacoinOutputs: []types.SiacoinOutput{{
				Address: types.VoidAddress,
				Value:   sendAmount,
			}},
		}
		if err := w.FundAndSign(&txn); err != nil {
			t.Fatal(err)
		}

		if err := cm.AddTipBlock(sim.MineBlockWithTxns(txn)); err != nil {
			t.Fatal(err)
		}

		stats, err := e.ChainStatsLatest()
		if err != nil {
			t.Fatal(err)
		}
		expected := explorer.ChainStats{
			// don't compare these
			Block: stats.Block,

			SpentSiacoinsCount:  1,
			SpentSiafundsCount:  0,
			ActiveContractCost:  types.ZeroCurrency,
			ActiveContractCount: 0,
			ActiveContractSize:  0,
			TotalContractCost:   types.ZeroCurrency,
			TotalContractSize:   0,
			TotalRevisionVolume: 0,
		}
		if !reflect.DeepEqual(stats, expected) {
			t.Fatal("chainstats don't match")
		}
	}
}

func TestChainStatsContracts(t *testing.T) {
	sim := chainutil.NewChainSim()
	cm := chain.NewManager(chainutil.NewEphemeralStore(sim.Genesis), sim.State)

	explorerStore, err := explorerutil.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	e := explorer.NewExplorer(sim.Genesis.State, explorerStore)
	cm.AddSubscriber(e, cm.Tip())

	w := walletutil.NewTestingWallet(cm.TipState())
	cm.AddSubscriber(w, cm.Tip())

	ourAddr := w.NewAddress()
	renterPubkey, renterPrivkey := testingKeypair(1)
	hostPubkey, hostPrivkey := testingKeypair(2)
	if err := cm.AddTipBlock(sim.MineBlockWithSiacoinOutputs(types.SiacoinOutput{Value: types.Siacoins(100), Address: ourAddr}, types.SiacoinOutput{Value: types.Siacoins(100), Address: types.StandardAddress(renterPubkey)}, types.SiacoinOutput{Value: types.Siacoins(7), Address: types.StandardAddress(hostPubkey)})); err != nil {
		t.Fatal(err)
	}

	renterOutputs, err := e.UnspentSiacoinElements(types.StandardAddress(renterPubkey))
	if err != nil {
		t.Fatal(err)
	}
	renterOutput, err := e.SiacoinElement(renterOutputs[0])
	if err != nil {
		t.Fatal(err)
	}

	hostOutputs, err := e.UnspentSiacoinElements(types.StandardAddress(hostPubkey))
	if err != nil {
		t.Fatal(err)
	}
	hostOutput, err := e.SiacoinElement(hostOutputs[0])
	if err != nil {
		t.Fatal(err)
	}

	// form initial contract
	initialRev := types.FileContract{
		WindowStart: 5,
		WindowEnd:   10,
		RenterOutput: types.SiacoinOutput{
			Address: types.StandardAddress(renterPubkey),
			Value:   types.Siacoins(58),
		},
		HostOutput: types.SiacoinOutput{
			Address: types.StandardAddress(hostPubkey),
			Value:   types.Siacoins(19),
		},
		MissedHostValue: types.Siacoins(17),
		TotalCollateral: types.Siacoins(18),
		RenterPublicKey: renterPubkey,
		HostPublicKey:   hostPubkey,
	}
	outputSum := initialRev.RenterOutput.Value.Add(initialRev.HostOutput.Value).Add(cm.TipState().FileContractTax(initialRev))
	txn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{
			{Parent: renterOutput, SpendPolicy: types.PolicyPublicKey(renterPubkey)},
			{Parent: hostOutput, SpendPolicy: types.PolicyPublicKey(hostPubkey)},
		},
		FileContracts: []types.FileContract{initialRev},
		MinerFee:      renterOutput.Value.Add(hostOutput.Value).Sub(outputSum),
	}
	fc := &txn.FileContracts[0]
	contractHash := cm.TipState().ContractSigHash(*fc)
	fc.RenterSignature = renterPrivkey.SignHash(contractHash)
	fc.HostSignature = hostPrivkey.SignHash(contractHash)
	sigHash := cm.TipState().InputSigHash(txn)
	txn.SiacoinInputs[0].Signatures = []types.Signature{renterPrivkey.SignHash(sigHash)}
	txn.SiacoinInputs[1].Signatures = []types.Signature{hostPrivkey.SignHash(sigHash)}

	if err := cm.AddTipBlock(sim.MineBlockWithTxns(txn)); err != nil {
		t.Fatal(err)
	}
	stats, err := e.ChainStatsLatest()
	if err != nil {
		t.Fatal(err)
	}
	expected := explorer.ChainStats{
		// don't compare these
		Block: stats.Block,

		SpentSiacoinsCount:  2,
		SpentSiafundsCount:  0,
		ActiveContractCost:  types.Siacoins(77),
		ActiveContractCount: 1,
		ActiveContractSize:  0,
		TotalContractCost:   types.Siacoins(77),
		TotalContractSize:   0,
		TotalRevisionVolume: 0,
	}
	if !reflect.DeepEqual(stats, expected) {
		t.Fatal("chainstats don't match")
	}
}
