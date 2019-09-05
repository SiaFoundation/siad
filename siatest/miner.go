package siatest

import (
	"bytes"
	"unsafe"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/types"
)

// solveHeader solves the header by finding a nonce for the target
func solveHeader(target types.Target, bh types.BlockHeader) (types.BlockHeader, error) {
	header := encoding.Marshal(bh)
	var nonce uint64
	for i := 0; i < 256; i++ {
		id := crypto.HashBytes(header)
		if bytes.Compare(target[:], id[:]) >= 0 {
			copy(bh.Nonce[:], header[32:40])
			return bh, nil
		}
		*(*uint64)(unsafe.Pointer(&header[32])) = nonce
		nonce += types.ASICHardforkFactor
	}
	return bh, errors.New("couldn't solve block")
}

// MineBlock makes the underlying node mine a single block and broadcast it.
func (tn *TestNode) MineBlock() error {
	// Get the header
	target, header, err := tn.MinerHeaderGet()
	if err != nil {
		return errors.AddContext(err, "failed to get header for work")
	}
	// Solve the header
	header, err = solveHeader(target, header)
	if err != nil {
		return errors.AddContext(err, "failed to solve header")
	}
	// Submit the header
	if err := tn.MinerHeaderPost(header); err != nil {
		return errors.AddContext(err, "failed to submit header")
	}
	return nil
}

// MineEmptyBlock mines an empty block without any transactions and broadcasts
// it.
func (tn *TestNode) MineEmptyBlock() error {
	// Get the current target.
	target, _, err := tn.MinerHeaderGet()
	if err != nil {
		return errors.AddContext(err, "failed to get target")
	}
	// Get the current blockheight.
	bh, err := tn.BlockHeight()
	if err != nil {
		return errors.AddContext(err, "failed to get current blockheight")
	}
	// Get the most recent block.
	cbg, err := tn.ConsensusBlocksHeightGet(bh)
	if err != nil {
		return errors.AddContext(err, "failed to get most recent block")
	}
	// Get a payout address.
	wag, err := tn.WalletAddressGet()
	if err != nil {
		return errors.AddContext(err, "failed to get new wallet address")
	}
	// Get the block.
	b := emptyBlockForWork(bh, wag.Address, cbg.ID)
	// Solve the block.
	header, err := solveHeader(target, b.Header())
	if err != nil {
		return errors.AddContext(err, "failed to solve block header")
	}
	b.Nonce = header.Nonce
	// Submit block.
	return errors.AddContext(tn.MinerBlockPost(b), "failed to submit block")
}

// emptyBlockForWork creates an empty block without any transactions.
func emptyBlockForWork(currentBlockHeight types.BlockHeight, address types.UnlockHash, parentID types.BlockID) types.Block {
	var b types.Block
	b.ParentID = parentID
	b.Timestamp = types.CurrentTimestamp()
	b.MinerPayouts = []types.SiacoinOutput{{
		Value:      b.CalculateSubsidy(currentBlockHeight + 1),
		UnlockHash: address,
	}}
	return b
}

// solveHeader solves the header by finding a nonce for the target
func solveHeader(target types.Target, bh types.BlockHeader) (types.BlockHeader, error) {
	header := encoding.Marshal(bh)
	var nonce uint64
	for i := 0; i < 256; i++ {
		id := crypto.HashBytes(header)
		if bytes.Compare(target[:], id[:]) >= 0 {
			copy(bh.Nonce[:], header[32:40])
			return bh, nil
		}
		*(*uint64)(unsafe.Pointer(&header[32])) = nonce
		nonce += types.ASICHardforkFactor
	}
	return bh, errors.New("couldn't solve block")
}
