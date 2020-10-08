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

	"gitlab.com/NebulousLabs/errors"
)

var (
	// ErrInvalidDefaultPath is returned when the specified default path is not
	// valid, e.g. the file it points to does not exist.
	ErrInvalidDefaultPath = errors.New("invalid default path provided")
)

// ValidateSkyfileMetadata validates the given SkyfileMetadata
func ValidateSkyfileMetadata(metadata SkyfileMetadata) error {
	// check filename
	err := ValidatePathString(metadata.Filename, false)
	if err != nil {
		return errors.AddContext(err, fmt.Sprintf("invalid filename provided '%v'", metadata.Filename))
	}

	// check filename of every subfile
	if metadata.Subfiles != nil {
		for filename, md := range metadata.Subfiles {
			if filename != md.Filename {
				return errors.New("subfile name did not match metadata filename")
			}
			err := ValidatePathString(filename, false)
			if err != nil {
				return errors.AddContext(err, fmt.Sprintf("invalid filename provided for subfile '%v'", filename))
			}

			// note that we do not check the length property of a subfile as it
			// is possible a user might have uploaded an empty part
		}
	}

	// check length
	if metadata.Length == 0 {
		return errors.New("'Length' property not set on metadata")
	}

	// validate default path (only if default path was not explicitly disabled)
	if !metadata.DisableDefaultPath {
		metadata.DefaultPath, err = validateDefaultPath(metadata.DefaultPath, metadata.Subfiles)
		if err != nil {
			return errors.Compose(ErrInvalidDefaultPath, err)
		}
	}

	return nil
}

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

// validateDefaultPath ensures the given default path makes sense in relation to
// the subfiles being uploaded. It returns a potentially altered default path.
func validateDefaultPath(defaultPath string, subfiles SkyfileSubfiles) (string, error) {
	if defaultPath == "" {
		return defaultPath, nil
	}
	defaultPath = EnsurePrefix(defaultPath, "/")

	// check if we have a subfile at the given default path.
	subfile, found := subfiles[strings.TrimPrefix(defaultPath, "/")]
	if !found {
		return "", fmt.Errorf("no such path: %s", defaultPath)
	}

	// ensure it's an HTML file.
	if !subfile.IsHTML() {
		return "", fmt.Errorf("invalid default path '%s', the default path must point to an HTML file", defaultPath)
	}

	// ensure it's at the root of the Skyfile
	if strings.Count(defaultPath, "/") > 1 {
		return "", fmt.Errorf("invalid default path '%s', the default path must point to a file in the root directory of the skyfile", defaultPath)
	}

	return defaultPath, nil
}
