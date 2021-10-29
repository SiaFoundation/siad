package chainutil

import (
	"io"
	"os"
	"testing"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
)

func TestFlatStoreRecovery(t *testing.T) {
	dir, err := os.MkdirTemp(os.TempDir(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	sim := NewChainSim()
	fs, _, err := NewFlatStore(dir, sim.Genesis)
	if err != nil {
		t.Fatal(err)
	}

	// add some blocks and flush meta
	for i := 0; i < 5; i++ {
		b := sim.MineBlock()
		err := fs.AddCheckpoint(consensus.Checkpoint{
			Block:   b,
			Context: sim.Context,
		})
		if err != nil {
			t.Fatal(err)
		} else if err := fs.ExtendBest(sim.Context.Index); err != nil {
			t.Fatal(err)
		}
	}
	if fs.Flush(); err != nil {
		t.Fatal(err)
	}

	// compare tips
	if fs.meta.tip != sim.Context.Index {
		t.Fatal("meta tip mismatch", fs.meta.tip, sim.Context.Index)
	} else if index, err := fs.BestIndex(fs.meta.tip.Height); err != nil || index != fs.meta.tip {
		t.Fatal("tip mismatch", index, fs.meta.tip)
	}
	goodTip := fs.meta.tip

	// add more blocks, then close without flushing
	for i := 0; i < 5; i++ {
		b := sim.MineBlock()
		err := fs.AddCheckpoint(consensus.Checkpoint{
			Block:   b,
			Context: sim.Context,
		})
		if err != nil {
			t.Fatal(err)
		} else if err := fs.ExtendBest(sim.Context.Index); err != nil {
			t.Fatal(err)
		}
	}

	// simulate write failure by corrupting index, entry, and best files
	for _, f := range []*os.File{fs.indexFile, fs.entryFile, fs.bestFile} {
		f.Seek(-10, io.SeekEnd)
		f.WriteString("garbagegarbage")
	}
	if index, err := fs.BestIndex(fs.meta.tip.Height); err != nil {
		t.Fatal(err)
	} else if index == fs.meta.tip {
		t.Fatal("tip should not match after corruption")
	}

	// reload fs; should recover to last good state
	fs.indexFile.Close()
	fs.entryFile.Close()
	fs.bestFile.Close()
	fs, tip, err := NewFlatStore(dir, sim.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	if tip.Context.Index != goodTip || fs.meta.tip != goodTip {
		t.Fatal("tip mismatch", tip.Context.Index, fs.meta.tip, goodTip)
	} else if index, err := fs.BestIndex(goodTip.Height); err != nil || index != goodTip {
		t.Fatal("tip mismatch", index, goodTip)
	}
	fs.Close()
}

func BenchmarkFlatStore(b *testing.B) {
	dir, err := os.MkdirTemp(os.TempDir(), b.Name())
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(dir)
	fs, _, err := NewFlatStore(dir, consensus.Checkpoint{})
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()

	cp := consensus.Checkpoint{
		Block: types.Block{
			Transactions: make([]types.Transaction, 10),
		},
	}

	for i := 0; i < b.N; i++ {
		if err := fs.AddCheckpoint(cp); err != nil {
			b.Fatal(err)
		}
	}
}
