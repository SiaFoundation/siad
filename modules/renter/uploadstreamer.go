package renter

import (
	"fmt"
	"os"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
)

type (
	uploadStreamer struct {
		staticFileEntry *siafile.SiaFileSetEntry
		r               *Renter
	}
)

func (us *uploadStreamer) Close() error {
	// TODO don't forget flushing the buffer.
	panic("not implemented yet")
}
func (us *uploadStreamer) Write(b []byte) (n int, err error) {
	panic("not implemented yet")

	//	// Send the upload to the repair loop.
	//	hosts := r.managedRefreshHostsAndWorkers()
	//	id := r.mu.Lock()
	//	unfinishedChunks := r.buildUnfinishedChunks(entry.ChunkEntrys(), hosts)
	//	r.mu.Unlock(id)
	//	for i := 0; i < len(unfinishedChunks); i++ {
	//		r.uploadHeap.managedPush(unfinishedChunks[i])
	//	}
	//	select {
	//	case r.uploadHeap.newUploads <- struct{}{}:
	//	default:
	//	}
	//	return nil, nil
}

// UploadStreamer creates a streamer that can be used to upload a file to Sia
// using a stream.
func (r *Renter) UploadStreamer(up modules.FileUploadParams) (modules.UploadStreamer, error) {
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
	entry, err := r.staticFileSet.NewSiaFile(up, crypto.GenerateSiaKey(crypto.TypeDefaultRenter), 0, 0700)
	if err != nil {
		return nil, err
	}
	defer entry.Close()

	return &uploadStreamer{
		staticFileEntry: entry,
		r:               r,
	}, nil
}
