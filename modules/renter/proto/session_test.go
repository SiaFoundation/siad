package proto

import (
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
)

func TestCalculateProofRanges(t *testing.T) {
	tests := []struct {
		desc       string
		numSectors uint64
		actions    []modules.LoopWriteAction
		exp        []crypto.ProofRange
	}{
		{
			desc:       "Append",
			numSectors: 2,
			actions: []modules.LoopWriteAction{
				{Type: modules.WriteActionAppend, Data: []byte{1, 2, 3}},
			},
			exp: []crypto.ProofRange{},
		},
		{
			desc:       "Swap",
			numSectors: 3,
			actions: []modules.LoopWriteAction{
				{Type: modules.WriteActionSwap, A: 1, B: 2},
			},
			exp: []crypto.ProofRange{
				{Start: 1, End: 2},
				{Start: 2, End: 3},
			},
		},
		{
			desc:       "Trim",
			numSectors: 3,
			actions: []modules.LoopWriteAction{
				{Type: modules.WriteActionTrim, A: 1},
			},
			exp: []crypto.ProofRange{
				{Start: 2, End: 3},
			},
		},
		{
			desc:       "AppendSwapTrim",
			numSectors: 12,
			actions: []modules.LoopWriteAction{
				{Type: modules.WriteActionAppend, Data: []byte{1, 2, 3}},
				{Type: modules.WriteActionSwap, A: 5, B: 12},
				{Type: modules.WriteActionTrim, A: 1},
			},
			exp: []crypto.ProofRange{
				{Start: 5, End: 6},
			},
		},
		{
			desc:       "SwapTrimSwapAppendAppend",
			numSectors: 12,
			actions: []modules.LoopWriteAction{
				{Type: modules.WriteActionSwap, A: 6, B: 11},
				{Type: modules.WriteActionTrim, A: 1},
				{Type: modules.WriteActionSwap, A: 7, B: 10},
				{Type: modules.WriteActionAppend, Data: []byte{1, 2, 3}},
				{Type: modules.WriteActionAppend, Data: []byte{4, 5, 6}},
			},
			exp: []crypto.ProofRange{
				{Start: 6, End: 7},
				{Start: 7, End: 8},
				{Start: 10, End: 11},
				{Start: 11, End: 12},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			res := calculateProofRanges(test.actions, test.numSectors)
			if !reflect.DeepEqual(res, test.exp) {
				t.Errorf("incorrect ranges: expected %v, got %v", test.exp, res)
			}
		})
	}
}

func TestModifyProofRanges(t *testing.T) {
	tests := []struct {
		desc        string
		numSectors  uint64
		actions     []modules.LoopWriteAction
		proofRanges []crypto.ProofRange
		exp         []crypto.ProofRange
	}{
		{
			desc:       "Append",
			numSectors: 2,
			actions: []modules.LoopWriteAction{
				{Type: modules.WriteActionAppend, Data: []byte{1, 2, 3}},
			},
			proofRanges: nil,
			exp: []crypto.ProofRange{
				{Start: 2, End: 3},
			},
		},
		{
			desc:       "Swap",
			numSectors: 3,
			actions: []modules.LoopWriteAction{
				{Type: modules.WriteActionSwap, A: 1, B: 2},
			},
			proofRanges: []crypto.ProofRange{
				{Start: 1, End: 2},
				{Start: 2, End: 3},
			},
			exp: []crypto.ProofRange{
				{Start: 1, End: 2},
				{Start: 2, End: 3},
			},
		},
		{
			desc:       "Trim",
			numSectors: 3,
			actions: []modules.LoopWriteAction{
				{Type: modules.WriteActionTrim, A: 1},
			},
			proofRanges: []crypto.ProofRange{
				{Start: 2, End: 3},
			},
			exp: []crypto.ProofRange{},
		},
		{
			desc:       "AppendSwapTrim",
			numSectors: 12,
			actions: []modules.LoopWriteAction{
				{Type: modules.WriteActionAppend, Data: []byte{1, 2, 3}},
				{Type: modules.WriteActionSwap, A: 5, B: 12},
				{Type: modules.WriteActionTrim, A: 1},
			},
			proofRanges: []crypto.ProofRange{
				{Start: 5, End: 6},
			},
			exp: []crypto.ProofRange{
				{Start: 5, End: 6},
			},
		},
		{
			desc:       "SwapTrimSwapAppendAppend",
			numSectors: 12,
			actions: []modules.LoopWriteAction{
				{Type: modules.WriteActionSwap, A: 6, B: 11},
				{Type: modules.WriteActionTrim, A: 1},
				{Type: modules.WriteActionSwap, A: 7, B: 10},
				{Type: modules.WriteActionAppend, Data: []byte{1, 2, 3}},
				{Type: modules.WriteActionAppend, Data: []byte{4, 5, 6}},
			},
			proofRanges: []crypto.ProofRange{
				{Start: 6, End: 7},
				{Start: 7, End: 8},
				{Start: 10, End: 11},
				{Start: 11, End: 12},
			},
			exp: []crypto.ProofRange{
				{Start: 6, End: 7},
				{Start: 7, End: 8},
				{Start: 10, End: 11},
				{Start: 11, End: 12},
				{Start: 12, End: 13},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			res := modifyProofRanges(test.proofRanges, test.actions, test.numSectors)
			if !reflect.DeepEqual(res, test.exp) {
				t.Errorf("incorrect modification: expected %v, got %v", test.exp, res)
			}
		})
	}
}

