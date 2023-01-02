package consensus

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"gitlab.com/NebulousLabs/bolt"
	"gitlab.com/NebulousLabs/encoding"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"

	cconsensus "go.sia.tech/core/consensus"
	ctypes "go.sia.tech/core/types"
)

func coreConvertToCore(from interface{}, to ctypes.DecoderFrom) {
	d := ctypes.NewBufDecoder(encoding.Marshal(from))
	to.DecodeFrom(d)
	if d.Err() != nil {
		panic(fmt.Sprintf("type conversion failed (%T->%t): %v", from, to, d.Err()))
	}
}

func coreConvertBlock(b types.Block) (cb ctypes.Block) {
	coreConvertToCore(b, &cb)
	return
}

func coreConvertToSiad(from ctypes.EncoderTo, to interface{}) {
	var buf bytes.Buffer
	e := ctypes.NewEncoder(&buf)
	from.EncodeTo(e)
	if err := encoding.Unmarshal(buf.Bytes(), to); err != nil {
		panic(fmt.Sprintf("type conversion failed (%T->%t): %v", from, to, err))
	}
}

func coreValidationContext(tx *bolt.Tx) cconsensus.ValidationContext {
	id := currentBlockID(tx)
	pb, _ := getBlockMap(tx, id)
	totalTime, totalTarget := (*ConsensusSet)(nil).getBlockTotals(tx, id)
	primary, failsafe := getFoundationUnlockHashes(tx)

	s := cconsensus.State{
		Index:                     ctypes.ChainIndex{Height: uint64(pb.Height), ID: ctypes.BlockID(id)},
		TotalWork:                 ctypes.WorkRequiredForHash(ctypes.BlockID(pb.Depth)),
		Difficulty:                ctypes.WorkRequiredForHash(ctypes.BlockID(pb.ChildTarget)),
		OakWork:                   ctypes.WorkRequiredForHash(ctypes.BlockID(totalTarget)),
		OakTime:                   time.Duration(totalTime) * time.Second,
		GenesisTimestamp:          time.Unix(int64(types.GenesisTimestamp), 0),
		FoundationPrimaryAddress:  ctypes.Address(primary),
		FoundationFailsafeAddress: ctypes.Address(failsafe),
	}
	coreConvertToCore(getSiafundPool(tx), &s.SiafundPool)
	s.PrevTimestamps[0] = time.Unix(int64(pb.Block.Timestamp), 0)
	parent := pb.Block.ParentID
	for i := 1; i < len(s.PrevTimestamps); i++ {
		if parent == (types.BlockID{}) {
			s.PrevTimestamps[i] = s.PrevTimestamps[i-1]
			continue
		}
		parentBytes := tx.Bucket(BlockMap).Get(parent[:])
		copy(parent[:], parentBytes[:32])
		s.PrevTimestamps[i] = time.Unix(int64(encoding.DecUint64(parentBytes[40:48])), 0)
	}

	return cconsensus.ValidationContext{
		State:    s,
		Blocks:   coreBlockStoreWraper{tx},
		Elements: coreElementStoreWrapper{tx},
	}
}

func coreApplyBlock(vc cconsensus.ValidationContext, b ctypes.Block) (cconsensus.State, cconsensus.BlockDiff) {
	return cconsensus.ApplyBlock(vc.State, vc.Elements, b)
}

