package chainutil

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/merkle"
	"go.sia.tech/core/types"
)

// EphemeralStore implements chain.ManagerStore in memory.
type EphemeralStore struct {
	entries map[types.ChainIndex]consensus.Checkpoint
	best    []types.ChainIndex
}

// AddCheckpoint implements chain.ManagerStore.
func (es *EphemeralStore) AddCheckpoint(c consensus.Checkpoint) error {
	es.entries[c.State.Index] = c
	return nil
}

// Checkpoint implements chain.ManagerStore.
func (es *EphemeralStore) Checkpoint(index types.ChainIndex) (consensus.Checkpoint, error) {
	e, ok := es.entries[index]
	if !ok {
		return consensus.Checkpoint{}, chain.ErrUnknownIndex
	}
	return e, nil
}

// Header implements chain.ManagerStore.
func (es *EphemeralStore) Header(index types.ChainIndex) (types.BlockHeader, error) {
	c, err := es.Checkpoint(index)
	return c.Block.Header, err
}

// ExtendBest implements chain.ManagerStore.
func (es *EphemeralStore) ExtendBest(index types.ChainIndex) error {
	if _, ok := es.entries[index]; !ok {
		panic("no entry for index")
	}
	es.best = append(es.best, index)
	return nil
}

// RewindBest implements chain.ManagerStore.
func (es *EphemeralStore) RewindBest() error {
	es.best = es.best[:len(es.best)-1]
	return nil
}

// BestIndex implements chain.ManagerStore.
func (es *EphemeralStore) BestIndex(height uint64) (types.ChainIndex, error) {
	baseHeight, tipHeight := es.best[0].Height, es.best[len(es.best)-1].Height
	if !(baseHeight <= height && height <= tipHeight) {
		return types.ChainIndex{}, chain.ErrUnknownIndex
	}
	return es.best[height-baseHeight], nil
}

// Flush implements chain.ManagerStore.
func (es *EphemeralStore) Flush() error { return nil }

// Close implements chain.ManagerStore.
func (es *EphemeralStore) Close() error { return nil }

// NewEphemeralStore returns an in-memory chain.ManagerStore.
func NewEphemeralStore(c consensus.Checkpoint) *EphemeralStore {
	return &EphemeralStore{
		entries: map[types.ChainIndex]consensus.Checkpoint{c.State.Index: c},
		best:    []types.ChainIndex{c.State.Index},
	}
}

type metadata struct {
	indexSize int64
	entrySize int64
	tip       types.ChainIndex
}

// FlatStore implements chain.ManagerStore with persistent files.
type FlatStore struct {
	indexFile *os.File
	entryFile *os.File
	bestFile  *os.File

	meta     metadata
	metapath string

	base    types.ChainIndex
	offsets map[types.ChainIndex]int64
}

// AddCheckpoint implements chain.ManagerStore.
func (fs *FlatStore) AddCheckpoint(c consensus.Checkpoint) error {
	offset, err := fs.entryFile.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}
	if err := writeCheckpoint(fs.entryFile, c); err != nil {
		return fmt.Errorf("failed to write checkpoint: %w", err)
	} else if err := writeIndex(fs.indexFile, c.State.Index, offset); err != nil {
		return fmt.Errorf("failed to write index: %w", err)
	}
	stat, err := fs.entryFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}
	fs.offsets[c.State.Index] = offset
	fs.meta.entrySize = stat.Size()
	fs.meta.indexSize += indexSize
	return nil
}

// Checkpoint implements chain.ManagerStore.
func (fs *FlatStore) Checkpoint(index types.ChainIndex) (c consensus.Checkpoint, err error) {
	if offset, ok := fs.offsets[index]; !ok {
		return consensus.Checkpoint{}, chain.ErrUnknownIndex
	} else if _, err := fs.entryFile.Seek(offset, io.SeekStart); err != nil {
		return consensus.Checkpoint{}, fmt.Errorf("failed to seek entry file: %w", err)
	}
	err = readCheckpoint(bufio.NewReader(fs.entryFile), &c)
	if err != nil {
		return consensus.Checkpoint{}, fmt.Errorf("failed to read checkpoint: %w", err)
	}
	return
}

