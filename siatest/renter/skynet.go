package renter

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"os"
	"strings"

	"gitlab.com/NebulousLabs/Sia/siatest"

	"gitlab.com/NebulousLabs/Sia/modules"
)

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

// addMultipartField is a helper function to add a file to the multipart form-
// data. Note that the given data will be treated as binary data, and the multi
// part's ContentType header will be set accordingly.
func addMultipartFile(w *multipart.Writer, filedata []byte, filekey, filename string, filemode uint64, offset *uint64) modules.SkyfileSubfileMetadata {
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

// uploadNewMultiPartSkyfileBlocking uploads a multipart upload that
// contains several files. It then downloads the file and returns its metadata.
// The `files` argument is a map of filepath->fileContent.
func uploadNewMultiPartSkyfileBlocking(r *siatest.TestNode, filename string, files map[string][]byte, defaultPath string) (content []byte, fileMetadata modules.SkyfileMetadata, err error) {
	// create a multipart upload with index.html
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	subfiles := make(modules.SkyfileSubfiles)
	// add the files
	var offset uint64
	for fname, fcontent := range files {
		subfile := addMultipartFile(writer, fcontent, "files[]", fname, modules.DefaultFilePerm, &offset)
		subfiles[subfile.Filename] = subfile
	}
	if err = writer.Close(); err != nil {
		return
	}
	reader := bytes.NewReader(body.Bytes())
	// call the upload skyfile client call
	uploadSiaPath, err := modules.NewSiaPath(filename)
	if err != nil {
		return
	}
	sup := modules.SkyfileMultipartUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
		Reader:              reader,
		ContentType:         writer.FormDataContentType(),
		Filename:            filename,
		DefaultPath:         defaultPath,
	}
	// upload the skyfile
	skylink, _, err := r.SkynetSkyfileMultiPartPost(sup)
	if err != nil {
		return
	}
	// download the file behind the skylink
	return r.SkynetSkylinkGet(skylink)
}