func coreConvertDiff(diff cconsensus.BlockDiff, blockHeight types.BlockHeight) (cc modules.ConsensusChangeDiffs) {
	for _, tdiff := range diff.Transactions {
		for id, sco := range tdiff.CreatedSiacoinOutputs {
			d := modules.SiacoinOutputDiff{
				Direction: true,
				ID:        types.SiacoinOutputID(id),
			}
			coreConvertToSiad(sco, &d.SiacoinOutput)
			cc.SiacoinOutputDiffs = append(cc.SiacoinOutputDiffs, d)
		}
		for id, sco := range tdiff.SpentSiacoinOutputs {
			d := modules.SiacoinOutputDiff{
				Direction: false,
				ID:        types.SiacoinOutputID(id),
			}
			coreConvertToSiad(sco, &d.SiacoinOutput)
			cc.SiacoinOutputDiffs = append(cc.SiacoinOutputDiffs, d)
		}
		for id, fc := range tdiff.CreatedFileContracts {
			d := modules.FileContractDiff{
				Direction: true,
				ID:        types.FileContractID(id),
			}
			coreConvertToSiad(fc, &d.FileContract)
			cc.FileContractDiffs = append(cc.FileContractDiffs, d)
		}
		for id, fc := range tdiff.RevisedFileContracts {
			d := modules.FileContractDiff{
				Direction: false,
				ID:        types.FileContractID(id),
			}
			coreConvertToSiad(fc, &d.FileContract)
			cc.FileContractDiffs = append(cc.FileContractDiffs, d)
		}
		for id, fc := range tdiff.ValidFileContracts {
			d := modules.FileContractDiff{
				Direction: false,
				ID:        types.FileContractID(id),
			}
			coreConvertToSiad(fc, &d.FileContract)
			cc.FileContractDiffs = append(cc.FileContractDiffs, d)
		}

		for id, sfo := range tdiff.CreatedSiafundOutputs {
			d := modules.SiafundOutputDiff{
				Direction: true,
				ID:        types.SiafundOutputID(id),
			}
			coreConvertToSiad(sfo, &d.SiafundOutput)
			cc.SiafundOutputDiffs = append(cc.SiafundOutputDiffs, d)
		}
		for id, sfo := range tdiff.SpentSiafundOutputs {
			d := modules.SiafundOutputDiff{
				Direction: false,
				ID:        types.SiafundOutputID(id),
			}
			coreConvertToSiad(sfo, &d.SiafundOutput)
			cc.SiafundOutputDiffs = append(cc.SiafundOutputDiffs, d)
		}

		for id, sco := range tdiff.DelayedSiacoinOutputs {
			d := modules.DelayedSiacoinOutputDiff{
				Direction:      true,
				ID:             types.SiacoinOutputID(id),
				MaturityHeight: blockHeight + types.MaturityDelay,
			}
			coreConvertToSiad(sco, &d.SiacoinOutput)
			cc.DelayedSiacoinOutputDiffs = append(cc.DelayedSiacoinOutputDiffs, d)
		}
	}
	for id, sco := range diff.DelayedSiacoinOutputs {
		d := modules.DelayedSiacoinOutputDiff{
			Direction:      true,
			ID:             types.SiacoinOutputID(id),
			MaturityHeight: blockHeight + types.MaturityDelay,
		}
		coreConvertToSiad(sco, &d.SiacoinOutput)
		cc.DelayedSiacoinOutputDiffs = append(cc.DelayedSiacoinOutputDiffs, d)
	}
	for id, sco := range diff.MaturedSiacoinOutputs {
		d := modules.DelayedSiacoinOutputDiff{
			Direction:      false,
			ID:             types.SiacoinOutputID(id),
			MaturityHeight: blockHeight,
		}
		coreConvertToSiad(sco, &d.SiacoinOutput)
		cc.DelayedSiacoinOutputDiffs = append(cc.DelayedSiacoinOutputDiffs, d)

		sd := modules.SiacoinOutputDiff{
			Direction: true,
			ID:        types.SiacoinOutputID(id),
		}
		coreConvertToSiad(sco, &d.SiacoinOutput)
		cc.SiacoinOutputDiffs = append(cc.SiacoinOutputDiffs, sd)
	}
	for id, fc := range diff.MissedFileContracts {
		d := modules.FileContractDiff{
			Direction: false,
			ID:        types.FileContractID(id),
		}
		coreConvertToSiad(fc, &d.FileContract)
		cc.FileContractDiffs = append(cc.FileContractDiffs, d)
	}
	return
}