func TestModifyLeafHashes(t *testing.T) {
	tests := []struct {
		desc       string
		numSectors uint64
		actions    []modules.LoopWriteAction
		leaves     []crypto.Hash
		exp        []crypto.Hash
	}{
		{
			desc:       "Append",
			numSectors: 2,
			actions: []modules.LoopWriteAction{
				{Type: modules.WriteActionAppend, Data: []byte{1, 2, 3}},
			},
			leaves: nil,
			exp:    []crypto.Hash{crypto.MerkleRoot([]byte{1, 2, 3})},
		},
		{
			desc:       "Swap",
			numSectors: 3,
			actions: []modules.LoopWriteAction{
				{Type: modules.WriteActionSwap, A: 1, B: 2},
			},
			leaves: []crypto.Hash{{1}, {2}},
			exp:    []crypto.Hash{{2}, {1}},
		},
		{
			desc:       "Trim",
			numSectors: 3,
			actions: []modules.LoopWriteAction{
				{Type: modules.WriteActionTrim, A: 1},
			},
			leaves: []crypto.Hash{{1}},
			exp:    []crypto.Hash{},
		},
		{
			desc:       "AppendSwapTrim",
			numSectors: 12,
			actions: []modules.LoopWriteAction{
				{Type: modules.WriteActionAppend, Data: []byte{1, 2, 3}},
				{Type: modules.WriteActionSwap, A: 5, B: 12},
				{Type: modules.WriteActionTrim, A: 1},
			},
			leaves: []crypto.Hash{{1}},
			exp:    []crypto.Hash{crypto.MerkleRoot([]byte{1, 2, 3})},
		},
		{
			desc:       "SwapTrimSwapAppendAppend",
			numSectors: 12,
			actions: []modules.LoopWriteAction{
				{Type: modules.WriteActionSwap, A: 6, B: 11},
				{Type: modules.WriteActionTrim, A: 1},
				{Type: modules.WriteActionSwap, A: 7, B: 10},
				{Type: modules.WriteActionAppend, Data: []byte{1, 2, 3}},
				{Type: modules.WriteActionAppend, Data: []byte{4, 5, 6}},
			},
			leaves: []crypto.Hash{{1}, {2}, {3}, {4}},
			exp:    []crypto.Hash{{4}, {3}, {2}, crypto.MerkleRoot([]byte{1, 2, 3}), crypto.MerkleRoot([]byte{4, 5, 6})},
		},
	}
	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			res := modifyLeaves(test.leaves, test.actions, test.numSectors)
			if !reflect.DeepEqual(res, test.exp) {
				t.Errorf("incorrect modification: expected %v, got %v", test.exp, res)
			}
		})
	}
}
