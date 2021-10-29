package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	"go.sia.tech/siad/v2/internal/chainutil"
	"go.sia.tech/siad/v2/internal/cpuminer"
	"go.sia.tech/siad/v2/internal/walletutil"
	"go.sia.tech/siad/v2/p2p"
	"go.sia.tech/siad/v2/txpool"
	"go.sia.tech/siad/v2/wallet"
)

type node struct {
	c  *chain.Manager
	tp *txpool.Pool
	s  *p2p.Syncer
	w  *wallet.HotWallet
	m  *cpuminer.CPUMiner
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

	walletDir := filepath.Join(dir, "wallet")
	if err := os.MkdirAll(walletDir, 0700); err != nil {
		return nil, err
	}
	walletStore, walletTip, err := walletutil.NewJSONStore(walletDir, tip.Context.Index)
	if err != nil {
		return nil, err
	}
	seed := wallet.NewSeed()

	cm := chain.NewManager(chainStore, tip.Context)
	tp := txpool.New(tip.Context)
	cm.AddSubscriber(tp, cm.Tip())
	if err := cm.AddSubscriber(walletStore, walletTip); err != nil {
		return nil, fmt.Errorf("couldn't resubscribe wallet at index %v: %w", walletTip, err)
	}
	w := wallet.NewHotWallet(walletStore, seed)

	m := cpuminer.New(tip.Context, w.NextAddress(), tp)
	cm.AddSubscriber(m, cm.Tip())

	s, err := p2p.NewSyncer(addr, genesisBlock.ID(), cm, tp)
	if err != nil {
		return nil, err
	}

	return &node{
		c:  cm,
		tp: tp,
		s:  s,
		w:  w,
		m:  m,
	}, nil
}
