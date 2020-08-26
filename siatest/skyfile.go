package siatest

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"os"
	"strings"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestFile is a small helper struct that identifies a file to be uploaded. The
// upload helpers take a slice of these files to ensure order is maintained.
type TestFile struct {
	Name string
	Data []byte
}

// AddMultipartFile is a helper function to add a file to the multipart form-
// data. Note that the given data will be treated as binary data, and the multi
// part's ContentType header will be set accordingly.
func AddMultipartFile(w *multipart.Writer, filedata []byte, filekey, filename string, filemode uint64, offset *uint64) modules.SkyfileSubfileMetadata {
	filemodeStr := fmt.Sprintf("%o", filemode)
	partHeader := createFormFileHeaders(filekey, filename, filemodeStr)
	part, err := w.CreatePart(partHeader)
	if err != nil {
		panic(err)
	}

	_, err = part.Write(filedata)
	if err != nil {
		panic(err)
	}

	metadata := modules.SkyfileSubfileMetadata{
		Filename:    filename,
		ContentType: "application/octet-stream",
		FileMode:    os.FileMode(filemode),
		Len:         uint64(len(filedata)),
	}

	if offset != nil {
		metadata.Offset = *offset
		*offset += metadata.Len
	}

	return metadata
}

// UploadNewSkyfileWithDataBlocking attempts to upload a skyfile with given
// data. After it has successfully performed the upload, it will verify the file
// can be downloaded using its Skylink. Returns the skylink, the parameters used
// for the upload and potentially an error.
func (tn *TestNode) UploadNewSkyfileWithDataBlocking(filename string, filedata []byte, force bool) (skylink string, sup modules.SkyfileUploadParameters, sshp api.SkynetSkyfileHandlerPOST, err error) {
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
		BaseChunkRedundancy: 2,
		FileMetadata: modules.SkyfileMetadata{
			Filename: filename,
			Length:   uint64(len(filedata)),
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

	if !sup.Root {
		skyfilePath, err = modules.SkynetFolder.Join(skyfilePath.String())
		if err != nil {
			err = errors.AddContext(err, "Failed to rebase skyfile path")
			return
		}
	}
	rf := &RemoteFile{
		checksum: crypto.HashBytes(filedata),
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
	// create the siapath
	skyfilePath, err := modules.NewSiaPath(filename)
	if err != nil {
		err = errors.AddContext(err, "Failed to create siapath")
		return
	}

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	subfiles := make(modules.SkyfileSubfiles)

	// add the files
	var offset uint64
	for _, tf := range files {
		subfile := AddMultipartFile(writer, tf.Data, "files[]", tf.Name, modules.DefaultFilePerm, &offset)
		subfiles[subfile.Filename] = subfile
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
	skylink, sshp, err = tn.SkynetSkyfileMultiPartPost(sup)
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

// escapeQuotes escapes the quotes in the given string.
func escapeQuotes(s string) string {
	quoteEscaper := strings.NewReplacer("\\", "\\\\", `"`, "\\\"")
	return quoteEscaper.Replace(s)
}

// createFormFileHeaders builds a header from the given params. These headers
// are used when creating the parts in a multi-part form upload.
func createFormFileHeaders(fieldname, filename, filemode string) textproto.MIMEHeader {
	fieldname = escapeQuotes(fieldname)
	filename = escapeQuotes(filename)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldname, filename))
	h.Set("mode", filemode)
	return h
}