// Header implements chain.ManagerStore.
func (fs *FlatStore) Header(index types.ChainIndex) (types.BlockHeader, error) {
	b := make([]byte, 8+32+8+8+32+32)
	if offset, ok := fs.offsets[index]; !ok {
		return types.BlockHeader{}, chain.ErrUnknownIndex
	} else if _, err := fs.entryFile.ReadAt(b, offset); err != nil {
		return types.BlockHeader{}, fmt.Errorf("failed to read header at offset %v: %w", offset, err)
	}
	d := types.NewBufDecoder(b)
	var h types.BlockHeader
	h.DecodeFrom(d)
	if err := d.Err(); err != nil {
		return types.BlockHeader{}, fmt.Errorf("failed to decode header: %w", err)
	}
	return h, nil
}

// ExtendBest implements chain.ManagerStore.
func (fs *FlatStore) ExtendBest(index types.ChainIndex) error {
	if err := writeBest(fs.bestFile, index); err != nil {
		return fmt.Errorf("failed to write to store: %w", err)
	}
	fs.meta.tip = index
	return nil
}

// RewindBest implements chain.ManagerStore.
func (fs *FlatStore) RewindBest() error {
	index, err := fs.BestIndex(fs.meta.tip.Height - 1)
	if err != nil {
		return fmt.Errorf("failed to get parent index %v: %w", fs.meta.tip.Height-1, err)
	} else if off, err := fs.bestFile.Seek(-bestSize, io.SeekEnd); err != nil {
		return fmt.Errorf("failed to seek best file: %w", err)
	} else if err := fs.bestFile.Truncate(off); err != nil {
		return fmt.Errorf("failed to truncate file: %w", err)
	}
	fs.meta.tip = index
	return nil
}

// BestIndex implements chain.ManagerStore.
func (fs *FlatStore) BestIndex(height uint64) (index types.ChainIndex, err error) {
	if height < fs.base.Height {
		return types.ChainIndex{}, chain.ErrPruned
	}
	offset := int64(height-fs.base.Height) * bestSize
	buf := make([]byte, bestSize)
	if _, err = fs.bestFile.ReadAt(buf, offset); err == io.EOF {
		err = chain.ErrUnknownIndex
		return
	}

	d := types.NewBufDecoder(buf)
	index.DecodeFrom(d)
	if err = d.Err(); err != nil {
		err = fmt.Errorf("failed to decode index: %w", err)
		return
	}
	return index, nil
}

// Flush implements chain.ManagerStore.
func (fs *FlatStore) Flush() error {
	// TODO: also sync parent directory?
	if err := fs.indexFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync index file: %w", err)
	} else if err := fs.entryFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync entry file: %w", err)
	} else if err := fs.bestFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync best file: %w", err)
	}

	// atomically update metafile
	f, err := os.OpenFile(fs.metapath+"_tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0660)
	if err != nil {
		return fmt.Errorf("failed to open tmp file: %w", err)
	}
	defer f.Close()
	if err := writeMeta(f, fs.meta); err != nil {
		return fmt.Errorf("failed to write meta tmp: %w", err)
	} else if f.Sync(); err != nil {
		return fmt.Errorf("failed to sync meta tmp: %w", err)
	} else if f.Close(); err != nil {
		return fmt.Errorf("failed to close meta tmp: %w", err)
	} else if err := os.Rename(fs.metapath+"_tmp", fs.metapath); err != nil {
		return fmt.Errorf("failed to rename meta tmp: %w", err)
	}

	return nil
}

