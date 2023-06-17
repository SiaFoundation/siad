package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"gitlab.com/NebulousLabs/bolt"
	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
)

func main() {
	log.SetFlags(log.Lshortfile)
	if len(os.Args) != 2 {
		log.Fatal("Usage: siad-convert-db /path/to/consensus.db")
	}
	db, err := bolt.Open(os.Args[1], 0600, nil)
	if err != nil {
		log.Fatal(err)
	}
	network, genesisBlock := chain.Mainnet()
	start := time.Now()

	err = db.Update(func(btx *bolt.Tx) error {
		isCoreDB := btx.Bucket(bVersion) != nil
		isSiadDB := btx.Bucket(bBlockMap) != nil
		switch {
		case !isCoreDB && !isSiadDB:
			return errors.New("database is not a siad or core database")
		case isCoreDB && !isSiadDB:
			return errors.New("database already converted, exiting")
		case isCoreDB && isSiadDB:
			fmt.Println("Database is partially converted, resuming...")
			return nil
		case !isCoreDB && isSiadDB:
			fmt.Println("siad database detected, ready to convert.")
			fmt.Println("Once conversion begins, this database will no longer be usable by siad!")
			fmt.Print("Proceed? (y/n): ")
			var resp string
			if _, err := fmt.Scanln(&resp); err != nil {
				return err
			} else if resp != "y" {
				return errors.New("aborted")
			}

			// check genesis block
			genesisID := genesisBlock.ID()
			if btx.Bucket(bBlockMap).Get(genesisID[:]) == nil {
				return errors.New("siad database has different genesis block")
			}

			fmt.Println("Deleting unneeded siad buckets...")
			err := btx.ForEach(func(name []byte, _ *bolt.Bucket) error {
				if bytes.Equal(name, bBlockHeight) || bytes.Equal(name, bBlockMap) || bytes.Equal(name, bBlockPath) {
					return nil
				}
				return btx.DeleteBucket(name)
			})
			if err != nil {
				return err
			}

			fmt.Println("Creating core buckets and applying genesis block...")
			for _, bucket := range [][]byte{
				bVersion,
				bMainChain,
				bCheckpoints,
				bFileContracts,
				bSiacoinOutputs,
				bSiafundOutputs,
			} {
				if _, err := btx.CreateBucket(bucket); err != nil {
					return err
				}
			}
			tx := &dbTx{tx: btx, n: network}
			tx.bucket(bVersion).putRaw(bVersion, []byte{1})
			genesisState := network.GenesisState()
			cs := consensus.ApplyState(genesisState, tx, genesisBlock)
			diff := consensus.ApplyDiff(genesisState, tx, genesisBlock)
			tx.putCheckpoint(chain.Checkpoint{Block: genesisBlock, State: cs, Diff: &diff})
			tx.applyState(cs)
			tx.applyDiff(cs, diff)
			return tx.err
		}
		return nil
	})
	if err != nil {
		log.Fatalln("Initialization failed:", err)
	}

	const blocksPerDBTx = 1000
	var converted uint64
	for {
		var siadHeight, coreHeight uint64
		err := db.Update(func(btx *bolt.Tx) error {
			tx := &dbTx{tx: btx, n: network}
			coreHeight = tx.getHeight()
			siadHeight = tx.getSiadHeight()
			if coreHeight == siadHeight {
				return nil
			}
			stopHeight := coreHeight + blocksPerDBTx
			if stopHeight > siadHeight {
				stopHeight = siadHeight
			}
			return convertBlocks(tx, coreHeight, stopHeight)
		})
		if err != nil {
			log.Fatal(err)
		} else if coreHeight == siadHeight {
			break
		}
		converted += blocksPerDBTx
		elapsed := time.Since(start)
		rate := float64(converted) / elapsed.Seconds()
		rem := (elapsed * time.Duration(siadHeight-coreHeight)) / time.Duration(converted)
		fmt.Printf("\rConverted %v/%v blocks (%.2f/s), ETA %v (%v m)", coreHeight, siadHeight, rate, time.Now().Add(rem).Format(time.Kitchen), int(rem/time.Minute))
	}
	fmt.Println()

	// conversion complete; delete remaining buckets
	err = db.Update(func(btx *bolt.Tx) error {
		for _, bucket := range [][]byte{
			bBlockHeight,
			bBlockMap,
			bBlockPath,
		} {
			if err := btx.DeleteBucket(bucket); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Fatalln("Couldn't clean up remaining siad buckets:", err)
	}

	fmt.Println("Successfully converted database to core format in", time.Since(start))
}

func convertBlocks(tx *dbTx, curHeight, stopHeight uint64) error {
	var cs consensus.State
	if index, ok := tx.BestIndex(curHeight); !ok {
		return errors.New("core database is missing best index")
	} else if c, ok := tx.getCheckpoint(index.ID); !ok {
		return errors.New("core database is missing checkpoint")
	} else {
		cs = c.State
	}
	for curHeight < stopHeight && tx.err == nil {
		curHeight++
		b, ok := tx.getSiadBlock(curHeight)
		if !ok {
			tx.setErr(fmt.Errorf("missing siad block %v", curHeight))
			break
		}
		diff := consensus.ApplyDiff(cs, tx, b)
		cs = consensus.ApplyState(cs, tx, b)
		tx.putCheckpoint(chain.Checkpoint{Block: b, State: cs, Diff: &diff})
		tx.applyState(cs)
		tx.applyDiff(cs, diff)
		tx.deleteSiadBlock(curHeight)
	}
	return tx.err
}

var (
	// siad buckets (that we care about)
	bBlockHeight = []byte("BlockHeight")
	bBlockMap    = []byte("BlockMap")
	bBlockPath   = []byte("BlockPath")

	// core buckets
	bVersion        = []byte("Version")
	bMainChain      = []byte("MainChain")
	bCheckpoints    = []byte("Checkpoints")
	bFileContracts  = []byte("FileContracts")
	bSiacoinOutputs = []byte("SiacoinOutputs")
	bSiafundOutputs = []byte("SiafundOutputs")

	// core keys
	keyFoundationOutputs = []byte("FoundationOutputs")
	keyHeight            = []byte("Height")
)

type dbBucket struct {
	b  *bolt.Bucket
	tx *dbTx
}

func (b *dbBucket) getRaw(key []byte) []byte {
	if b == nil || b.tx.err != nil {
		return nil
	}
	return b.b.Get(key)
}

func (b *dbBucket) get(key []byte, v types.DecoderFrom) bool {
	val := b.getRaw(key)
	if val == nil || b.tx.err != nil {
		return false
	}
	d := types.NewBufDecoder(val)
	v.DecodeFrom(d)
	if d.Err() != nil {
		b.tx.setErr(fmt.Errorf("error decoding %T: %w", v, d.Err()))
		return false
	}
	return true
}

func (b *dbBucket) putRaw(key, value []byte) {
	if b == nil || b.tx.err != nil {
		return
	}
	b.tx.setErr(b.b.Put(key, value))
}

func (b *dbBucket) put(key []byte, v types.EncoderTo) {
	var buf bytes.Buffer
	e := types.NewEncoder(&buf)
	v.EncodeTo(e)
	e.Flush()
	b.putRaw(key, buf.Bytes())
}

func (b *dbBucket) delete(key []byte) {
	if b == nil || b.tx.err != nil {
		return
	}
	b.tx.setErr(b.b.Delete(key))
}

type dbTx struct {
	tx  *bolt.Tx
	n   *consensus.Network // for getCheckpoint
	err error
}

func (tx *dbTx) setErr(err error) {
	if tx.err == nil {
		tx.err = err
	}
}

func (tx *dbTx) bucket(name []byte) *dbBucket {
	if tx.err != nil {
		return nil
	}
	b := tx.tx.Bucket(name)
	if b == nil {
		return nil
	}
	return &dbBucket{b, tx}
}

// siad-specific methods

func (tx *dbTx) getSiadHeight() (height uint64) {
	return binary.LittleEndian.Uint64(tx.bucket(bBlockHeight).getRaw(bBlockHeight))
}

func (tx *dbTx) getSiadBlockID(height uint64) (id types.BlockID) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, height)
	tx.bucket(bBlockPath).get(buf, &id)
	return
}

