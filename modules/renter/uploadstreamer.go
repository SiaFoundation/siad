package renter

import (
	"fmt"
	"io"
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// Upload Streaming Overview:
// Most of the logic that enables upload streaming can be found within
// UploadStreamFromReader and the StreamShard. As seen at the beginning of the
// big for - loop in UploadStreamFromReader, the streamer currently always
// assumes that the data provided by the user starts at index 0 of chunk 0. In
// every iteration the siafile is grown by a single chunk to prepare for the
// upload of the next chunk. To allow the upload code to repair a chunk from a
// stream, the stream is passed into the unfinished chunk as a new field. If the
// upload code detects a stream, it will use that instead of a local file to
// fetch the chunk's logical data. As soon as the upload code is done fetching
// the logical data, it will close that streamer to signal the loop that it's
// save to upload another chunk.
// This is possible due to the custom StreamShard type which is a wrapper for a
// io.Reader with a channel which is closed when the StreamShard is closed.

// StreamShard is a helper type that allows us to split an io.Reader up into
// multiple readers, wait for the shard to finish reading and then check the
// error for that Read.
type StreamShard struct {
	n   int
	err error

	r io.Reader

	closed     bool
	mu         sync.Mutex
	signalChan chan struct{}
}

// NewStreamShard creates a new stream shard from a reader.
func NewStreamShard(r io.Reader) *StreamShard {
	return &StreamShard{
		r:          r,
		signalChan: make(chan struct{}),
	}
}

// Close closes the underlying channel of the shard.
func (ss *StreamShard) Close() error {
	close(ss.signalChan)
	ss.closed = true
	return nil
}

// Result returns the returned values of calling Read on the shard.
func (ss *StreamShard) Result() (int, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.n, ss.err
}

// Read implements the io.Reader interface. It closes signalChan after Read
// returns.
func (ss *StreamShard) Read(b []byte) (int, error) {
	if ss.closed {
		return 0, errors.New("StreamShard already closed")
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	n, err := ss.r.Read(b)
	ss.n += n
	ss.err = err
	return n, err
}

// UploadStreamFromReader reads from the provided reader until io.EOF is reached and
// upload the data to the Sia network.
func (r *Renter) UploadStreamFromReader(up modules.FileUploadParams, reader io.Reader) error {
	// Check the upload params first.
	entry, err := r.managedInitUploadStream(up)
	if err != nil {
		return err
	}
	defer entry.Close()

	// Build a map of host public keys.
	pks := make(map[string]types.SiaPublicKey)
	for _, pk := range entry.HostPublicKeys() {
		pks[string(pk.Key)] = pk
	}

	// Get the most recent workers.
	hosts := r.managedRefreshHostsAndWorkers()

	// Check if we currently have enough workers for the specified redundancy.
	minWorkers := entry.ErasureCode().MinPieces()
	id := r.mu.RLock()
	availableWorkers := len(r.workerPool)
	r.mu.RUnlock(id)
	if availableWorkers < minWorkers {
		return fmt.Errorf("Need at least %v workers for upload but got only %v",
			minWorkers, availableWorkers)
	}

	// Read the chunks we want to upload one by one from the input stream using
	// shards. A shard will signal completion after reading the input but
	// before the upload is done.
	for chunkIndex := uint64(0); ; chunkIndex++ {
		// Grow the SiaFile to the right size. Otherwise buildUnfinishedChunk
		// won't realize that there are pieces which haven't been repaired yet.
		if err := entry.SiaFile.GrowNumChunks(chunkIndex + 1); err != nil {
			return err
		}

		// Start the chunk upload.
		id := r.mu.Lock()
		uuc := r.buildUnfinishedChunk(entry, chunkIndex, hosts, pks, true)
		r.mu.Unlock(id)

		// Create a new shard set it to be the source reader of the chunk.
		ss := NewStreamShard(reader)
		uuc.sourceReader = ss

		// Check if the chunk needs any work or if we can skip it.
		if uuc.piecesCompleted < uuc.piecesNeeded {
			// Add the chunk to the upload heap.
			if !r.uploadHeap.managedPush(uuc) {
				return fmt.Errorf("failed to push chunk with idx %v of stream to uploadheap", chunkIndex)
			}
			// Notify the upload loop.
			select {
			case r.uploadHeap.newUploads <- struct{}{}:
			default:
			}
		} else {
			// The chunk doesn't need any work. We still need to read a chunk
			// from the shard though. Otherwise we will upload the wrong chunk
			// for the next chunkIndex. We don't need to check the error though
			// since we check that anyway at the end of the loop.
			_, _ = io.ReadFull(ss, make([]byte, entry.ChunkSize()))
		}
		// Wait for the shard to be read.
		select {
		case <-r.tg.StopChan():
			return errors.New("interrupted by shutdown")
		case <-ss.signalChan:
		}

		// If an io.EOF error occurred or less than chunkSize was read, we are
		// done. Otherwise we report the error.
		if _, err := ss.Result(); err == io.EOF {
			// Adjust the fileSize
			return nil
		} else if ss.err != nil {
			return ss.err
		}
	}
}

// managedInitUploadStream  verifies the upload parameters and prepares an empty
// SiaFile for the upload.
func (r *Renter) managedInitUploadStream(up modules.FileUploadParams) (*siafile.SiaFileSetEntry, error) {
	siaPath, ec, force := up.SiaPath, up.ErasureCode, up.Force
	// Check if ec was set. If not use defaults.
	var err error
	if ec == nil {
		ec, err = siafile.NewRSSubCode(defaultDataPieces, defaultParityPieces, 64)
		if err != nil {
			return nil, err
		}
	}

	// Delete existing file if overwrite flag is set. Ignore ErrUnknownPath.
	if force {
		if err := r.DeleteFile(siaPath); err != nil && err != siafile.ErrUnknownPath {
			return nil, err
		}
	}
	// Check that we have contracts to upload to. We need at least data +
	// parity/2 contracts. NumPieces is equal to data+parity, and min pieces is
	// equal to parity. Therefore (NumPieces+MinPieces)/2 = (data+data+parity)/2
	// = data+parity/2.
	numContracts := len(r.hostContractor.Contracts())
	requiredContracts := (ec.NumPieces() + ec.MinPieces()) / 2
	if numContracts < requiredContracts && build.Release != "testing" {
		return nil, fmt.Errorf("not enough contracts to upload file: got %v, needed %v", numContracts, (ec.NumPieces()+ec.MinPieces())/2)
	}
	// Create the directory path on disk. Renter directory is already present so
	// only files not in top level directory need to have directories created
	dirSiaPath, err := siaPath.Dir()
	if err != nil {
		return nil, err
	}
	// Create directory
	siaDirEntry, err := r.staticDirSet.NewSiaDir(dirSiaPath)
	if err != nil && err != siadir.ErrPathOverload {
		return nil, err
	}
	defer siaDirEntry.Close()
	// Create the Siafile and add to renter
	sk := crypto.GenerateSiaKey(crypto.TypeDefaultRenter)
	entry, err := r.staticFileSet.NewSiaFile(up, sk, 0, 0700)
	if err != nil {
		return nil, err
	}
	return entry, nil
}
