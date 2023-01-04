package consensus

import (
	"bytes"
	"fmt"
	"log"
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
		panic(fmt.Sprintf("type conversion failed (%T->%T): %v", from, to, d.Err()))
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
	e.Flush()
	if err := encoding.Unmarshal(buf.Bytes(), to); err != nil {
		panic(fmt.Sprintf("type conversion failed (%T->%T): %v", from, to, err))
	}
}

func coreStoreState(tx *bolt.Tx, s cconsensus.State) {
	var buf bytes.Buffer
	e := ctypes.NewEncoder(&buf)
	s.EncodeTo(e)
	e.Flush()
	tx.Bucket(CoreStates).Put(s.Index.ID[:], buf.Bytes())
}

func coreGetState(tx *bolt.Tx, id ctypes.BlockID) (s cconsensus.State) {
	s.DecodeFrom(ctypes.NewBufDecoder(tx.Bucket(CoreStates).Get(id[:])))
	return
}

func coreCurrentValidationContext(tx *bolt.Tx) cconsensus.ValidationContext {
	id := currentBlockID(tx)
	s := coreGetState(tx, ctypes.BlockID(id))
	// check for divergence in work values
	if true {
		totalTime, totalTarget := (*ConsensusSet)(nil).getBlockTotals(tx, id)
		if totalTime > 0 {
			siadHashrate := ctypes.WorkRequiredForHash(ctypes.BlockID(totalTarget)).Div64(uint64(totalTime))
			coreHashrate := s.OakWork.Div64(uint64(s.OakTime.Seconds()))
			delta := siadHashrate.Sub(coreHashrate).String()
			if siadHashrate.Cmp(coreHashrate) < 0 {
				delta = "-" + coreHashrate.Sub(siadHashrate).String()
			}
			if delta != "0" {
				log.Println("oak hashrate diverges: " + delta)
			}
		}
		pb, _ := getBlockMap(tx, id)
		siadWork := ctypes.WorkRequiredForHash(ctypes.BlockID(pb.Depth))
		coreWork := s.TotalWork
		delta := siadWork.Sub(coreWork).String()
		if siadWork.Cmp(coreWork) < 0 {
			delta = "-" + coreWork.Sub(siadWork).String()
		}
		if delta != "0" {
			log.Println("total work diverges: " + delta)
		}
		siadDifficulty := ctypes.WorkRequiredForHash(ctypes.BlockID(pb.ChildTarget))
		coreDifficulty := s.Difficulty
		delta = siadDifficulty.Sub(coreDifficulty).String()
		if siadDifficulty.Cmp(coreDifficulty) < 0 {
			delta = "-" + coreDifficulty.Sub(siadDifficulty).String()
		}
		if delta != "0" {
			log.Println("difficulty diverges: " + delta)
		}
	}

	return cconsensus.ValidationContext{
		State: s,
		Store: coreStoreWrapper{tx},
	}
}

func coreComputeState(tx *bolt.Tx, id types.BlockID) cconsensus.State {
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
	parent := pb.Block.ParentID
	for i := range s.PrevTimestamps {
		if i == 0 {
			s.PrevTimestamps[i] = time.Unix(int64(pb.Block.Timestamp), 0)
		} else if parent != (types.BlockID{}) {
			parentBytes := tx.Bucket(BlockMap).Get(parent[:])
			copy(parent[:], parentBytes[:32])
			s.PrevTimestamps[i] = time.Unix(int64(encoding.DecUint64(parentBytes[40:48])), 0)
		} else {
			s.PrevTimestamps[i] = s.PrevTimestamps[i-1]
		}
	}
	return s
}

func coreApplyBlock(vc cconsensus.ValidationContext, b ctypes.Block) (cconsensus.State, cconsensus.BlockDiff) {
	return cconsensus.ApplyBlock(vc.State, vc.Store, b)
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

		md := modules.SiacoinOutputDiff{
			Direction: true,
			ID:        types.SiacoinOutputID(id),
		}
		coreConvertToSiad(sco, &md.SiacoinOutput)
		cc.SiacoinOutputDiffs = append(cc.SiacoinOutputDiffs, md)
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

	// TODO: we ignore siafund pool stuff for now
	for i := range diff.SiafundOutputDiffs {
		diff.SiafundOutputDiffs[i].SiafundOutput.ClaimStart = types.ZeroCurrency
	}
	diff.SiafundPoolDiffs = nil
}

type coreStoreWrapper struct {
	tx *bolt.Tx
}

func (w coreStoreWrapper) BestIndex(height uint64) (ctypes.ChainIndex, bool) {
	id, err := getPath(w.tx, types.BlockHeight(height))
	return ctypes.ChainIndex{Height: height, ID: ctypes.BlockID(id)}, err == nil
}

func (w coreStoreWrapper) AncestorTimestamp(id ctypes.BlockID, n uint64) time.Time {
	blockMap := w.tx.Bucket(BlockMap)
	current := id[:]
	for ; n > 1; n-- {
		current = blockMap.Get(current[:])[:32]
	}
	return time.Unix(int64(encoding.DecUint64(blockMap.Get(current)[40:48])), 0)
}

func (w coreStoreWrapper) SiacoinOutput(id ctypes.SiacoinOutputID) (sco ctypes.SiacoinOutput, ok bool) {
	o, err := getSiacoinOutput(w.tx, types.SiacoinOutputID(id))
	coreConvertToCore(o, &sco)
	ok = err == nil
	return
}

func (w coreStoreWrapper) SiafundOutput(id ctypes.SiafundOutputID) (sfo ctypes.SiafundOutput, claimStart ctypes.Currency, ok bool) {
	o, err := getSiafundOutput(w.tx, types.SiafundOutputID(id))
	coreConvertToCore(o, &sfo)
	coreConvertToCore(o.ClaimStart, &claimStart)
	ok = err == nil
	return
}

func (w coreStoreWrapper) FileContract(id ctypes.FileContractID) (fc ctypes.FileContract, ok bool) {
	c, err := getFileContract(w.tx, types.FileContractID(id))
	coreConvertToCore(c, &fc)
	ok = err == nil
	return
}

func (w coreStoreWrapper) MaturedSiacoinOutputs(height uint64) (ids []ctypes.SiacoinOutputID) {
	bucket := w.tx.Bucket(append(prefixDSCO, encoding.Marshal(height)...))
	if bucket == nil {
		return nil
	}
	_ = bucket.ForEach(func(k, v []byte) error {
		var id ctypes.SiacoinOutputID
		copy(id[:], k)
		ids = append(ids, id)
		return nil
	})
	return
}

func (w coreStoreWrapper) MaturedSiacoinOutput(height uint64, id ctypes.SiacoinOutputID) (sco ctypes.SiacoinOutput, ok bool) {
	if b := w.tx.Bucket(append(prefixDSCO, encoding.Marshal(height)...)); b != nil {
		d := ctypes.NewBufDecoder(b.Get(id[:]))
		sco.DecodeFrom(d)
		ok = d.Err() == nil
	}
	return
}

func (w coreStoreWrapper) MissedFileContracts(height uint64) (ids []ctypes.FileContractID) {
	bucket := w.tx.Bucket(append(prefixFCEX, encoding.Marshal(height)...))
	if bucket == nil {
		return nil
	}
	_ = bucket.ForEach(func(k, v []byte) error {
		var id ctypes.FileContractID
		copy(id[:], k)
		ids = append(ids, id)
		return nil
	})
	return
}
