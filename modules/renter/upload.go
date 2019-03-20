package renter

// upload.go performs basic preprocessing on upload requests and then adds the
// requested files into the repair heap.
//
// TODO: Currently you cannot upload a directory using the api, if you want to
// upload a directory you must make 1 api call per file in that directory.
// Perhaps we should extend this endpoint to be able to recursively add files in
// a directory?
//
// TODO: Currently the minimum contracts check is not enforced while testing,
// which means that code is not covered at all. Enabling enforcement during
// testing will probably break a ton of existing tests, which means they will
// all need to be fixed when we do enable it, but we should enable it.

import (
	"fmt"
	"os"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

var (
	// errUploadDirectory is returned if the user tries to upload a directory.
	errUploadDirectory = errors.New("cannot upload directory")
)

// validateSource verifies that a sourcePath meets the
// requirements for upload.
func validateSource(sourcePath string) error {
	// Check for read access
	file, err := os.Open(sourcePath)
	if err != nil {
		return errors.AddContext(err, "unable to open the source file")
	}
	file.Close()

	finfo, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	if finfo.IsDir() {
		return errUploadDirectory
	}

	return nil
}

// Upload instructs the renter to start tracking a file. The renter will
// automatically upload and repair tracked files using a background loop.
func (r *Renter) Upload(up modules.FileUploadParams) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	// Enforce source rules.
	if err := validateSource(up.Source); err != nil {
		return err
	}

	// Delete existing file if overwrite flag is set. Ignore ErrUnknownPath.
	if up.Force {
		if err := r.DeleteFile(up.SiaPath); err != nil && err != siafile.ErrUnknownPath {
			return err
		}
	}

	// Fill in any missing upload params with sensible defaults.
	fileInfo, err := os.Stat(up.Source)
	if err != nil {
		return err
	}
	if up.ErasureCode == nil {
		up.ErasureCode, _ = siafile.NewRSSubCode(defaultDataPieces, defaultParityPieces, 64)
	}

	// Check that we have contracts to upload to. We need at least data +
	// parity/2 contracts. NumPieces is equal to data+parity, and min pieces is
	// equal to parity. Therefore (NumPieces+MinPieces)/2 = (data+data+parity)/2
	// = data+parity/2.
	numContracts := len(r.hostContractor.Contracts())
	requiredContracts := (up.ErasureCode.NumPieces() + up.ErasureCode.MinPieces()) / 2
	if numContracts < requiredContracts && build.Release != "testing" {
		return fmt.Errorf("not enough contracts to upload file: got %v, needed %v", numContracts, (up.ErasureCode.NumPieces()+up.ErasureCode.MinPieces())/2)
	}

	// Create the directory path on disk. Renter directory is already present so
	// only files not in top level directory need to have directories created
	dirSiaPath := up.SiaPath.Dir()
	if dirSiaPath.ToString() == "." {
		dirSiaPath = types.RootDirSiaPath()
	}
	// Try to create the directory. If ErrPathOverload is returned it already exists.
	siaDirEntry, err := r.staticDirSet.NewSiaDir(dirSiaPath)
	if err != siadir.ErrPathOverload && err != nil {
		return err
	} else if err == nil {
		siaDirEntry.Close()
	}

	// Create the Siafile and add to renter
	entry, err := r.staticFileSet.NewSiaFile(up, crypto.GenerateSiaKey(crypto.TypeDefaultRenter), uint64(fileInfo.Size()), fileInfo.Mode())
	if err != nil {
		return err
	}
	defer entry.Close()

	// No need to upload zero-byte files.
	if fileInfo.Size() == 0 {
		return nil
	}

	// Bubble the health of the SiaFile directory to ensure the health is
	// updated with the new file
	go r.threadedBubbleMetadata(dirSiaPath)

	// Create nil maps for offline and goodForRenew to pass in to
	// managedBuildAndPushChunks. These maps are used to determine the health of
	// the file and its chunks. Nil maps will result in the file and its chunks
	// having the worst possible health which is accurate since the file hasn't
	// been uploaded yet
	nilMap := make(map[string]bool)
	// Send the upload to the repair loop.
	hosts := r.managedRefreshHostsAndWorkers()
	r.managedBuildAndPushChunks([]*siafile.SiaFileSetEntry{entry}, hosts, targetUnstuckChunks, nilMap, nilMap)
	select {
	case r.uploadHeap.newUploads <- struct{}{}:
	default:
	}
	return nil
}
