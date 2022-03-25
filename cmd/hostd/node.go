package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/api/hostd"
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

type node struct {
	c   *chain.Manager
	tp  *txpool.Pool
	s   *p2p.Syncer
	w   *walletutil.TestingWallet
	h   *host.Host
	mr  host.MetricRecorder
	log host.Logger

	cs hostd.ContractStore
	m  *cpuminer.CPUMiner
}

var (
	defaultHostSettings = rhp.HostSettings{
		AcceptingContracts:         true,
		EphemeralAccountExpiry:     time.Hour * 24,
		MaxCollateral:              types.Siacoins(1000),
		MaxDuration:                144 * 30 * 3,
		MaxEphemeralAccountBalance: types.Siacoins(10),
		SectorSize:                 rhp.SectorSize,
		RemainingRegistryEntries:   1000,
		TotalRegistryEntries:       1000,
		RemainingStorage:           1 << 30,
		TotalStorage:               1 << 30,
		Version:                    "v2.0.0",
		WindowSize:                 144,

		ContractFee:            types.Siacoins(1).Div64(5),
		Collateral:             types.Siacoins(1).Div64(1 << 40),          // 1 SC/TB/block
		DownloadBandwidthPrice: types.Siacoins(10).Div64(1 << 40),         // 1 SC/TB
		UploadBandwidthPrice:   types.ZeroCurrency,                        // 0 SC/TB
		StoragePrice:           types.Siacoins(1).Div64(2).Div64(1 << 40), // 0.5 SC/TB/block

		RPCAccountBalanceCost: types.NewCurrency64(1),
		RPCFundAccountCost:    types.NewCurrency64(1),
		RPCHostSettingsCost:   types.NewCurrency64(1),
		RPCLatestRevisionCost: types.NewCurrency64(1),

		ProgInitBaseCost:   types.NewCurrency64(1),
		ProgMemoryTimeCost: types.NewCurrency64(1),
		ProgReadCost:       types.NewCurrency64(1),
		ProgWriteCost:      types.NewCurrency64(1),

		InstrAppendSectorBaseCost:   types.NewCurrency64(1),
		InstrDropSectorsBaseCost:    types.NewCurrency64(1),
		InstrDropSectorsUnitCost:    types.ZeroCurrency,
		InstrHasSectorBaseCost:      types.NewCurrency64(1),
		InstrReadBaseCost:           types.NewCurrency64(1),
		InstrRevisionBaseCost:       types.NewCurrency64(1),
		InstrSectorRootsBaseCost:    types.NewCurrency64(1),
		InstrSwapSectorBaseCost:     types.NewCurrency64(1),
		InstrUpdateSectorBaseCost:   types.NewCurrency64(1),
		InstrWriteBaseCost:          types.NewCurrency64(1),
		InstrReadRegistryBaseCost:   types.NewCurrency64(1),
		InstrUpdateRegistryBaseCost: types.NewCurrency64(1),
	}
)

func (n *node) run() error {
	return n.s.Run()
}

func (n *node) Close() error {
	errs := []error{
		n.s.Close(),
		n.c.Close(),
	}
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func newNode(p2pAddr, hostAddr, dir string, c consensus.Checkpoint) (*node, error) {
	chainDir := filepath.Join(dir, "chain")
	if err := os.MkdirAll(chainDir, 0700); err != nil {
		return nil, fmt.Errorf("could not create chain directory: %v", err)
	}
	chainStore, tip, err := chainutil.NewFlatStore(chainDir, c)
	if err != nil {
		return nil, fmt.Errorf("could not create chain store: %v", err)
	}

	walletDir := filepath.Join(dir, "wallet")
	if err := os.MkdirAll(walletDir, 0700); err != nil {
		return nil, fmt.Errorf("could not create wallet dir: %v", err)
	}
	walletStore, walletTip, err := walletutil.NewJSONStore(walletDir, tip.Context.Index)
	if err != nil {
		return nil, fmt.Errorf("could not create wallet store: %v", err)
	}

	cm := chain.NewManager(chainStore, tip.Context)
	tp := txpool.New(tip.Context)
	cm.AddSubscriber(tp, cm.Tip())
	if err := cm.AddSubscriber(walletStore, walletTip); err != nil {
		return nil, fmt.Errorf("couldn't resubscribe wallet at index %v: %w", walletTip, err)
	}
	w := walletutil.NewTestingWallet(cm.TipContext())
	cm.AddSubscriber(w, cm.Tip())

	m := cpuminer.New(tip.Context, w.NewAddress(), tp)
	cm.AddSubscriber(m, cm.Tip())

	p2pDir := filepath.Join(dir, "p2p")
	if err := os.MkdirAll(p2pDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to make p2p dir: %w", err)
	}
	peerStore, err := p2putil.NewJSONStore(p2pDir)
	if err != nil {
		return nil, fmt.Errorf("failed to init peer store: %w", err)
	}
	s, err := p2p.NewSyncer(p2pAddr, genesisBlock.ID(), cm, tp, peerStore)
	if err != nil {
		return nil, fmt.Errorf("failed to init syncer: %w", err)
	}

	hostKey := types.NewPrivateKeyFromSeed(frand.Entropy256())
	ss := hostutil.NewEphemeralSectorStore()
	cs := hostutil.NewEphemeralContractStore()
	rs := hostutil.NewEphemeralRegistryStore(1000)
	es := hostutil.NewMemAccountStore()
	r := hostutil.NewEphemeralMetricRecorder()
	log := hostutil.NewStdOutLogger()
	h, err := host.New(hostAddr, hostKey, defaultHostSettings, cm, ss, cs, rs, es, w, tp, r, log)
	if err != nil {
		return nil, fmt.Errorf("failed to init host: %w", err)
	}
	return &node{
		c:   cm,
		tp:  tp,
		s:   s,
		w:   w,
		m:   m,
		h:   h,
		mr:  r,
		cs:  cs,
		log: log,
	}, nil
}