func (tx *dbTx) getSiadBlock(height uint64) (b types.Block, ok bool) {
	id := tx.getSiadBlockID(height)
	ok = tx.bucket(bBlockMap).get(id[:], &b)
	return
}

func (tx *dbTx) deleteSiadBlock(height uint64) {
	id := tx.getSiadBlockID(height)
	tx.bucket(bBlockMap).delete(id[:])
}

// core methods

func (tx *dbTx) encHeight(height uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], height)
	return buf[:]
}

func (tx *dbTx) BestIndex(height uint64) (index types.ChainIndex, ok bool) {
	index.Height = height
	ok = tx.bucket(bMainChain).get(tx.encHeight(height), &index.ID)
	return
}

func (tx *dbTx) putBestIndex(index types.ChainIndex) {
	tx.bucket(bMainChain).put(tx.encHeight(index.Height), &index.ID)
}

func (tx *dbTx) getHeight() (height uint64) {
	if val := tx.bucket(bMainChain).getRaw(keyHeight); len(val) == 8 {
		height = binary.BigEndian.Uint64(val)
	}
	return
}

func (tx *dbTx) putHeight(height uint64) {
	tx.bucket(bMainChain).putRaw(keyHeight, tx.encHeight(height))
}

func (tx *dbTx) getCheckpoint(id types.BlockID) (c chain.Checkpoint, ok bool) {
	ok = tx.bucket(bCheckpoints).get(id[:], &c)
	c.State.Network = tx.n
	return
}