func coreNormalizeDiff(diff *modules.ConsensusChangeDiffs) {
	sort.Slice(diff.SiacoinOutputDiffs, func(i, j int) bool {
		c := bytes.Compare(diff.SiacoinOutputDiffs[i].ID[:], diff.SiacoinOutputDiffs[j].ID[:])
		if c == 0 {
			return diff.SiacoinOutputDiffs[i].Direction == modules.DiffApply
		}
		return c < 0
	})
	sort.Slice(diff.FileContractDiffs, func(i, j int) bool {
		c := bytes.Compare(diff.FileContractDiffs[i].ID[:], diff.FileContractDiffs[j].ID[:])
		if c == 0 {
			return diff.FileContractDiffs[i].Direction == modules.DiffApply
		}
		return c < 0
	})
	sort.Slice(diff.SiafundOutputDiffs, func(i, j int) bool {
		c := bytes.Compare(diff.SiafundOutputDiffs[i].ID[:], diff.SiafundOutputDiffs[j].ID[:])
		if c == 0 {
			return diff.SiafundOutputDiffs[i].Direction == modules.DiffApply
		}
		return c < 0
	})
	sort.Slice(diff.DelayedSiacoinOutputDiffs, func(i, j int) bool {
		c := bytes.Compare(diff.DelayedSiacoinOutputDiffs[i].ID[:], diff.DelayedSiacoinOutputDiffs[j].ID[:])
		if c == 0 {
			return diff.DelayedSiacoinOutputDiffs[i].Direction == modules.DiffApply
		}
		return c < 0
	})
	diff.SiafundPoolDiffs = nil
}

func coreEqualDiff(core, siad modules.ConsensusChangeDiffs) bool {
	siadCopy := modules.ConsensusChangeDiffs{
		SiacoinOutputDiffs:        append([]modules.SiacoinOutputDiff(nil), siad.SiacoinOutputDiffs...),
		FileContractDiffs:         append([]modules.FileContractDiff(nil), siad.FileContractDiffs...),
		SiafundOutputDiffs:        append([]modules.SiafundOutputDiff(nil), siad.SiafundOutputDiffs...),
		DelayedSiacoinOutputDiffs: append([]modules.DelayedSiacoinOutputDiff(nil), siad.DelayedSiacoinOutputDiffs...),
	}
	coreNormalizeDiff(&core)
	coreNormalizeDiff(&siadCopy)
	return bytes.Equal(encoding.Marshal(core), encoding.Marshal(siadCopy))
}

type coreBlockStoreWraper struct {
	tx *bolt.Tx
}

func (w coreBlockStoreWraper) BestIndex(height uint64) (ctypes.ChainIndex, bool) {
	id, err := getPath(w.tx, types.BlockHeight(height))
	return ctypes.ChainIndex{Height: height, ID: ctypes.BlockID(id)}, err == nil
}

type coreElementStoreWrapper struct {
	tx *bolt.Tx
}

func (w coreElementStoreWrapper) SiacoinOutput(id ctypes.SiacoinOutputID) (sco ctypes.SiacoinOutput, ok bool) {
	o, err := getSiacoinOutput(w.tx, types.SiacoinOutputID(id))
	coreConvertToCore(o, &sco)
	ok = err == nil
	return
}

func (w coreElementStoreWrapper) SiafundOutput(id ctypes.SiafundOutputID) (sfo ctypes.SiafundOutput, claimStart ctypes.Currency, ok bool) {
	o, err := getSiafundOutput(w.tx, types.SiafundOutputID(id))
	coreConvertToCore(o, &sfo)
	coreConvertToCore(o.ClaimStart, &claimStart)
	ok = err == nil
	return
}

func (w coreElementStoreWrapper) FileContract(id ctypes.FileContractID) (fc ctypes.FileContract, ok bool) {
	c, err := getFileContract(w.tx, types.FileContractID(id))
	coreConvertToCore(c, &fc)
	ok = err == nil
	return
}

func (w coreElementStoreWrapper) MaturedSiacoinOutputs(height uint64) (ids []ctypes.SiacoinOutputID) {
	bucketID := append(prefixDSCO, encoding.Marshal(height)...)

	bucket := w.tx.Bucket(bucketID)
	if bucket == nil {
		return nil
	}
	bucket.ForEach(func(k, _ []byte) error {
		var id ctypes.SiacoinOutputID
		copy(id[:], k)
		ids = append(ids, id)
		return nil
	})
	return
}

func (w coreElementStoreWrapper) MissedFileContracts(height uint64) (ids []ctypes.FileContractID) {
	bucketID := append(prefixFCEX, encoding.Marshal(height)...)

	bucket := w.tx.Bucket(bucketID)
	if bucket == nil {
		return nil
	}
	bucket.ForEach(func(k, _ []byte) error {
		var id ctypes.FileContractID
		copy(id[:], k)
		ids = append(ids, id)
		return nil
	})
	return
}
