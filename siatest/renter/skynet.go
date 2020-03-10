package renter

import (
	"fmt"
	"mime/multipart"
	"net/textproto"
	"os"
	"strings"

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
// part 's ContentType header will be set accordingly.
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