func (tx *dbTx) putCheckpoint(c chain.Checkpoint) {
	tx.bucket(bCheckpoints).put(c.State.Index.ID[:], c)
}

func (tx *dbTx) AncestorTimestamp(id types.BlockID, n uint64) time.Time {
	c, _ := tx.getCheckpoint(id)
	for i := uint64(1); i < n; i++ {
		// if we're on the best path, we can jump to the n'th block directly
		if index, _ := tx.BestIndex(c.State.Index.Height); index.ID == id {
			ancestorIndex, _ := tx.BestIndex(c.State.Index.Height - (n - i))
			c, _ = tx.getCheckpoint(ancestorIndex.ID)
			break
		}
		c, _ = tx.getCheckpoint(c.Block.ParentID)
	}
	return c.Block.Timestamp
}

func (tx *dbTx) SiacoinOutput(id types.SiacoinOutputID) (sco types.SiacoinOutput, ok bool) {
	ok = tx.bucket(bSiacoinOutputs).get(id[:], &sco)
	return
}

func (tx *dbTx) putSiacoinOutput(id types.SiacoinOutputID, sco types.SiacoinOutput) {
	tx.bucket(bSiacoinOutputs).put(id[:], sco)
}

func (tx *dbTx) deleteSiacoinOutput(id types.SiacoinOutputID) {
	tx.bucket(bSiacoinOutputs).delete(id[:])
}

func (tx *dbTx) FileContract(id types.FileContractID) (fc types.FileContract, ok bool) {
	ok = tx.bucket(bFileContracts).get(id[:], &fc)
	return
}

func (tx *dbTx) MissedFileContracts(height uint64) (fcids []types.FileContractID) {
	ids := tx.bucket(bFileContracts).getRaw(tx.encHeight(height))
	for i := 0; i < len(ids); i += 32 {
		var fcid types.FileContractID
		copy(fcid[:], ids[i:])
		fcids = append(fcids, fcid)
	}
	return
}

func (tx *dbTx) putFileContract(id types.FileContractID, fc types.FileContract) {
	b := tx.bucket(bFileContracts)
	b.put(id[:], fc)

	key := tx.encHeight(fc.WindowEnd)
	b.putRaw(key, append(b.getRaw(key), id[:]...))
}