func (fs *FlatStore) recoverBest(tip types.ChainIndex) error {
	// if the store is empty, wipe the bestFile too
	if len(fs.offsets) == 0 {
		if err := fs.bestFile.Truncate(0); err != nil {
			return fmt.Errorf("failed to truncate best file: %w", err)
		}
		return nil
	}

	// truncate to multiple of bestSize
	if stat, err := fs.bestFile.Stat(); err != nil {
		return fmt.Errorf("failed to stat best file: %w", err)
	} else if n := stat.Size() / bestSize; n%bestSize != 0 {
		if err := fs.bestFile.Truncate(n * bestSize); err != nil {
			return fmt.Errorf("failed to truncate best file: %w", err)
		}
	}

	// initialize base
	base, err := readBest(fs.bestFile)
	if err != nil {
		return fmt.Errorf("failed to initialize best index: %w", err)
	}
	fs.base = base

	// recover best chain by reading parents of tip, stopping when the index is
	// also in bestFile
	index := tip
	var path []types.ChainIndex
	for {
		if bestIndex, err := fs.BestIndex(index.Height); err != nil && !errors.Is(err, chain.ErrUnknownIndex) {
			return fmt.Errorf("failed to get index at %v: %w", index.Height, err)
		} else if err == nil {
			return nil
		} else if bestIndex == index {
			break
		}
		path = append(path, index)
		h, err := fs.Header(index)
		if err != nil {
			return fmt.Errorf("failed to get block header %v: %w", index, err)
		}
		index = h.ParentIndex()
	}
	// truncate and extend
	if err := fs.bestFile.Truncate(int64(index.Height-base.Height) * bestSize); err != nil {
		return fmt.Errorf("failed to truncate best file (%v - %v): %w", index.Height, base.Height, err)
	}
	for i := len(path) - 1; i >= 0; i-- {
		if err := fs.ExtendBest(path[i]); err != nil {
			return fmt.Errorf("failed to extend best file %v: %w", path[i], err)
		}
	}

	return nil
}

// Close closes the store.
func (fs *FlatStore) Close() (err error) {
	errs := []error{
		fmt.Errorf("error flushing store: %w", fs.Flush()),
		fmt.Errorf("error closing index file: %w", fs.indexFile.Close()),
		fmt.Errorf("error closing entry file: %w", fs.entryFile.Close()),
		fmt.Errorf("error closing best file: %w", fs.bestFile.Close()),
	}
	for _, err := range errs {
		if errors.Unwrap(err) != nil {
			return err
		}
	}
	return nil
}

// NewFlatStore returns a FlatStore that stores data in the specified dir.
func NewFlatStore(dir string, c consensus.Checkpoint) (*FlatStore, consensus.Checkpoint, error) {
	indexFile, err := os.OpenFile(filepath.Join(dir, "index.dat"), os.O_CREATE|os.O_RDWR, 0o660)
	if err != nil {
		return nil, consensus.Checkpoint{}, fmt.Errorf("unable to open index file: %w", err)
	}
	entryFile, err := os.OpenFile(filepath.Join(dir, "entry.dat"), os.O_CREATE|os.O_RDWR, 0o660)
	if err != nil {
		return nil, consensus.Checkpoint{}, fmt.Errorf("unable to open entry file: %w", err)
	}
	bestFile, err := os.OpenFile(filepath.Join(dir, "best.dat"), os.O_CREATE|os.O_RDWR, 0o660)
	if err != nil {
		return nil, consensus.Checkpoint{}, fmt.Errorf("unable to open best file: %w", err)
	}

	// trim indexFile and entryFile according to metadata
	metapath := filepath.Join(dir, "meta.dat")
	meta, err := readMetaFile(metapath)
	if errors.Is(err, os.ErrNotExist) {
		// initial metadata
		meta = metadata{tip: c.State.Index}
	} else if err != nil {
		return nil, consensus.Checkpoint{}, fmt.Errorf("unable to read meta file %s: %w", metapath, err)
	} else if err := indexFile.Truncate(meta.indexSize); err != nil {
		return nil, consensus.Checkpoint{}, fmt.Errorf("failed to truncate meta index: %w", err)
	} else if err := entryFile.Truncate(meta.entrySize); err != nil {
		return nil, consensus.Checkpoint{}, fmt.Errorf("failed to truncate meta entrry: %w", err)
	}

	// read index entries into map
	offsets := make(map[types.ChainIndex]int64)
	for {
		index, offset, err := readIndex(indexFile)
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, consensus.Checkpoint{}, fmt.Errorf("failed to read index: %w", err)
		}
		offsets[index] = offset
	}

	fs := &FlatStore{
		indexFile: indexFile,
		entryFile: entryFile,
		bestFile:  bestFile,

		meta:     meta,
		metapath: metapath,

		base:    c.State.Index,
		offsets: offsets,
	}

	// recover bestFile, if necessary
	if err := fs.recoverBest(meta.tip); err != nil {
		return nil, consensus.Checkpoint{}, fmt.Errorf("unable to recover best at %v: %w", meta.tip, err)
	}
	if _, err := fs.bestFile.Seek(0, io.SeekEnd); err != nil {
		return nil, consensus.Checkpoint{}, fmt.Errorf("unable to seek to end of best file: %w", err)
	}

	// if store is empty, write base entry
	if len(fs.offsets) == 0 {
		if err := fs.AddCheckpoint(c); err != nil {
			return nil, consensus.Checkpoint{}, fmt.Errorf("unable to write checkpoint for %v: %w", c.State.Index, err)
		} else if err := fs.ExtendBest(c.State.Index); err != nil {
			return nil, consensus.Checkpoint{}, fmt.Errorf("failed to extend best for %v: %w", c.State.Index, err)
		}
		return fs, c, nil
	}

	c, err = fs.Checkpoint(meta.tip)
	if err != nil {
		return nil, consensus.Checkpoint{}, fmt.Errorf("unable to get checkpoint %v: %w", meta.tip, err)
	}
	return fs, c, nil
}

