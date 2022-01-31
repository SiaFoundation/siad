package renter

/*import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/host"
	"go.sia.tech/siad/v2/internal/chainutil"
	"go.sia.tech/siad/v2/internal/cpuminer"
	"go.sia.tech/siad/v2/internal/hostutil"
	"go.sia.tech/siad/v2/internal/p2putil"
	"go.sia.tech/siad/v2/internal/walletutil"
	"go.sia.tech/siad/v2/p2p"
	"go.sia.tech/siad/v2/txpool"
	"go.sia.tech/siad/v2/wallet"
	"lukechampine.com/frand"
)

var testSettings = rhp.HostSettings{
	AcceptingContracts: true,
	MaxCollateral:      types.Siacoins(200),
	MaxDuration:        1000,
	SectorSize:         rhp.SectorSize,
	WindowSize:         100,

	ContractFee:            types.Siacoins(1),
	Collateral:             types.Siacoins(2).Div64(rhp.SectorSize), // 2 SC / sector / block
	StoragePrice:           types.Siacoins(1).Div64(rhp.SectorSize), // 1 SC / sector / block
	DownloadBandwidthPrice: types.Siacoins(1).Div64(rhp.SectorSize), // 1 SC / sector
	UploadBandwidthPrice:   types.Siacoins(1).Div64(rhp.SectorSize), // 1 SC / sector

	RPCAccountBalanceCost: types.NewCurrency64(1), // 1 H / op
	RPCFundAccountCost:    types.NewCurrency64(1), // 1 H / op
	RPCHostSettingsCost:   types.NewCurrency64(1), // 1 H / op
	RPCLatestRevisionCost: types.NewCurrency64(1), // 1 H / op
	RPCRenewContractCost:  types.NewCurrency64(1), // 1 H / op
}

type testNode struct {
	mining int32
	c      *chain.Manager
	cs     chain.ManagerStore
	tp     *txpool.Pool
	s      *p2p.Syncer
	h      *host.Host
	w      *wallet.HotWallet
	m      *cpuminer.CPUMiner
}

func (tn *testNode) run() {
	go tn.mine()
	if err := tn.s.Run(); err != nil {
		panic(err)
	}
}

func (tn *testNode) Close() error {
	// signal miner to stop, and wait for it to exit
	atomic.StoreInt32(&tn.mining, 2)
	for atomic.LoadInt32(&tn.mining) != 3 {
		time.Sleep(100 * time.Millisecond)
	}
	return tn.s.Close()
}

func (tn *testNode) startMining() bool { return atomic.CompareAndSwapInt32(&tn.mining, 0, 1) }
func (tn *testNode) stopMining() bool  { return atomic.CompareAndSwapInt32(&tn.mining, 1, 0) }

func (tn *testNode) mineBlock() error {
again:
	b := tn.m.MineBlock()
	err := tn.c.AddTipBlock(b)
	if errors.Is(err, chain.ErrUnknownIndex) {
		goto again
	} else if err != nil {
		return err
	}
	tn.s.BroadcastBlock(b)
	time.Sleep(10 * time.Millisecond)
	return nil
}

func (tn *testNode) mine() {
	for {
		// wait for permission
		for atomic.LoadInt32(&tn.mining) == 0 {
			time.Sleep(100 * time.Millisecond)
		}
		if atomic.CompareAndSwapInt32(&tn.mining, 2, 3) {
			return // shutdown
		}

		tn.mineBlock()
	}
}

func newTestNode(tb testing.TB, genesisID types.BlockID, c consensus.Checkpoint) *testNode {
	cs := chainutil.NewEphemeralStore(c)
	cm := chain.NewManager(cs, c.Context)
	tp := txpool.New(c.Context)
	cm.AddSubscriber(tp, cm.Tip())
	ws := walletutil.NewEphemeralStore()
	w := wallet.NewHotWallet(ws, wallet.NewSeed())
	cm.AddSubscriber(ws, cm.Tip())
	m := cpuminer.New(c.Context, w.NextAddress(), tp)
	cm.AddSubscriber(m, cm.Tip())
	s, err := p2p.NewSyncer(":0", genesisID, cm, tp, p2putil.NewEphemeralStore())
	if err != nil {
		tb.Fatal(err)
	}

	contractStore := hostutil.NewEphemeralContractStore()
	cm.AddSubscriber(contractStore, cm.Tip())
	sectorStore := hostutil.NewEphemeralSectorStore()
	registryStore := hostutil.NewEphemeralRegistryStore(100)
	accountStore := hostutil.NewMemAccountStore()
	h, err := host.New(":0", types.NewPrivateKeyFromSeed(frand.Entropy256()), testSettings, cm, sectorStore, contractStore, registryStore, accountStore, w, tp, hostutil.NewStdOutLogger())
	if err != nil {
		tb.Fatal(err)
	}

	return &testNode{
		c:  cm,
		cs: cs,
		tp: tp,
		s:  s,
		w:  w,
		m:  m,
		h:  h,
	}
}

// testNodes creates a set of connected nodes, mines until all of them have a
// balance, then returns the set of nodes.
func testNodes(tb testing.TB, n int) []*testNode {
	genesisBlock := types.Block{
		Header: types.BlockHeader{
			Timestamp: time.Unix(734600000, 0),
		},
	}
	sau := consensus.GenesisUpdate(genesisBlock, types.Work{NumHashes: [32]byte{31: 1 << 1}})
	genesis := consensus.Checkpoint{
		Block:   genesisBlock,
		Context: sau.Context,
	}

	if n == 0 {
		tb.Fatal("must create at  least 1 node")
	}

	var wg sync.WaitGroup
	wg.Add(n)
	nodes := make([]*testNode, n)
	for i := range nodes {
		n := newTestNode(tb, genesis.Block.ID(), genesis)
		tb.Cleanup(func() { n.Close() })
		go n.run()
		if i != 0 {
			n.s.Connect(nodes[0].s.Addr())
		}
		n.startMining()
		go func() {
			for n.w.Balance().IsZero() {
				time.Sleep(time.Millisecond * 5)
			}
			n.stopMining()
			wg.Done()
		}()
		nodes[i] = n
	}

	wg.Wait()
	return nodes
}

func TestFormContract(t *testing.T) {
	nodes := testNodes(t, 2)
	renterNode := nodes[0]
	hostNode := nodes[1]

	hostSettings := hostNode.h.Settings()
	hostPub := hostNode.h.PublicKey()

	session, err := NewSession(hostSettings.NetAddress, hostPub, renterNode.w, renterNode.tp, renterNode.c)
	if err != nil {
		t.Fatal(err)
	}

	context := renterNode.c.Tip()

	// form a contract
	renterPriv := types.NewPrivateKeyFromSeed(frand.Entropy256())
	contract, _, err := session.FormContract(renterPriv, types.Siacoins(20), types.Siacoins(10), context.Height+200)
	if err != nil {
		t.Fatal(err)
	}

	switch {
	case contract.Revision.ValidHostOutput.Value.Cmp(types.Siacoins(20).Add(hostSettings.ContractFee)) != 0:
		t.Error("wrong host output value")
	case contract.Revision.ValidHostOutput.Address != hostSettings.Address:
		t.Error("wrong host output address")
	case contract.Revision.ValidRenterOutput.Value.Cmp(types.Siacoins(10)) != 0:
		t.Error("wrong renter output value")
	}

	// mine the block with the contract in it
	if err := hostNode.mineBlock(); err != nil {
		t.Fatal(err)
	}

	block, err := hostNode.c.Block(hostNode.c.Tip())
	if err != nil {
		t.Fatal(err)
	}

	if len(block.Transactions[0].FileContracts) != 1 {
		t.Fatal("expected 1 file contract")
	}

	// mine until the contract expires
	hostNode.startMining()
	for hostNode.c.Tip().Height < contract.Revision.WindowEnd+5 {
		time.Sleep(time.Millisecond * 5)
	}

	// TODO: verify that the contract was finalized
}

func TestAccountFunding(t *testing.T) {
	nodes := testNodes(t, 2)
	renterNode := nodes[0]
	hostNode := nodes[1]

	hostSettings := hostNode.h.Settings()
	hostPub := hostNode.h.PublicKey()

	session, err := NewSession(hostSettings.NetAddress, hostPub, renterNode.w, renterNode.tp, renterNode.c)
	if err != nil {
		t.Fatal(err)
	}

	context := renterNode.c.Tip()

	// form a contract
	renterPriv := types.NewPrivateKeyFromSeed(frand.Entropy256())
	contract, _, err := session.FormContract(renterPriv, types.Siacoins(20), types.Siacoins(10), context.Height+200)
	if err != nil {
		t.Fatal(err)
	}

	expectedHostOutput := types.Siacoins(21).Add(types.NewCurrency64(1))
	expectedRenterOutput := types.Siacoins(10).Sub(types.NewCurrency64(1))

	// register a price table on the host
	payment := session.PayByContract(&contract, renterPriv, renterPriv.PublicKey())
	if _, err := session.RegisterSettings(payment); err != nil {
		t.Fatal(err)
	} else if contract.Revision.ValidHostOutput.Value != expectedHostOutput {
		t.Fatalf("expected %d H as payment for registering settings, got %d H", expectedHostOutput, contract.Revision.ValidHostOutput.Value)
	} else if contract.Revision.ValidRenterOutput.Value != expectedRenterOutput {
		t.Fatalf("expected %d H as payment for registering settings, got %d H", expectedRenterOutput, contract.Revision.ValidRenterOutput.Value)
	}

	// add an additional hasting for the account balance RPC
	expectedHostOutput = expectedHostOutput.Add(types.NewCurrency64(1))
	expectedRenterOutput = expectedRenterOutput.Sub(types.NewCurrency64(1))
	if balance, err := session.AccountBalance(renterPriv.PublicKey(), payment); err != nil {
		t.Fatal(err)
	} else if !balance.IsZero() {
		t.Fatal("expected empty account")
	} else if contract.Revision.ValidHostOutput.Value != expectedHostOutput {
		t.Fatalf("expected %d H as payment for registering settings, got %d H", expectedHostOutput, contract.Revision.ValidHostOutput.Value)
	} else if contract.Revision.ValidRenterOutput.Value != expectedRenterOutput {
		t.Fatalf("expected %d H as payment for registering settings, got %d H", expectedRenterOutput, contract.Revision.ValidRenterOutput.Value)
	}

	// add an additional hasting and 1 SC for the account funding RPC
	expectedHostOutput = expectedHostOutput.Add(types.NewCurrency64(1)).Add(types.Siacoins(1))
	expectedRenterOutput = expectedRenterOutput.Sub(types.NewCurrency64(1)).Sub(types.Siacoins(1))
	if balance, err := session.FundAccount(renterPriv.PublicKey(), types.Siacoins(1), payment); err != nil {
		t.Fatal(err)
	} else if balance != types.Siacoins(1) {
		t.Fatal("expected 1 SC balance")
	} else if contract.Revision.ValidHostOutput.Value != expectedHostOutput {
		t.Fatalf("expected %d H as payment for registering settings, got %d H", expectedHostOutput, contract.Revision.ValidHostOutput.Value)
	} else if contract.Revision.ValidRenterOutput.Value != expectedRenterOutput {
		t.Fatalf("expected %d H as payment for registering settings, got %d H", expectedRenterOutput, contract.Revision.ValidRenterOutput.Value)
	}

	// add an additional hasting for the account balance RPC
	expectedHostOutput = expectedHostOutput.Add(types.NewCurrency64(1))
	expectedRenterOutput = expectedRenterOutput.Sub(types.NewCurrency64(1))
	if balance, err := session.AccountBalance(renterPriv.PublicKey(), payment); err != nil {
		t.Fatal(err)
	} else if balance != types.Siacoins(1) {
		t.Fatal("expected 1 SC of account funding")
	} else if contract.Revision.ValidHostOutput.Value != expectedHostOutput {
		t.Fatalf("expected %d H as payment for registering settings, got %d H", expectedHostOutput, contract.Revision.ValidHostOutput.Value)
	} else if contract.Revision.ValidRenterOutput.Value != expectedRenterOutput {
		t.Fatalf("expected %d H as payment for registering settings, got %d H", expectedRenterOutput, contract.Revision.ValidRenterOutput.Value)
	}
}*/
