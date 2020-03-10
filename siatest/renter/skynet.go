package renter

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strings"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest"
)

// escapeQuotes escapes the quotes in the given string.
func escapeQuotes(s string) string {
	quoteEscaper := strings.NewReplacer("\\", "\\\\", `"`, "\\\"")
	return quoteEscaper.Replace(s)
}

// createFormFileHeaders builds a header from the given params. These headers are used when creating the parts in a multi-part form upload.
func createFormFileHeaders(fieldname, filename string, headers map[string]string) textproto.MIMEHeader {
	fieldname = escapeQuotes(fieldname)
	filename = escapeQuotes(filename)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldname, filename))
	for k, v := range headers {
		h.Set(k, v)
	}
	return h
}

// addMultipartField is a helper function to add a file to the multipart form-
// data. Note that the given data will be treated as binary data, and the multi
// part 's ContentType header will be set accordingly.
func addMultipartFile(w *multipart.Writer, filedata []byte, filekey, filename string, filemode uint64, offset *uint64) modules.SkyfileSubfileMetadata {
	h := map[string]string{"mode": fmt.Sprintf("%o", filemode)}
	partHeader := createFormFileHeaders(filekey, filename, h)
	part, err := w.CreatePart(partHeader)
	if err != nil {
		panic(err)
	}

	_, err = part.Write(filedata)
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

// skynetSkyfilePostRequestWithHeaders is a helper function that turns the given
// SkyfileUploadParameters into a SkynetSkyfilePost request, allowing additional
// headers to be set on the request object
func skynetSkyfilePostRequestWithHeaders(r *siatest.TestNode, sup modules.SkyfileUploadParameters) (*http.Request, error) {
	values := url.Values{}
	values.Set("filename", sup.FileMetadata.Filename)
	forceStr := fmt.Sprintf("%t", sup.Force)
	values.Set("force", forceStr)
	modeStr := fmt.Sprintf("%o", sup.FileMetadata.Mode)
	values.Set("mode", modeStr)
	redundancyStr := fmt.Sprintf("%v", sup.BaseChunkRedundancy)
	values.Set("basechunkredundancy", redundancyStr)
	rootStr := fmt.Sprintf("%t", sup.Root)
	values.Set("root", rootStr)

	resource := fmt.Sprintf("/skynet/skyfile/%s?%s", sup.SiaPath.String(), values.Encode())
	return r.NewRequest("POST", resource, sup.Reader)
}
