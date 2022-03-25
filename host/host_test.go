package host_test

import (
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
	"lukechampine.com/frand"
)

var testSettings = rhp.HostSettings{
	AcceptingContracts: true,
	MaxCollateral:      types.Siacoins(1e6),
	MaxDuration:        1000,
	SectorSize:         rhp.SectorSize,
	WindowSize:         144,

	ContractFee:            types.Siacoins(1),
	Collateral:             types.Siacoins(2).Div64(rhp.SectorSize).Div64(144), // 2 SC / sector / day
	StoragePrice:           types.Siacoins(1).Div64(rhp.SectorSize).Div64(144), // 1 SC / sector / day
	DownloadBandwidthPrice: types.Siacoins(1).Div64(rhp.SectorSize),            // 1 SC / sector
	UploadBandwidthPrice:   types.Siacoins(1).Div64(rhp.SectorSize),            // 1 SC / sector

	RPCAccountBalanceCost: types.NewCurrency64(1), // 1 H / op
	RPCFundAccountCost:    types.NewCurrency64(1), // 1 H / op
	RPCHostSettingsCost:   types.NewCurrency64(1), // 1 H / op
	RPCLatestRevisionCost: types.NewCurrency64(1), // 1 H / op
}

type testNode struct {
	mining int32
	c      *chain.Manager
	cs     chain.ManagerStore
	tp     *txpool.Pool
	s      *p2p.Syncer
	h      *host.Host
	w      *walletutil.TestingWallet
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

func (tn *testNode) mineBlock() (types.Block, error) {
again:
	b := tn.m.MineBlock()
	err := tn.c.AddTipBlock(b)
	if errors.Is(err, chain.ErrUnknownIndex) {
		goto again
	} else if err != nil {
		return b, err
	}
	tn.s.BroadcastBlock(b)
	time.Sleep(10 * time.Millisecond)
	return b, nil
}

func (tn *testNode) mineBlocks(n uint64) error {
	for i := uint64(0); i < n; i++ {
		if _, err := tn.mineBlock(); err != nil {
			return err
		}
	}
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
	w := walletutil.NewTestingWallet(cm.TipContext())
	cm.AddSubscriber(w, cm.Tip())
	m := cpuminer.New(c.Context, w.NewAddress(), tp)
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
	rec := hostutil.NewEphemeralMetricRecorder()
	h, err := host.New(":0", types.NewPrivateKeyFromSeed(frand.Entropy256()), testSettings, cm, sectorStore, contractStore, registryStore, accountStore, w, tp, rec, hostutil.NewStdOutLogger())
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
			for n.w.Balance().Cmp(types.Siacoins(100)) < 0 {
				time.Sleep(time.Millisecond * 5)
			}
			n.stopMining()
			wg.Done()
		}()
		nodes[i] = n
	}
	wg.Wait()
	for {
		var synced bool
		tip := nodes[0].c.Tip()
		for _, n := range nodes[1:] {
			synced = synced || n.c.Tip() == tip
		}
		if synced {
			break
		}
		time.Sleep(time.Millisecond * 5)
	}
	return nodes
}
