package siatest

import (
	"bytes"
	"mime/multipart"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestFile is a small helper struct that identifies a file to be uploaded. The
// upload helpers take a slice of these files to ensure order is maintained.
type TestFile struct {
	Name string
	Data []byte
}

// UploadNewSkyfileWithDataBlocking attempts to upload a skyfile with given
// data. After it has successfully performed the upload, it will verify the file
// can be downloaded using its Skylink. Returns the skylink, the parameters used
// for the upload and potentially an error.
func (tn *TestNode) UploadNewSkyfileWithDataBlocking(filename string, filedata []byte, force bool) (skylink string, sup modules.SkyfileUploadParameters, sshp api.SkynetSkyfileHandlerPOST, err error) {
	return tn.UploadNewEncryptedSkyfileBlocking(filename, filedata, "", force)
}

// UploadNewEncryptedSkyfileBlocking attempts to upload a skyfile. After it has
// successfully performed the upload, it will verify the file can be downloaded
// using its Skylink. Returns the skylink, the parameters used for the upload
// and potentially an error.
func (tn *TestNode) UploadNewEncryptedSkyfileBlocking(filename string, filedata []byte, skykeyName string, force bool) (skylink string, sup modules.SkyfileUploadParameters, sshp api.SkynetSkyfileHandlerPOST, err error) {
	return tn.UploadSkyfileBlockingCustom(filename, filedata, skykeyName, 2, force)
}

// UploadSkyfileCustom attempts to upload a skyfile. Returns the skylink, the
// parameters used for the upload and potentially an error.
func (tn *TestNode) UploadSkyfileCustom(filename string, filedata []byte, skykeyName string, baseChunkRedundancy uint8, force bool) (skylink string, sup modules.SkyfileUploadParameters, sshp api.SkynetSkyfileHandlerPOST, rf *RemoteFile, err error) {
	// create the siapath
	skyfilePath, err := modules.NewSiaPath(filename)
	if err != nil {
		err = errors.AddContext(err, "Failed to create siapath")
		return
	}

	// wrap the data in a reader
	reader := bytes.NewReader(filedata)
	sup = modules.SkyfileUploadParameters{
		SiaPath:             skyfilePath,
		BaseChunkRedundancy: baseChunkRedundancy,
		Filename:            filename,
		Mode:                modules.DefaultFilePerm,
		Reader:              reader,
		Force:               force,
		Root:                false,
		SkykeyName:          skykeyName,
	}

	// upload a skyfile
	skylink, sshp, err = tn.SkynetSkyfilePost(sup)
	if err != nil {
		err = errors.AddContext(err, "Failed to upload skyfile")
		return
	}

	if !sup.Root {
		skyfilePath, err = modules.SkynetFolder.Join(skyfilePath.String())
		if err != nil {
			err = errors.AddContext(err, "Failed to rebase skyfile path")
			return
		}
	}
	// Return the Remote File for callers to block for upload progress
	rf = &RemoteFile{
		checksum: crypto.HashBytes(filedata),
		siaPath:  skyfilePath,
		root:     true,
	}
	return
}

// UploadSkyfileBlockingCustom attempts to upload a skyfile. After it has
// successfully performed the upload, it will verify the file can be downloaded
// using its Skylink. Returns the skylink, the parameters used for the upload
// and potentially an error.
func (tn *TestNode) UploadSkyfileBlockingCustom(filename string, filedata []byte, skykeyName string, baseChunkRedundancy uint8, force bool) (skylink string, sup modules.SkyfileUploadParameters, sshp api.SkynetSkyfileHandlerPOST, err error) {
	// Upload the file
	var rf *RemoteFile
	skylink, sup, sshp, rf, err = tn.UploadSkyfileCustom(filename, filedata, skykeyName, baseChunkRedundancy, force)
	if err != nil {
		err = errors.AddContext(err, "Skyfile upload failed")
		return
	}

	// Wait until upload reached the specified progress
	if err = tn.WaitForUploadProgress(rf, 1); err != nil {
		err = errors.AddContext(err, "Skyfile upload failed, progress did not reach a value of 1")
		return
	}

	// wait until upload reaches a certain health
	if err = tn.WaitForUploadHealth(rf); err != nil {
		err = errors.AddContext(err, "Skyfile upload failed, health did not reach the repair threshold")
		return
	}

	return
}

// UploadNewSkyfileBlocking attempts to upload a skyfile of given size. After it
// has successfully performed the upload, it will verify the file can be
// downloaded using its Skylink. Returns the skylink, the parameters used for
// the upload and potentially an error.
func (tn *TestNode) UploadNewSkyfileBlocking(filename string, filesize uint64, force bool) (skylink string, sup modules.SkyfileUploadParameters, sshp api.SkynetSkyfileHandlerPOST, err error) {
	data := fastrand.Bytes(int(filesize))
	return tn.UploadNewSkyfileWithDataBlocking(filename, data, force)
}

// UploadNewMultipartSkyfileBlocking uploads a multipart skyfile that
// contains several files. After it has successfully performed the upload, it
// will verify the file can be downloaded using its Skylink. Returns the
// skylink, the parameters used for the upload and potentially an error.
// The `files` argument is a map of filepath->fileContent.
func (tn *TestNode) UploadNewMultipartSkyfileBlocking(filename string, files []TestFile, defaultPath string, disableDefaultPath bool, force bool) (skylink string, sup modules.SkyfileMultipartUploadParameters, sshp api.SkynetSkyfileHandlerPOST, err error) {
	return tn.UploadNewMultipartSkyfileEncryptedBlocking(filename, files, defaultPath, disableDefaultPath, force, "", skykey.SkykeyID{})
}

// UploadNewMultipartSkyfileEncryptedBlocking uploads a multipart skyfile that
// contains several files. After it has successfully performed the upload, it
// will verify the file can be downloaded using its Skylink. Returns the
// skylink, the parameters used for the upload and potentially an error.  The
// `files` argument is a map of filepath->fileContent.
func (tn *TestNode) UploadNewMultipartSkyfileEncryptedBlocking(filename string, files []TestFile, defaultPath string, disableDefaultPath bool, force bool, skykeyName string, skykeyID skykey.SkykeyID) (skylink string, sup modules.SkyfileMultipartUploadParameters, sshp api.SkynetSkyfileHandlerPOST, err error) {
	// create the siapath
	skyfilePath, err := modules.NewSiaPath(filename)
	if err != nil {
		err = errors.AddContext(err, "Failed to create siapath")
		return
	}

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)

	// add the files
	var offset uint64
	for _, tf := range files {
		_, err = modules.AddMultipartFile(writer, tf.Data, "files[]", tf.Name, modules.DefaultFilePerm, &offset)
		if err != nil {
			panic(err)
		}
	}

	if err = writer.Close(); err != nil {
		return
	}
	reader := bytes.NewReader(body.Bytes())

	sup = modules.SkyfileMultipartUploadParameters{
		SiaPath:             skyfilePath,
		BaseChunkRedundancy: 2,
		Reader:              reader,
		Force:               force,
		Root:                false,
		ContentType:         writer.FormDataContentType(),
		Filename:            filename,
		DefaultPath:         defaultPath,
		DisableDefaultPath:  disableDefaultPath,
	}

	// upload a skyfile
	skylink, sshp, err = tn.SkynetSkyfileMultiPartEncryptedPost(sup, skykeyName, skykeyID)
	if err != nil {
		err = errors.AddContext(err, "Failed to upload skyfile")
		return
	}

	if !sup.Root {
		skyfilePath, err = modules.SkynetFolder.Join(skyfilePath.String())
		if err != nil {
			err = errors.AddContext(err, "Failed to rebase skyfile path")
			return
		}
	}
	rf := &RemoteFile{
		checksum: crypto.HashBytes(body.Bytes()),
		siaPath:  skyfilePath,
		root:     true,
	}

	// Wait until upload reached the specified progress
	if err = tn.WaitForUploadProgress(rf, 1); err != nil {
		err = errors.AddContext(err, "Skyfile upload failed, progress did not reach a value of 1")
		return
	}

	// wait until upload reaches a certain health
	if err = tn.WaitForUploadHealth(rf); err != nil {
		err = errors.AddContext(err, "Skyfile upload failed, health did not reach the repair threshold")
		return
	}

	return
}
