package main

import (
	"os"
	"path/filepath"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	"go.sia.tech/siad/v2/internal/chainutil"
	"go.sia.tech/siad/v2/internal/p2putil"
	"go.sia.tech/siad/v2/p2p"
	"go.sia.tech/siad/v2/txpool"
)

type node struct {
	c  *chain.Manager
	tp *txpool.Pool
	s  *p2p.Syncer
}

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
	tp := txpool.New(tip.State)
	cm.AddSubscriber(tp, cm.Tip())

	p2pDir := filepath.Join(dir, "p2p")
	if err := os.MkdirAll(p2pDir, 0700); err != nil {
		return nil, err
	}
	peerStore, err := p2putil.NewJSONStore(p2pDir)
	if err != nil {
		return nil, err
	}
	s, err := p2p.NewSyncer(addr, genesisBlock.ID(), cm, tp, peerStore)
	if err != nil {
		return nil, err
	}

	return &node{
		c:  cm,
		tp: tp,
		s:  s,
	}, nil
}