func (tx *dbTx) reviseFileContract(id types.FileContractID, fc types.FileContract) {
	b := tx.bucket(bFileContracts)
	b.put(id[:], fc)
}

func (tx *dbTx) deleteFileContracts(fcds []consensus.FileContractDiff) {
	byHeight := make(map[uint64][]types.FileContractID)
	b := tx.bucket(bFileContracts)
	for _, fcd := range fcds {
		var fc types.FileContract
		if !b.get(fcd.ID[:], &fc) {
			tx.setErr(fmt.Errorf("missing file contract %v", fcd.ID))
		}
		b.delete(fcd.ID[:])
		byHeight[fc.WindowEnd] = append(byHeight[fc.WindowEnd], fcd.ID)
	}

	for height, ids := range byHeight {
		toDelete := make(map[types.FileContractID]struct{})
		for _, id := range ids {
			toDelete[id] = struct{}{}
		}
		key := tx.encHeight(height)
		val := append([]byte(nil), b.getRaw(key)...)
		for i := 0; i < len(val); i += 32 {
			var id types.FileContractID
			copy(id[:], val[i:])
			if _, ok := toDelete[id]; ok {
				copy(val[i:], val[len(val)-32:])
				val = val[:len(val)-32]
				i -= 32
				delete(toDelete, id)
			}
		}
		b.putRaw(key, val)
		if len(toDelete) != 0 {
			tx.setErr(errors.New("missing expired file contract(s)"))
		}
	}
}

type claimSFO struct {
	Output     types.SiafundOutput
	ClaimStart types.Currency
}

func (sfo claimSFO) EncodeTo(e *types.Encoder) {
	sfo.Output.EncodeTo(e)
	sfo.ClaimStart.EncodeTo(e)
}

func (sfo *claimSFO) DecodeFrom(d *types.Decoder) {
	sfo.Output.DecodeFrom(d)
	sfo.ClaimStart.DecodeFrom(d)
}

func (tx *dbTx) SiafundOutput(id types.SiafundOutputID) (sfo types.SiafundOutput, claimStart types.Currency, ok bool) {
	var csfo claimSFO
	ok = tx.bucket(bSiafundOutputs).get(id[:], &csfo)
	return csfo.Output, csfo.ClaimStart, ok
}

func (tx *dbTx) putSiafundOutput(id types.SiafundOutputID, sfo types.SiafundOutput, claimStart types.Currency) {
	tx.bucket(bSiafundOutputs).put(id[:], claimSFO{Output: sfo, ClaimStart: claimStart})
}

func (tx *dbTx) deleteSiafundOutput(id types.SiafundOutputID) {
	tx.bucket(bSiafundOutputs).delete(id[:])
}

func (tx *dbTx) MaturedSiacoinOutputs(height uint64) (dscods []consensus.DelayedSiacoinOutputDiff) {
	dscos := tx.bucket(bSiacoinOutputs).getRaw(tx.encHeight(height))
	d := types.NewBufDecoder(dscos)
	for {
		var dscod consensus.DelayedSiacoinOutputDiff
		dscod.DecodeFrom(d)
		if d.Err() != nil {
			break
		}
		dscods = append(dscods, dscod)
	}
	if !errors.Is(d.Err(), io.EOF) {
		tx.setErr(d.Err())
	}
	return
}

func (tx *dbTx) putDelayedSiacoinOutputs(dscods []consensus.DelayedSiacoinOutputDiff) {
	if len(dscods) == 0 {
		return
	}
	maturityHeight := dscods[0].MaturityHeight
	b := tx.bucket(bSiacoinOutputs)
	key := tx.encHeight(maturityHeight)
	var buf bytes.Buffer
	e := types.NewEncoder(&buf)
	for _, dscod := range dscods {
		if dscod.MaturityHeight != maturityHeight {
			tx.setErr(errors.New("mismatched maturity heights"))
			return
		}
		dscod.EncodeTo(e)
	}
	e.Flush()
	b.putRaw(key, append(b.getRaw(key), buf.Bytes()[:]...))
}