const (
	bestSize  = 40
	indexSize = 48
	metaSize  = 56
)

func bufferedDecoder(r io.Reader, size int) (*types.Decoder, error) {
	buf := make([]byte, size)
	_, err := io.ReadFull(r, buf)
	return types.NewBufDecoder(buf), err
}

func writeMeta(w io.Writer, meta metadata) error {
	e := types.NewEncoder(w)
	e.WriteUint64(uint64(meta.indexSize))
	e.WriteUint64(uint64(meta.entrySize))
	meta.tip.EncodeTo(e)
	return e.Flush()
}

func readMeta(r io.Reader) (meta metadata, err error) {
	d, err := bufferedDecoder(r, metaSize)
	meta.indexSize = int64(d.ReadUint64())
	meta.entrySize = int64(d.ReadUint64())
	meta.tip.DecodeFrom(d)
	return
}

func readMetaFile(path string) (meta metadata, err error) {
	f, err := os.Open(path)
	if err != nil {
		return metadata{}, fmt.Errorf("unable to open metafile %s: %w", path, err)
	}
	defer f.Close()
	meta, err = readMeta(f)
	if err != nil {
		err = fmt.Errorf("unable to read meta file %s: %w", path, err)
	}
	return
}

func writeBest(w io.Writer, index types.ChainIndex) error {
	e := types.NewEncoder(w)
	index.EncodeTo(e)
	return e.Flush()
}

func readBest(r io.Reader) (index types.ChainIndex, err error) {
	d, err := bufferedDecoder(r, bestSize)
	index.DecodeFrom(d)
	return
}

func writeIndex(w io.Writer, index types.ChainIndex, offset int64) error {
	e := types.NewEncoder(w)
	index.EncodeTo(e)
	e.WriteUint64(uint64(offset))
	return e.Flush()
}

func readIndex(r io.Reader) (index types.ChainIndex, offset int64, err error) {
	d, err := bufferedDecoder(r, indexSize)
	index.DecodeFrom(d)
	offset = int64(d.ReadUint64())
	return
}

func writeCheckpoint(w io.Writer, c consensus.Checkpoint) error {
	e := types.NewEncoder(w)
	(merkle.CompressedBlock)(c.Block).EncodeTo(e)
	c.State.EncodeTo(e)
	return e.Flush()
}

func readCheckpoint(r io.Reader, c *consensus.Checkpoint) error {
	d := types.NewDecoder(io.LimitedReader{
		R: r,
		N: 10e6, // a checkpoint should never be anywhere near this large
	})
	(*merkle.CompressedBlock)(&c.Block).DecodeFrom(d)
	c.State.DecodeFrom(d)
	return d.Err()
}
