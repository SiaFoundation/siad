package modules

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
)

// AddMultipartFile is a helper function to add a file to multipart form-data.
// Note that the given data will be treated as binary data and the multipart
// ContentType header will be set accordingly.
func AddMultipartFile(w *multipart.Writer, filedata []byte, filekey, filename string, filemode uint64, offset *uint64) (SkyfileSubfileMetadata, error) {
	filemodeStr := fmt.Sprintf("%o", filemode)
	contentType, err := fileContentType(filename, bytes.NewReader(filedata))
	if err != nil {
		return SkyfileSubfileMetadata{}, err
	}
	partHeader := createFormFileHeaders(filekey, filename, filemodeStr, contentType)
	part, err := w.CreatePart(partHeader)
	if err != nil {
		return SkyfileSubfileMetadata{}, err
	}
	_, err = part.Write(filedata)
	if err != nil {
		return SkyfileSubfileMetadata{}, err
	}
	metadata := SkyfileSubfileMetadata{
		Filename:    filename,
		ContentType: contentType,
		FileMode:    os.FileMode(filemode),
		Len:         uint64(len(filedata)),
	}
	if offset != nil {
		metadata.Offset = *offset
		*offset += metadata.Len
	}
	return metadata, nil
}

// EnsurePrefix checks if `str` starts with `prefix` and adds it if that's not
// the case.
func EnsurePrefix(str, prefix string) string {
	if strings.HasPrefix(str, prefix) {
		return str
	}
	return prefix + str
}

// EnsureSuffix checks if `str` ends with `suffix` and adds it if that's not
// the case.
func EnsureSuffix(str, suffix string) string {
	if strings.HasSuffix(str, suffix) {
		return str
	}
	return str + suffix
}

// createFormFileHeaders builds a header from the given params. These headers
// are used when creating the parts in a multi-part form upload.
func createFormFileHeaders(fieldname, filename, filemode, contentType string) textproto.MIMEHeader {
	quoteEscaper := strings.NewReplacer("\\", "\\\\", `"`, "\\\"")
	fieldname = quoteEscaper.Replace(fieldname)
	filename = quoteEscaper.Replace(filename)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Type", contentType)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldname, filename))
	h.Set("mode", filemode)
	return h
}

// fileContentType extracts the content type from a given file. If the content
// type cannot be determined by the file's extension, this function will read up
// to 512 bytes from the provided reader.
func fileContentType(filename string, file io.Reader) (string, error) {
	contentType := mime.TypeByExtension(filepath.Ext(filename))
	if contentType != "" {
		return contentType, nil
	}
	// Only the first 512 bytes are used to sniff the content type.
	buffer := make([]byte, 512)
	_, err := file.Read(buffer)
	if err != nil {
		return "", err
	}
	// Always returns a valid content-type by returning
	// "application/octet-stream" if no others seemed to match.
	return http.DetectContentType(buffer), nil
}
