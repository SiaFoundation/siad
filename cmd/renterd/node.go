package main

import (
	"errors"
	"log"
	"os"
	"path/filepath"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	"go.sia.tech/siad/v2/internal/chainutil"
	"go.sia.tech/siad/v2/internal/cpuminer"
	"go.sia.tech/siad/v2/internal/hostdbutil"
	"go.sia.tech/siad/v2/internal/p2putil"
	"go.sia.tech/siad/v2/internal/walletutil"
	"go.sia.tech/siad/v2/p2p"
	"go.sia.tech/siad/v2/renter/hostdb"
	"go.sia.tech/siad/v2/txpool"
)

type node struct {
	c   *chain.Manager
	tp  *txpool.Pool
	s   *p2p.Syncer
	w   *walletutil.TestingWallet
	m   *cpuminer.CPUMiner
	hdb *hostdb.DB
}

func (n *node) run() error {
	return n.s.Run()
}

func (n *node) mine() {
	for {
		b := n.m.MineBlock()

		// give it to ourselves
		if err := n.c.AddTipBlock(b); err != nil {
			if !errors.Is(err, chain.ErrUnknownIndex) {
				log.Println("Couldn't add block:", err)
			}
			continue
		}
		log.Println("mined block", b.Index())

		// broadcast it
		n.s.BroadcastBlock(b)
	}
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

func newNode(addr, dir string, c consensus.Checkpoint) (*node, error) {
	chainDir := filepath.Join(dir, "chain")
	if err := os.MkdirAll(chainDir, 0700); err != nil {
		return nil, err
	}
	chainStore, tip, err := chainutil.NewFlatStore(chainDir, c)
	if err != nil {
		return nil, err
	}

	cm := chain.NewManager(chainStore, tip.State)
	w := walletutil.NewTestingWallet(tip.State)
	cm.AddSubscriber(w, cm.Tip())
	tp := txpool.New(tip.State)
	cm.AddSubscriber(tp, cm.Tip())
	m := cpuminer.New(tip.State, w.NewAddress(), tp)
	cm.AddSubscriber(m, cm.Tip())

	p2pDir := filepath.Join(dir, "p2p")
	if err := os.MkdirAll(p2pDir, 0700); err != nil {
		return nil, err
	}
	peerStore, err := p2putil.NewJSONStore(p2pDir)
	if err != nil {
		return nil, err
	}

	hdbDir := filepath.Join(dir, "hdb")
	if err := os.MkdirAll(hdbDir, 0700); err != nil {
		return nil, err
	}
	hdbStore, err := hostdbutil.NewJSONStore(hdbDir)
	if err != nil {
		return nil, err
	}
	hdb := hostdb.New(hdbStore)
	cm.AddSubscriber(hdb, cm.Tip())

	s, err := p2p.NewSyncer(addr, genesisBlock.ID(), cm, tp, peerStore)
	if err != nil {
		return nil, err
	}

	return &node{
		c:   cm,
		tp:  tp,
		s:   s,
		w:   w,
		m:   m,
		hdb: hdb,
	}, nil
}
