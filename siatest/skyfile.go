package siatest

import (
	"bytes"
	"fmt"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// Skyfile returns the file at given path
func (tn *TestNode) Skyfile(path modules.SiaPath) (modules.FileInfo, error) {
	rfile, err := tn.RenterFileRootGet(path)
	if err != nil {
		return rfile.File, err
	}
	return rfile.File, err
}

// UploadNewSkyfileBlocking attempts to upload a skyfile of given size. After it
// has successfully performed the upload, it will verify the file can be
// downloaded using its Skylink. Returns the skylink, the parameters used for
// the upload and potentially an error.
func (tn *TestNode) UploadNewSkyfileBlocking(filename string, filesize uint64, force bool) (skylink string, sup modules.SkyfileUploadParameters, sshp api.SkynetSkyfileHandlerPOST, err error) {
	// create the siapath
	skyfilePath, err := modules.NewSiaPath(filename)
	if err != nil {
		err = errors.AddContext(err, "Failed to create siapath")
		return
	}

	// create random data and wrap it in a reader
	data := fastrand.Bytes(int(filesize))
	reader := bytes.NewReader(data)
	sup = modules.SkyfileUploadParameters{
		SiaPath:             skyfilePath,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.SkyfileMetadata{
			Filename: filename,
			Mode:     modules.DefaultFilePerm,
		},
		Reader: reader,
		Force:  force,
		Root:   false,
	}

	// upload a skyfile
	skylink, sshp, err = tn.SkynetSkyfilePost(sup)
	if err != nil {
		err = errors.AddContext(err, "Failed to upload skyfile")
		return
	}

	// rebase the siapath if necessary
	if !sup.Root {
		skyfilePath, err = modules.SkynetFolder.Join(skyfilePath.String())
		if err != nil {
			err = errors.AddContext(err, "Failed to create siapath")
			return
		}
	}

	// wait until upload reached the specified redundancy
	if err = tn.WaitForSkyfileRedundancy(skyfilePath, 2); err != nil {
		err = errors.AddContext(err, "Skyfile upload not complete, redundancy did not reach a value of 2")
		return
	}

	return
}

// WaitForSkyfileRedundancy waits until the file at given path reaches the given
// redundancy threshold. Note that we specify the given path must be the path of
// a Skyfile because we call `tn.Skyfile` and not `tn.File`.
func (tn *TestNode) WaitForSkyfileRedundancy(path modules.SiaPath, redundancy float64) error {
	// Check if file is tracked by renter at all
	if _, err := tn.Skyfile(path); err != nil {
		return ErrFileNotTracked
	}
	// Wait until it reaches the redundancy
	return Retry(1000, 100*time.Millisecond, func() error {
		file, err := tn.Skyfile(path)
		if err != nil {
			return ErrFileNotTracked
		}
		if file.Redundancy < redundancy {
			return fmt.Errorf("redundancy should be %v but was %v", redundancy, file.Redundancy)
		}
		return nil
	})
}
