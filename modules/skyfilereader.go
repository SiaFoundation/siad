package modules

import (
	"fmt"
	"io"
	"mime/multipart"
	"os"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/errors"
)

var (
	// ErrIllegalFormName is returned when the multipart form contains a part
	// under an illegal form name, only 'files[]' or 'file' are allowed.
	ErrIllegalFormName = errors.New("multipart file submitted under an illegal form name, allowed values are 'files[]' and 'file'")
)

type (
	// SkyfileUploadReader is an interface that wraps a reader, containing the
	// Skyfile data, and adds a method to fetch the SkyfileMetadata.
	SkyfileUploadReader interface {
		SkyfileMetadata() SkyfileMetadata
		io.Reader
	}

	// skyfileMultipartReader is a helper struct that implements the
	// SkyfileUploadReader interface for a multipart upload.
	//
	// NOTE: this object is not threadsafe and thus should not be called from
	// more than one thread.
	skyfileMultipartReader struct {
		reader *multipart.Reader

		currLen  uint64
		currOff  uint64
		currPart *multipart.Part

		metadata      SkyfileMetadata
		metadataAvail chan struct{}
	}

	// skyfileMultipartReader is a helper struct that implements the
	// SkyfileUploadReader interface for a regular upload
	//
	// NOTE: this object is not threadsafe and thus should not be called from
	// more than one thread.
	skyfileReader struct {
		reader   io.Reader
		metadata SkyfileMetadata
	}
)

// NewSkyfileReader wraps the given reader and metadata and returns a
// SkyfileUploadReader
func NewSkyfileReader(reader io.Reader, metadata SkyfileMetadata) SkyfileUploadReader {
	return &skyfileReader{
		reader:   reader,
		metadata: metadata,
	}
}

// SkyfileMetadata returns the SkyfileMetadata associated with this reader.
func (sr *skyfileReader) SkyfileMetadata() SkyfileMetadata {
	return sr.metadata
}

// Read implents the io.Reader part of the interface and reads data from the
// underlying reader.
func (sr *skyfileReader) Read(p []byte) (n int, err error) {
	return sr.reader.Read(p)
}

// NewSkyfileReader wraps the given reader and returns a SkyfileUploadReader.
// By reading from this reader until an EOF is reached, the SkyfileMetadata will
// be constructed incrementally every time a new Part is read.
func NewSkyfileMultipartReader(reader *multipart.Reader) SkyfileUploadReader {
	return &skyfileMultipartReader{
		reader:        reader,
		metadata:      SkyfileMetadata{Subfiles: make(SkyfileSubfiles)},
		metadataAvail: make(chan struct{}),
	}
}

// SkyfileMetadata returns the SkyfileMetadata associated with this reader.
func (sr *skyfileMultipartReader) SkyfileMetadata() SkyfileMetadata {
	<-sr.metadataAvail // TODO use a tg context
	return sr.metadata
}

// Read implents the io.Reader part of the interface and reads data from the
// underlying multipart reader. While the data is being read, the metadata is
// being constructed.
func (sr *skyfileMultipartReader) Read(p []byte) (n int, err error) {
	for n < len(p) && err == nil {
		// only read the next part if the current part is not set
		if sr.currPart == nil {
			sr.currPart, err = sr.reader.NextPart()
			if err != nil {
				// only when `NextPart` errors out we want to signal the
				// metadata is ready, on any error not only EOF
				close(sr.metadataAvail)
				break
			}
			sr.currOff += sr.currLen
			sr.currLen = 0

			// verify the multipart file is submitted under the expected name
			if !isLegalFormName(sr.currPart.FormName()) {
				err = ErrIllegalFormName
				break
			}
		}

		// read data from the part
		var nn int
		nn, err = sr.currPart.Read(p[n:])
		n += nn

		// update the length
		sr.currLen += uint64(nn)

		// ignore the EOF to continue reading from the next part if necessary,
		if err == io.EOF {
			err = nil

			// create the metadata for the current subfile before resetting the
			// current part
			err = sr.createSubfileFromCurrPart()
			if err != nil {
				break
			}
			sr.currPart = nil
		}
	}

	return
}

// createSubfileFromCurrPart adds a subfile to the metadata's Subfiles for the
// part that is currently set as `currPart`.
func (sr *skyfileMultipartReader) createSubfileFromCurrPart() error {
	if sr.currPart == nil {
		build.Critical("createSubfileFromCurrPart called when currPart is nil")
	}

	mode, err := parseMode(sr.currPart.Header.Get("Mode"))
	if err != nil {
		return errors.AddContext(err, "failed to parse file mode")
	}

	filename := sr.currPart.FileName()
	sr.metadata.Subfiles[filename] = SkyfileSubfileMetadata{
		FileMode:    mode,
		Filename:    filename,
		ContentType: sr.currPart.Header.Get("Content-Type"),
		Offset:      sr.currOff,
		Len:         sr.currLen,
	}
	return nil
}

// isLegalFormName is a helper function that returns true if the given form name
// is allowed to submit a Skyfile subfile.
func isLegalFormName(formName string) bool {
	return formName == "file" || formName == "files[]"
}

// parseMode is a helper function that parses an os.FileMode from the given
// string.
func parseMode(modeStr string) (os.FileMode, error) {
	var mode os.FileMode
	if modeStr != "" {
		_, err := fmt.Sscanf(modeStr, "%o", &mode)
		if err != nil {
			return mode, err
		}
	}
	return mode, nil
}