func (tx *dbTx) deleteDelayedSiacoinOutputs(dscods []consensus.DelayedSiacoinOutputDiff) {
	if len(dscods) == 0 {
		return
	}
	maturityHeight := dscods[0].MaturityHeight
	toDelete := make(map[types.SiacoinOutputID]struct{})
	for _, dscod := range dscods {
		if dscod.MaturityHeight != maturityHeight {
			tx.setErr(errors.New("mismatched maturity heights"))
			return
		}
		toDelete[dscod.ID] = struct{}{}
	}
	var buf bytes.Buffer
	e := types.NewEncoder(&buf)
	for _, mdscod := range tx.MaturedSiacoinOutputs(maturityHeight) {
		if _, ok := toDelete[mdscod.ID]; !ok {
			mdscod.EncodeTo(e)
		}
		delete(toDelete, mdscod.ID)
	}
	if len(toDelete) != 0 {
		tx.setErr(errors.New("missing delayed siacoin output(s)"))
		return
	}
	e.Flush()
	tx.bucket(bSiacoinOutputs).putRaw(tx.encHeight(maturityHeight), buf.Bytes())
}

func (tx *dbTx) putFoundationOutput(id types.SiacoinOutputID) {
	b := tx.bucket(bSiacoinOutputs)
	b.putRaw(keyFoundationOutputs, append(b.getRaw(keyFoundationOutputs), id[:]...))
}

func (tx *dbTx) moveFoundationOutputs(addr types.Address) {
	ids := tx.bucket(bSiacoinOutputs).getRaw(keyFoundationOutputs)
	for i := 0; i < len(ids); i += 32 {
		var id types.SiacoinOutputID
		copy(id[:], ids[i:])
		if sco, ok := tx.SiacoinOutput(id); ok {
			if sco.Address == addr {
				return // address unchanged; no migration necessary
			}
			sco.Address = addr
			tx.putSiacoinOutput(id, sco)
		}
	}
}

func (tx *dbTx) applyState(next consensus.State) {
	tx.moveFoundationOutputs(next.FoundationPrimaryAddress)
	tx.putBestIndex(next.Index)
	tx.putHeight(next.Index.Height)
}

func (tx *dbTx) applyDiff(s consensus.State, diff consensus.BlockDiff) {
	for _, td := range diff.Transactions {
		for _, scod := range td.CreatedSiacoinOutputs {
			tx.putSiacoinOutput(scod.ID, scod.Output)
		}
		tx.putDelayedSiacoinOutputs(td.ImmatureSiacoinOutputs)
		for _, sfod := range td.CreatedSiafundOutputs {
			tx.putSiafundOutput(sfod.ID, sfod.Output, sfod.ClaimStart)
		}
		for _, fcd := range td.CreatedFileContracts {
			tx.putFileContract(fcd.ID, fcd.Contract)
		}
		for _, scod := range td.SpentSiacoinOutputs {
			tx.deleteSiacoinOutput(scod.ID)
		}
		for _, sfod := range td.SpentSiafundOutputs {
			tx.deleteSiafundOutput(sfod.ID)
		}
		for _, fcrd := range td.RevisedFileContracts {
			tx.reviseFileContract(fcrd.ID, fcrd.NewContract)
		}
		tx.deleteFileContracts(td.ValidFileContracts)
	}
	tx.putDelayedSiacoinOutputs(diff.ImmatureSiacoinOutputs)
	for _, dscod := range diff.ImmatureSiacoinOutputs {
		if dscod.Source == consensus.OutputSourceFoundation {
			tx.putFoundationOutput(dscod.ID)
		}
	}
	tx.deleteDelayedSiacoinOutputs(diff.MaturedSiacoinOutputs)
	for _, scod := range diff.MaturedSiacoinOutputs {
		tx.putSiacoinOutput(scod.ID, scod.Output)
	}
	tx.deleteFileContracts(diff.MissedFileContracts)
}
