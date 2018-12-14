package renter

import (
	"fmt"
	"io"
	"os"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// StreamShard is a helper type that allows us to split an io.Reader up into
// multiple readers, wait for the shard to finish reading and then check the
// error for that Read.
// NOTE each shard should only be used for a single call to Read.
type StreamShard struct {
	err        error
	r          io.Reader
	signalChan chan struct{}
}

// NewStreamShard creates a new stream shard from a reader and an optional
// waitForChan.
func NewStreamShard(r io.Reader) *StreamShard {
	return &StreamShard{
		r:          r,
		signalChan: make(chan struct{}),
	}
}

// Read implements the io.Reader interface. It closes signalChan after Read
// returns.
func (ss *StreamShard) Read(b []byte) (int, error) {
	n, err := ss.Read(b)
	close(ss.signalChan)
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

	// Read the chunks we want to upload one by one from the input stream using
	// shards. A shard will signal completion after reading the input but
	// before the upload is done.
	for chunkIndex := uint64(0); ; chunkIndex++ {
		// Create a new shard.
		ss := NewStreamShard(reader)

		// Start the chunk upload.
		id := r.mu.Lock()
		uuc := r.buildUnfinishedChunk(entry, chunkIndex, hosts, pks)
		r.mu.Unlock(id)

		// Set the chunks source reader.
		uuc.sourceReader = ss

		// Add the chunk to the upload heap.
		r.uploadHeap.managedPush(uuc)

		// Notify the upload loop.
		select {
		case r.uploadHeap.newUploads <- struct{}{}:
		default:
		}

		// Wait for the shard to be read.
		select {
		case <-r.tg.StopChan():
			return errors.New("interrupted by shutdown")
		case <-ss.signalChan:
		}

		// If an io.EOF error occurred we are done. Otherwise we report the
		// error.
		if ss.err == io.EOF {
			return nil
		} else if ss.err != nil {
			return ss.err
		}
	}
}

// managedInitUploadStream  verifies hte upload parameters and prepares an empty
// SiaFile for the upload.
func (r *Renter) managedInitUploadStream(up modules.FileUploadParams) (*siafile.SiaFileSetEntry, error) {
	siaPath, ec, force := up.SiaPath, up.ErasureCode, up.Force

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
	// Check if directory exists already
	exists, err := r.staticDirSet.Exists(dirSiaPath)
	if !os.IsNotExist(err) && err != nil {
		return nil, err
	}
	if !exists {
		// Create directory
		siaDirEntry, err := r.staticDirSet.NewSiaDir(dirSiaPath)
		if err != nil {
			return nil, err
		}
		siaDirEntry.Close()
	}
	// Create the Siafile and add to renter
	sk := crypto.GenerateSiaKey(crypto.TypeDefaultRenter)
	entry, err := r.staticFileSet.NewSiaFile(up, sk, 0, 0700)
	if err != nil {
		return nil, err
	}
	defer entry.Close()

	return entry, nil
}
