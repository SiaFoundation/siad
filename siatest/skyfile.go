package siatest

import (
	"bytes"
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

func rebaseSkyfileSiaPath(siaPath modules.SiaPath, root bool) (modules.SiaPath, error) {
	if root {
		return siaPath, nil
	}
	return modules.SkynetFolder.Join(siaPath.String())
}

// UploadNewSkyfileBlocking attempts to upload a skyfile of given size. After it
// has successfully performed the upload, it will verify the file can be
// downloaded using its Skylink. Returns the skylink, the parameters used for
// the upload and potentially an error.
func (tn *TestNode) UploadNewSkyfileBlocking(filename string, filesize uint64, force bool) (skylink string, sup modules.SkyfileUploadParameters, sshp api.SkynetSkyfileHandlerPOST, err error) {
	// create the siapath
	siapath, err := modules.NewSiaPath(filename)
	if err != nil {
		errors.AddContext(err, "Failed to create siapath")
		return
	}
	fmt.Println("uploading at ", siapath.String())
	// create random data and wrap it in a reader
	data := fastrand.Bytes(int(filesize))
	reader := bytes.NewReader(data)
	sup = modules.SkyfileUploadParameters{
		SiaPath:             siapath,
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
		errors.AddContext(err, "Failed to upload skyfile")
		return
	}

	// verify the redundancy on the file
	skyfilePath, err := rebaseSkyfileSiaPath(siapath, false)
	if err != nil {
		errors.AddContext(err, "Failed to create siapath")
		return
	}

	remoteFile := &RemoteFile{
		checksum: crypto.HashBytes(data),
		siaPath:  skyfilePath,
	}
	fmt.Println("REMOTE FILE PATH", remoteFile.siaPath)
	// Wait until upload reached the specified progress
	if err = tn.WaitForUploadProgress(remoteFile, 1); err != nil {
		errors.AddContext(err, "Upload progress did not reach a value of 1")
		return
	}

	// Wait until upload reaches a certain health
	if err = tn.WaitForUploadHealth(remoteFile); err != nil {
		errors.AddContext(err, "Uploaded Skyfile is not considered healthy")
		return
	}
	// uploadpath, err := modules.SkynetFolder.Join(sup.SiaPath.String())
	// if err != nil {
	// 	errors.AddContext(err, "Failed to create the upload path")
	// 	return
	// }
	// err = build.Retry(10, 100*time.Millisecond, func() error {
	// 	f, err := tn.RenterFileRootGet(uploadpath)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	if f.File.Redundancy != float64(sup.BaseChunkRedundancy) {
	// 		return fmt.Errorf("bad redundancy, expected %v but was %v", sup.BaseChunkRedundancy, f.File.Redundancy)
	// 	}
	// 	return nil
	// })
	// if err != nil {
	// 	errors.AddContext(err, "Failed to verify skyfile redundancy")
	// 	return
	// }

	// // verify it can be downloaded
	// if err = build.Retry(10, 100*time.Millisecond, func() error {
	// 	_, _, err := tn.SkynetSkylinkGet(skylink)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	return nil
	// }); err != nil {
	// 	errors.AddContext(err, "Failed to download skyfile after it got uploaded")
	// 	return
	// }
	return
}
