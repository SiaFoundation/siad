package renter_test

import (
	"bytes"
	"context"
	"errors"
	"io"
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
	"go.sia.tech/siad/v2/renter"
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

func TestFormContract(t *testing.T) {
	nodes := testNodes(t, 2)
	renterNode := nodes[0]
	hostNode := nodes[1]

	hostSettings := hostNode.h.Settings()
	hostPub := hostNode.h.PublicKey()

	session, err := renter.NewSession(hostSettings.NetAddress, hostPub, renterNode.c)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	settings, err := session.ScanSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// form a contract
	renterPriv := types.NewPrivateKeyFromSeed(frand.Entropy256())
	contract, _, err := session.FormContract(renterPriv, types.Siacoins(20), types.Siacoins(40), renterNode.c.Tip().Height+200, settings, renterNode.w, renterNode.tp)
	if err != nil {
		t.Fatal(err)
	}

	switch {
	case contract.Revision.HostOutput.Value.Cmp(hostSettings.ContractFee.Add(types.Siacoins(40))) != 0:
		t.Fatal("wrong host output value")
	case contract.Revision.MissedHostValue != contract.Revision.HostOutput.Value:
		t.Fatal("wrong missed host value")
	case contract.Revision.HostOutput.Address != hostSettings.Address:
		t.Fatal("wrong host output address")
	case contract.Revision.RenterOutput.Value.Cmp(types.Siacoins(20)) != 0:
		t.Fatal("wrong renter output value")
	}

	// mine the block with the contract in it
	if block, err := hostNode.mineBlock(); err != nil {
		t.Fatal(err)
	} else if len(block.Transactions) == 0 {
		t.Fatal("expected contract transaction to be mined")
	} else if len(block.Transactions[0].FileContracts) != 1 {
		t.Fatal("expected file contract")
	} else if block.Transactions[0].FileContracts[0] != contract.Revision {
		t.Fatal("broadcast contract does not match")
	}
}

func TestSettings(t *testing.T) {
	nodes := testNodes(t, 2)
	renterNode := nodes[0]
	hostNode := nodes[1]

	hostSettings := hostNode.h.Settings()
	hostPub := hostNode.h.PublicKey()

	session, err := renter.NewSession(hostSettings.NetAddress, hostPub, renterNode.c)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	// scan the settings
	settings, err := session.ScanSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// form a contract
	renterPriv := types.NewPrivateKeyFromSeed(frand.Entropy256())
	contract, _, err := session.FormContract(renterPriv, types.Siacoins(20), types.Siacoins(10), renterNode.c.Tip().Height+200, settings, renterNode.w, renterNode.tp)
	if err != nil {
		t.Fatal(err)
	}

	// register the settings with the host
	payment := session.PayByContract(&contract, renterPriv, renterPriv.PublicKey())
	if _, err := session.RegisterSettings(context.Background(), payment); err != nil {
		t.Fatal(err)
	}
}

func TestAccountFunding(t *testing.T) {
	nodes := testNodes(t, 2)
	renterNode := nodes[0]
	hostNode := nodes[1]

	hostSettings := hostNode.h.Settings()
	hostPub := hostNode.h.PublicKey()

	session, err := renter.NewSession(hostSettings.NetAddress, hostPub, renterNode.c)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	settings, err := session.ScanSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// form a contract
	renterPriv := types.NewPrivateKeyFromSeed(frand.Entropy256())
	contract, _, err := session.FormContract(renterPriv, types.Siacoins(10), types.Siacoins(20), renterNode.c.Tip().Height+200, settings, renterNode.w, renterNode.tp)
	if err != nil {
		t.Fatal(err)
	}

	// register a price table on the host
	expectedHostOutput := types.Siacoins(20).Add(settings.ContractFee).Add(types.NewCurrency64(1))
	expectedRenterOutput := types.Siacoins(10).Sub(types.NewCurrency64(1))
	payment := session.PayByContract(&contract, renterPriv, renterPriv.PublicKey())
	if _, err := session.RegisterSettings(context.Background(), payment); err != nil {
		t.Fatal(err)
	} else if contract.Revision.HostOutput.Value != expectedHostOutput {
		t.Fatalf("expected %d H as payment for registering settings, got %d H", expectedHostOutput, contract.Revision.HostOutput.Value)
	} else if contract.Revision.HostOutput.Value != contract.Revision.MissedHostValue {
		t.Fatalf("expected valid and missed host outputs to be equal, got %d H and %d H", contract.Revision.HostOutput.Value, contract.Revision.MissedHostValue)
	} else if contract.Revision.RenterOutput.Value != expectedRenterOutput {
		t.Fatalf("expected %d H as payment for registering settings, got %d H", expectedRenterOutput, contract.Revision.RenterOutput.Value)
	}

	// add an additional hasting for the account balance RPC
	expectedHostOutput = expectedHostOutput.Add(types.NewCurrency64(1))
	expectedRenterOutput = expectedRenterOutput.Sub(types.NewCurrency64(1))
	if balance, err := session.AccountBalance(context.Background(), renterPriv.PublicKey(), payment); err != nil {
		t.Fatal(err)
	} else if !balance.IsZero() {
		t.Fatalf("expected account balance to be zero, got %d", balance)
	} else if contract.Revision.HostOutput.Value != expectedHostOutput {
		t.Fatalf("expected %d H as payment for account balance, got %d H", expectedHostOutput, contract.Revision.HostOutput.Value)
	} else if contract.Revision.HostOutput.Value != contract.Revision.MissedHostValue {
		t.Fatalf("expected valid and missed host outputs to be equal, got %d H and %d H", contract.Revision.HostOutput.Value, contract.Revision.MissedHostValue)
	} else if contract.Revision.RenterOutput.Value != expectedRenterOutput {
		t.Fatalf("expected %d H as payment for registering settings, got %d H", expectedRenterOutput, contract.Revision.RenterOutput.Value)
	}

	// fund an account with 5 SC
	fundingAmount := types.Siacoins(5)
	expectedHostOutput = expectedHostOutput.Add(types.NewCurrency64(1)).Add(fundingAmount)
	expectedRenterOutput = expectedRenterOutput.Sub(types.NewCurrency64(1)).Sub(fundingAmount)
	if balance, err := session.FundAccount(context.Background(), renterPriv.PublicKey(), fundingAmount, payment); err != nil {
		t.Fatal(err)
	} else if balance != fundingAmount {
		t.Fatalf("expected account balance to be %d, got %d", fundingAmount, balance)
	} else if contract.Revision.HostOutput.Value != expectedHostOutput {
		t.Fatalf("expected %d H as payment for account funding, got %d H", expectedHostOutput, contract.Revision.HostOutput.Value)
	} else if contract.Revision.HostOutput.Value != contract.Revision.MissedHostValue {
		t.Fatalf("expected valid and missed host outputs to be equal, got %d H and %d H", contract.Revision.HostOutput.Value, contract.Revision.MissedHostValue)
	} else if contract.Revision.RenterOutput.Value != expectedRenterOutput {
		t.Fatalf("expected %d H as payment for registering settings, got %d H", expectedRenterOutput, contract.Revision.RenterOutput.Value)
	}

	// add an additional hasting for the account balance RPC
	expectedHostOutput = expectedHostOutput.Add(types.NewCurrency64(1))
	expectedRenterOutput = expectedRenterOutput.Sub(types.NewCurrency64(1))
	if _, err := session.AccountBalance(context.Background(), renterPriv.PublicKey(), payment); err != nil {
		t.Fatal(err)
	} else if contract.Revision.HostOutput.Value != expectedHostOutput {
		t.Fatalf("expected %d H as payment for account balance, got %d H", expectedHostOutput, contract.Revision.HostOutput.Value)
	} else if contract.Revision.HostOutput.Value != contract.Revision.MissedHostValue {
		t.Fatalf("expected valid and missed host outputs to be equal, got %d H and %d H", contract.Revision.HostOutput.Value, contract.Revision.MissedHostValue)
	} else if contract.Revision.RenterOutput.Value != expectedRenterOutput {
		t.Fatalf("expected %d H as payment for registering settings, got %d H", expectedRenterOutput, contract.Revision.RenterOutput.Value)
	}
}

func TestUploadDownloadProgram(t *testing.T) {
	nodes := testNodes(t, 2)
	renterNode := nodes[0]
	hostNode := nodes[1]

	hostSettings := hostNode.h.Settings()
	hostPub := hostNode.h.PublicKey()

	session, err := renter.NewSession(hostSettings.NetAddress, hostPub, renterNode.c)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	settings, err := session.ScanSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// form a contract
	renterPriv := types.NewPrivateKeyFromSeed(frand.Entropy256())
	contract, _, err := session.FormContract(renterPriv, types.Siacoins(50), types.Siacoins(100), renterNode.c.Tip().Height+200, settings, renterNode.w, renterNode.tp)
	if err != nil {
		t.Fatal(err)
	}

	// upload data to the contract
	vc := renterNode.c.TipContext()
	contractPayment := session.PayByContract(&contract, renterPriv, renterPriv.PublicKey())
	hostSettings, err = session.RegisterSettings(context.Background(), contractPayment)
	if err != nil {
		t.Fatal(err)
	}
	b := bytes.NewBuffer(nil)
	builder := rhp.NewProgramBuilder(hostSettings, b, contract.Revision.WindowEnd-vc.Index.Height)
	sectors := make([][rhp.SectorSize]byte, 10)
	for i := 0; i < len(sectors); i++ {
		frand.Read(sectors[i][:256])
		builder.AddAppendSectorInstruction(&sectors[i], true)
	}
	cost := builder.Cost()
	instructions, requiresContract, requiresFinalization, err := builder.Program()
	if err != nil {
		t.Fatal(err)
	}

	budget := cost.BaseCost.Add(cost.StorageCost).Add(settings.DownloadBandwidthPrice.Mul64(1 << 12)).Add(settings.UploadBandwidthPrice.Mul64(rhp.SectorSize * 11))
	if _, err := session.FundAccount(context.Background(), renterPriv.PublicKey(), budget, contractPayment); err != nil {
		t.Fatal(err)
	}

	payment := session.PayByEphemeralAccount(renterPriv.PublicKey(), renterPriv, vc.Index.Height+10)
	err = session.ExecuteProgram(renter.Program{
		Instructions: instructions,
		Budget:       budget,

		RequiresContract:     requiresContract,
		RequiresFinalization: requiresFinalization,
		Contract:             &contract,
		RenterKey:            renterPriv,
	}, b.Bytes(), payment, renter.NoopOnOutput)
	if err != nil {
		t.Fatal(err)
	} else if contract.Revision.RevisionNumber != 3 {
		t.Fatalf("wrong revision number, got %v expected %v", contract.Revision.RevisionNumber, 3)
	} else if contract.Revision.Filesize != uint64(rhp.SectorSize*len(sectors)) {
		t.Fatalf("wrong file size, got %v expected %v", contract.Revision.Filesize, rhp.SectorSize*2)
	}

	b.Reset()
	builder = rhp.NewProgramBuilder(hostSettings, b, contract.Revision.WindowEnd-vc.Index.Height)
	roots := make([]types.Hash256, 3)
	for i := 0; i < len(roots); i++ {
		roots[i] = rhp.SectorRoot(&sectors[frand.Intn(len(sectors))])
		if err := builder.AddReadSectorInstruction(roots[i], 0, rhp.SectorSize, false); err != nil {
			t.Fatal(err)
		}
	}

	cost = builder.Cost()
	instructions, requiresContract, requiresFinalization, err = builder.Program()
	if err != nil {
		t.Fatal(err)
	}

	budget = cost.BaseCost.Add(settings.DownloadBandwidthPrice.Mul64(rhp.SectorSize * 4))
	if _, err := session.FundAccount(context.Background(), renterPriv.PublicKey(), budget, contractPayment); err != nil {
		t.Fatal(err)
	}

	// execute download program
	downloadRoots := make([]types.Hash256, 0, len(roots))
	err = session.ExecuteProgram(renter.Program{
		Instructions: instructions,
		Budget:       budget,

		RequiresContract:     requiresContract,
		RequiresFinalization: requiresFinalization,
		Contract:             &contract,
		RenterKey:            renterPriv,
	}, b.Bytes(), payment, func(rir rhp.RPCExecuteInstrResponse, r io.Reader) error {
		root, _, err := rhp.ReadSector(r)
		if err != nil {
			return err
		}
		downloadRoots = append(downloadRoots, root)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range roots {
		if roots[i] != downloadRoots[i] {
			t.Fatalf("root %v wrong, got %v expected %v", i, downloadRoots[i], roots[i])
		}
	}
}

func TestReadAppend(t *testing.T) {
	nodes := testNodes(t, 2)
	renterNode := nodes[0]
	hostNode := nodes[1]

	hostSettings := hostNode.h.Settings()
	hostPub := hostNode.h.PublicKey()

	session, err := renter.NewSession(hostSettings.NetAddress, hostPub, renterNode.c)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	settings, err := session.ScanSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// form a contract
	renterPriv := types.NewPrivateKeyFromSeed(frand.Entropy256())
	contract, _, err := session.FormContract(renterPriv, types.Siacoins(500), types.Siacoins(1000), renterNode.c.Tip().Height+200, settings, renterNode.w, renterNode.tp)
	if err != nil {
		t.Fatal(err)
	} else if err := session.Lock(context.Background(), contract.ID, renterPriv); err != nil {
		t.Fatal(err)
	}

	var sector [rhp.SectorSize]byte
	frand.Read(sector[:256])
	root := rhp.SectorRoot(&sector)

	r, err := session.Append(context.Background(), &sector)
	if err != nil {
		t.Fatal(err)
	} else if root != r {
		t.Fatalf("wrong root, got %v expected %v", r, root)
	}

	// append more sectors
	for i := 0; i < 10; i++ {
		{
			var sector [rhp.SectorSize]byte
			frand.Read(sector[:256])
			root := rhp.SectorRoot(&sector)

			r, err := session.Append(context.Background(), &sector)
			if err != nil {
				t.Fatalf("append %v failed: %v", i, err)
			} else if root != r {
				t.Fatalf("append %v failed: wrong root, got %v expected %v", i, r, root)
			}
		}
	}

	sections := []rhp.RPCReadRequestSection{{
		MerkleRoot: root,
		Offset:     0,
		Length:     rhp.SectorSize,
	}}
	buf := bytes.NewBuffer(nil)
	if err := session.Read(context.Background(), buf, sections); err != nil {
		t.Fatal(err)
	}

	var readSector [rhp.SectorSize]byte
	copy(readSector[:], buf.Bytes())
	if sector != readSector {
		t.Fatal("wrong sector read")
	}
}

func BenchmarkDownloadProgram(b *testing.B) {
	nodes := testNodes(b, 2)
	renterNode := nodes[0]
	hostNode := nodes[1]

	hostSettings := hostNode.h.Settings()
	hostPub := hostNode.h.PublicKey()

	session, err := renter.NewSession(hostSettings.NetAddress, hostPub, renterNode.c)
	if err != nil {
		b.Fatal(err)
	}
	defer session.Close()

	settings, err := session.ScanSettings(context.Background())
	if err != nil {
		b.Fatal(err)
	}

	// form a contract
	renterPriv := types.NewPrivateKeyFromSeed(frand.Entropy256())
	contract, _, err := session.FormContract(renterPriv, types.Siacoins(5000), types.Siacoins(10000), renterNode.c.Tip().Height+200, settings, renterNode.w, renterNode.tp)
	if err != nil {
		b.Fatal(err)
	}

	// upload data to the contract
	vc := renterNode.c.TipContext()
	contractPayment := session.PayByContract(&contract, renterPriv, renterPriv.PublicKey())
	hostSettings, err = session.RegisterSettings(context.Background(), contractPayment)
	if err != nil {
		b.Fatal(err)
	}

	buf := bytes.NewBuffer(nil)
	builder := rhp.NewProgramBuilder(hostSettings, buf, contract.Revision.WindowEnd-vc.Index.Height)
	sectors := make([][rhp.SectorSize]byte, 10)
	for i := 0; i < len(sectors); i++ {
		frand.Read(sectors[i][:256])
		builder.AddAppendSectorInstruction(&sectors[i], true)
	}
	cost := builder.Cost()
	instructions, requiresContract, requiresFinalization, err := builder.Program()
	if err != nil {
		b.Fatal(err)
	}

	budget := cost.BaseCost.Add(cost.StorageCost).Add(settings.DownloadBandwidthPrice.Mul64(1 << 12)).Add(settings.UploadBandwidthPrice.Mul64(rhp.SectorSize * 11))
	if _, err := session.FundAccount(context.Background(), renterPriv.PublicKey(), budget, contractPayment); err != nil {
		b.Fatal(err)
	}

	payment := session.PayByEphemeralAccount(renterPriv.PublicKey(), renterPriv, vc.Index.Height+10)
	err = session.ExecuteProgram(renter.Program{
		Instructions: instructions,
		Budget:       budget,

		RequiresContract:     requiresContract,
		RequiresFinalization: requiresFinalization,
		Contract:             &contract,
		RenterKey:            renterPriv,
	}, buf.Bytes(), payment, renter.NoopOnOutput)
	if err != nil {
		b.Fatal(err)
	}

	buf.Reset()
	builder = rhp.NewProgramBuilder(hostSettings, buf, contract.Revision.WindowEnd-vc.Index.Height)
	roots := make([]types.Hash256, 256)
	for i := 0; i < len(roots); i++ {
		roots[i] = rhp.SectorRoot(&sectors[frand.Intn(len(sectors))])
		if err := builder.AddReadSectorInstruction(roots[i], 0, rhp.SectorSize, false); err != nil {
			b.Fatal(err)
		}
	}

	cost = builder.Cost()
	instructions, requiresContract, requiresFinalization, err = builder.Program()
	if err != nil {
		b.Fatal(err)
	}

	budget = cost.BaseCost.Add(settings.DownloadBandwidthPrice.Mul64(rhp.SectorSize * 300))
	if _, err := session.FundAccount(context.Background(), renterPriv.PublicKey(), budget, contractPayment); err != nil {
		b.Fatal(err)
	}

	// execute download program
	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(256 * rhp.SectorSize)
	err = session.ExecuteProgram(renter.Program{
		Instructions: instructions,
		Budget:       budget,

		RequiresContract:     requiresContract,
		RequiresFinalization: requiresFinalization,
		Contract:             &contract,
		RenterKey:            renterPriv,
	}, buf.Bytes(), payment, renter.NoopOnOutput)
	if err != nil {
		b.Fatal(err)
	}
}

func BenchmarkUploadProgram(b *testing.B) {
	nodes := testNodes(b, 2)
	renterNode := nodes[0]
	hostNode := nodes[1]

	hostSettings := hostNode.h.Settings()
	hostPub := hostNode.h.PublicKey()

	session, err := renter.NewSession(hostSettings.NetAddress, hostPub, renterNode.c)
	if err != nil {
		b.Fatal(err)
	}
	defer session.Close()

	settings, err := session.ScanSettings(context.Background())
	if err != nil {
		b.Fatal(err)
	}

	// form a contract
	renterPriv := types.NewPrivateKeyFromSeed(frand.Entropy256())
	contract, _, err := session.FormContract(renterPriv, types.Siacoins(5000), types.Siacoins(10000), renterNode.c.Tip().Height+200, settings, renterNode.w, renterNode.tp)
	if err != nil {
		b.Fatal(err)
	}

	// upload data to the contract
	vc := renterNode.c.TipContext()
	contractPayment := session.PayByContract(&contract, renterPriv, renterPriv.PublicKey())
	hostSettings, err = session.RegisterSettings(context.Background(), contractPayment)
	if err != nil {
		b.Fatal(err)
	}

	buf := bytes.NewBuffer(nil)
	builder := rhp.NewProgramBuilder(hostSettings, buf, contract.Revision.WindowEnd-vc.Index.Height)
	sectors := make([][rhp.SectorSize]byte, 10)
	for i := 0; i < len(sectors); i++ {
		frand.Read(sectors[i][:256])
	}
	for i := 0; i < 256; i++ {
		builder.AddAppendSectorInstruction(&sectors[frand.Intn(len(sectors))], true)
	}
	cost := builder.Cost()
	instructions, requiresContract, requiresFinalization, err := builder.Program()
	if err != nil {
		b.Fatal(err)
	}

	budget := cost.BaseCost.Add(cost.StorageCost).Add(settings.DownloadBandwidthPrice.Mul64(1 << 20)).Add(settings.UploadBandwidthPrice.Mul64(rhp.SectorSize * 300))
	if _, err := session.FundAccount(context.Background(), renterPriv.PublicKey(), budget, contractPayment); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(256 * rhp.SectorSize)
	// execute upload program
	payment := session.PayByEphemeralAccount(renterPriv.PublicKey(), renterPriv, vc.Index.Height+10)
	err = session.ExecuteProgram(renter.Program{
		Instructions: instructions,
		Budget:       budget,

		RequiresContract:     requiresContract,
		RequiresFinalization: requiresFinalization,
		Contract:             &contract,
		RenterKey:            renterPriv,
	}, buf.Bytes(), payment, renter.NoopOnOutput)
	if err != nil {
		b.Fatal(err)
	}
}
