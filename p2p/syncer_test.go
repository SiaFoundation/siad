package p2p_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/internal/chainutil"
	"go.sia.tech/siad/v2/internal/cpuminer"
	"go.sia.tech/siad/v2/internal/p2putil"
	"go.sia.tech/siad/v2/internal/walletutil"
	"go.sia.tech/siad/v2/p2p"
	"go.sia.tech/siad/v2/txpool"
	"go.sia.tech/siad/v2/wallet"
	"lukechampine.com/frand"
)

type testNode struct {
	c  *chain.Manager
	cs chain.ManagerStore
	tp *txpool.Pool
	s  *p2p.Syncer
	w  *wallet.HotWallet
	m  *cpuminer.CPUMiner
}

func (tn *testNode) run() {
	if err := tn.s.Run(); err != nil {
		panic(err)
	}
}

func (tn *testNode) Close() error {
	return tn.s.Close()
}

func (tn *testNode) send(amount types.Currency, dest types.Address) error {
	txn := types.Transaction{
		SiacoinOutputs: []types.SiacoinOutput{{Value: amount, Address: dest}},
	}
	toSign, discard, err := tn.w.FundTransaction(&txn, amount, tn.tp.Transactions())
	if err != nil {
		return err
	}
	defer discard()
	if err := tn.w.SignTransaction(consensus.ValidationContext{}, &txn, toSign); err != nil {
		return err
	}
	// give message to ourselves and to peers
	if err := tn.tp.AddTransaction(txn.DeepCopy()); err != nil {
		return err
	}
	tn.s.BroadcastTransaction(txn, nil)
	return nil
}

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
	return &testNode{
		c:  cm,
		cs: cs,
		tp: tp,
		s:  s,
		w:  w,
		m:  m,
	}
}

func TestNetwork(t *testing.T) {
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

	// create two nodes and start mining on both
	n1 := newTestNode(t, genesisBlock.ID(), genesis)
	defer n1.Close()
	go n1.run()
	n2 := newTestNode(t, genesisBlock.ID(), genesis)
	defer n2.Close()
	go n2.run()

	mineOnRandomNode := func() {
		if frand.Intn(2) == 0 {
			n1.mineBlock()
		} else {
			n2.mineBlock()
		}
	}

	// connect the nodes after a few blocks have been mined
	for n1.c.Tip().Height < 10 || n2.c.Tip().Height < 10 {
		mineOnRandomNode()
	}
	if err := n1.s.Connect(n2.s.Addr()); err != nil {
		t.Fatal(err)
	}

	// continue mining until both nodes have a balance
	for n1.w.Balance().IsZero() || n2.w.Balance().IsZero() {
		mineOnRandomNode()
	}

	// simulate some chain activity by mining and broadcasting simple txns,
	// stopping after both nodes have sent 10 txns
	n1addr := n1.w.NextAddress()
	n2addr := n2.w.NextAddress()
	for s1, s2 := 0, 0; s1 < 10 || s2 < 10; {
		if frand.Intn(2) == 0 {
			if n1.send(types.Siacoins(7), n2addr) == nil {
				s1++
			}
		} else {
			if n2.send(types.Siacoins(9), n1addr) == nil {
				s2++
			}
			mineOnRandomNode()
		}
	}
	mineOnRandomNode()

	// since we are mining with low difficulty, the chains may have an identical
	// amount of work; if so, mine a little more on one chain
	vc1 := n1.c.TipContext()
	vc2 := n2.c.TipContext()
	if vc1.TotalWork.Cmp(vc2.TotalWork) == 0 {
		n1.mineBlock()
	}

	// nodes should synchronize within 5 seconds
	var synced bool
	for start := time.Now(); !synced && time.Since(start) < 5*time.Second; {
		time.Sleep(5 * time.Millisecond)
		synced = n1.c.Tip() == n2.c.Tip()
	}
	if !synced {
		t.Fatal("nodes not synchronized:", "\n", n1.s.Addr(), n1.c.Tip(), "\n", n2.s.Addr(), n2.c.Tip())
	}
}

func TestCheckpoint(t *testing.T) {
	genesisBlock := types.Block{
		Header: types.BlockHeader{
			Timestamp: time.Unix(734600000, 0),
		},
	}
	sau := consensus.GenesisUpdate(genesisBlock, types.Work{NumHashes: [32]byte{30: 1 << 2}})
	genesis := consensus.Checkpoint{
		Block:   genesisBlock,
		Context: sau.Context,
	}

	// create a node and mine some blocks
	n1 := newTestNode(t, genesisBlock.ID(), genesis)
	defer n1.Close()
	go n1.run()
	for i := 0; i < 10; i++ {
		if err := n1.mineBlock(); err != nil {
			t.Fatal(err)
		}
	}

	// download a checkpoint and use it to initialize a new node
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	checkpointIndex := n1.c.Tip()
	checkpoint, err := p2p.DownloadCheckpoint(ctx, n1.s.Addr(), genesisBlock.ID(), checkpointIndex)
	if err != nil {
		t.Fatal(err)
	}
	n2 := newTestNode(t, genesisBlock.ID(), checkpoint)
	defer n2.Close()
	go n2.run()
	if n2.c.Tip() != n1.c.Tip() {
		t.Fatal("tips should match after loading from checkpoint")
	}

	// connect the nodes and have n2 mine some blocks
	if err := n1.s.Connect(n2.s.Addr()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := n2.mineBlock(); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(100 * time.Millisecond)

	// tips should match
	if n1.c.Tip() != n2.c.Tip() {
		t.Fatal("tips should match after mining on checkpoint")
	}
}
