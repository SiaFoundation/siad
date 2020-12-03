package modules

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"sort"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/errors"
)

var (
	// ErrIllegalFormName is returned when the multipart form contains a part
	// under an illegal form name, only 'files[]' or 'file' are allowed.
	ErrIllegalFormName = errors.New("multipart file submitted under an illegal form name, allowed values are 'files[]' and 'file'")

	// ErrEmptyFilename is returned when the multipart form contains a part
	// with an empty filename
	ErrEmptyFilename = errors.New("no filename provided")

	// ErrSkyfileMetadataUnavailable is returned when the context passed to
	// SkyfileMetadata is cancelled before the metadata became available
	ErrSkyfileMetadataUnavailable = errors.New("metadata unavailable")
)

type (
	// SkyfileUploadReader is an interface that wraps a reader, containing the
	// Skyfile data, and adds a method to fetch the SkyfileMetadata.
	SkyfileUploadReader interface {
		AddReadBuffer(data []byte)
		FanoutReader() io.Reader
		SkyfileMetadata(ctx context.Context) (SkyfileMetadata, error)
		io.Reader
	}

	// skyfileMultipartReader is a helper struct that implements the
	// SkyfileUploadReader interface for a multipart upload.
	//
	// NOTE: reading from this object is not threadsafe and thus should not be
	// done from more than one thread if you want the reads to be deterministic.
	skyfileMultipartReader struct {
		reader  *multipart.Reader
		readBuf []byte

		fanoutReader *skyfileMultipartReader

		currLen  uint64
		currOff  uint64
		currPart *multipart.Part

		metadata      SkyfileMetadata
		metadataAvail chan struct{}
	}

	// skyfileReader is a helper struct that implements the SkyfileUploadReader
	// interface for a regular upload
	//
	// NOTE: reading from this object is not threadsafe and thus should not be
	// done from more than one thread if you want the reads to be deterministic.
	skyfileReader struct {
		reader  io.Reader
		readBuf []byte

		fanoutReader io.Reader

		currLen uint64

		metadata      SkyfileMetadata
		metadataAvail chan struct{}
	}
)

// NewSkyfileReader wraps the given reader and metadata and returns a
// SkyfileUploadReader
func NewSkyfileReader(reader io.Reader, sup SkyfileUploadParameters) SkyfileUploadReader {
	// Split the reader using a TeeReader so that the upload is not blocking the
	// creation of the fanout bytes.
	var buf bytes.Buffer
	tr := io.TeeReader(reader, &buf)

	// Define the skyfileReader
	return &skyfileReader{
		reader:       tr,
		fanoutReader: &buf,
		metadata: SkyfileMetadata{
			Filename: sup.Filename,
			Mode:     sup.Mode,
		},
		metadataAvail: make(chan struct{}),
	}
}

// AddReadBuffer adds the given bytes to the read buffer. The next reads will
// read from this buffer until it is entirely consumed, after which we continue
// reading from the underlying reader.
func (sr *skyfileReader) AddReadBuffer(b []byte) {
	sr.readBuf = append(sr.readBuf, b...)
}

// FanoutReader returns the reader to be used for generating the encoded fanout.
func (sr *skyfileReader) FanoutReader() io.Reader {
	return sr.fanoutReader
}

// SkyfileMetadata returns the SkyfileMetadata associated with this reader.
//
// NOTE: this method will block until the metadata becomes available
func (sr *skyfileReader) SkyfileMetadata(ctx context.Context) (SkyfileMetadata, error) {
	// Wait for the metadata to become available, that will be the case when
	// the reader returned an EOF, or until the context is cancelled.
	select {
	case <-ctx.Done():
		return SkyfileMetadata{}, errors.AddContext(ErrSkyfileMetadataUnavailable, "context cancelled")
	case <-sr.metadataAvail:
	}

	return sr.metadata, nil
}

// Read implements the io.Reader part of the interface and reads data from the
// underlying reader.
func (sr *skyfileReader) Read(p []byte) (n int, err error) {
	if len(sr.readBuf) > 0 {
		n = copy(p, sr.readBuf)
		sr.readBuf = sr.readBuf[n:]
	}

	// check if we've already read until EOF, that will be the case if
	// `metadataAvail` is closed.
	select {
	case <-sr.metadataAvail:
		return n, io.EOF
	default:
	}

	// return early if possible
	if n == len(p) {
		return
	}

	var nn int
	nn, err = sr.reader.Read(p[n:])
	n += nn
	sr.currLen += uint64(nn)

	if errors.Contains(err, io.EOF) {
		close(sr.metadataAvail)
		sr.metadata.Length = sr.currLen
	}
	return
}

// NewMultipartReader creates a multipar.Reader from an io.Reader and the
// provided subfiles. This reader can then be used to create
// a NewSkyfileMultipartReader.
func NewMultipartReader(reader io.Reader, subFiles SkyfileSubfiles) (*multipart.Reader, error) {
	// Read data from reader
	data, err := ioutil.ReadAll(reader)
	if err != nil {
		return nil, errors.AddContext(err, "unable to read data from reader")
	}

	// Sort the subFiles by offset
	subFilesArray := make([]SkyfileSubfileMetadata, 0, len(subFiles))
	for _, sfm := range subFiles {
		subFilesArray = append(subFilesArray, sfm)
	}
	sort.Slice(subFilesArray, func(i, j int) bool {
		return subFilesArray[i].Offset < subFilesArray[j].Offset
	})

	// Build multipart reader from subFiles
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	var offset uint64
	for _, sfm := range subFilesArray {
		_, err = AddMultipartFile(writer, data[sfm.Offset:sfm.Offset+sfm.Len], "files[]", sfm.Filename, DefaultFilePerm, &offset)
		if err != nil {
			return nil, errors.AddContext(err, "unable to add multipart file")
		}
	}
	multiReader := multipart.NewReader(body, writer.Boundary())
	if err = writer.Close(); err != nil {
		return nil, errors.AddContext(err, "unable to close writer")
	}
	return multiReader, nil
}

// NewSkyfileMultipartReader wraps the given reader and returns a
// SkyfileUploadReader. By reading from this reader until an EOF is reached, the
// SkyfileMetadata will be constructed incrementally every time a new Part is
// read.
//
// NOTE: The fanout reader should be created from the io.Reader generated by
// a TeeReader of the underlying io.Reader that was used to create the
// multipart.Reader.
func NewSkyfileMultipartReader(reader *multipart.Reader, fanoutReader *skyfileMultipartReader, sup SkyfileUploadParameters) SkyfileUploadReader {
	return &skyfileMultipartReader{
		reader:       reader,
		fanoutReader: fanoutReader,
		metadata: SkyfileMetadata{
			Filename:           sup.Filename,
			Mode:               sup.Mode,
			DefaultPath:        sup.DefaultPath,
			DisableDefaultPath: sup.DisableDefaultPath,
			Subfiles:           make(SkyfileSubfiles),
		},
		metadataAvail: make(chan struct{}),
	}
}

// newFanoutReader returns a skyfileMultipartReader that should be used as the
// fanout reader.
func newFanoutReader(reader *multipart.Reader, sup SkyfileUploadParameters) *skyfileMultipartReader {
	return &skyfileMultipartReader{
		reader: reader,
		metadata: SkyfileMetadata{
			Filename:           sup.Filename,
			Mode:               sup.Mode,
			DefaultPath:        sup.DefaultPath,
			DisableDefaultPath: sup.DisableDefaultPath,
			Subfiles:           make(SkyfileSubfiles),
		},
		metadataAvail: make(chan struct{}),
	}
}

// NewSkyfileMultipartReaderFromRequest generates a multipart reader from the
// http request and returns a SkyfileUploadReader.
func NewSkyfileMultipartReaderFromRequest(req *http.Request, sup SkyfileUploadParameters) (SkyfileUploadReader, error) {
	// Error checks on request pulled from http package MultipartReader()
	// http.Request method.
	//
	// https://golang.org/src/net/http/request.go?s=16785:16847#L453
	if req.MultipartForm != nil {
		return nil, errors.New("http: multipart previously handled")
	}
	req.MultipartForm = &multipart.Form{
		Value: make(map[string][]string),
		File:  make(map[string][]*multipart.FileHeader),
	}
	v := req.Header.Get("Content-Type")
	if v == "" {
		return nil, http.ErrNotMultipart
	}
	d, params, err := mime.ParseMediaType(v)
	allowMixed := true // pulled from http package
	if err != nil || !(d == "multipart/form-data" || allowMixed && d == "multipart/mixed") {
		return nil, http.ErrNotMultipart
	}
	boundary, ok := params["boundary"]
	if !ok {
		return nil, http.ErrMissingBoundary
	}

	// Create TeeReader and use it for creating the multipart reader.
	var buf bytes.Buffer
	tr := io.TeeReader(req.Body, &buf)
	mpr := multipart.NewReader(tr, boundary)
	mprFanout := multipart.NewReader(&buf, boundary)
	fanoutReader := newFanoutReader(mprFanout, sup)
	return NewSkyfileMultipartReader(mpr, fanoutReader, sup), nil
}

// AddReadBuffer adds the given bytes to the read buffer. The next reads will
// read from this buffer until it is entirely consumed, after which we continue
// reading from the underlying reader.
func (sr *skyfileMultipartReader) AddReadBuffer(b []byte) {
	sr.readBuf = append(sr.readBuf, b...)
}

// FanoutReader returns the reader to be used for generating the encoded fanout.
func (sr *skyfileMultipartReader) FanoutReader() io.Reader {
	return sr.fanoutReader
}

// SkyfileMetadata returns the SkyfileMetadata associated with this reader.
func (sr *skyfileMultipartReader) SkyfileMetadata(ctx context.Context) (SkyfileMetadata, error) {
	// Wait for the metadata to become available, that will be the case when
	// the reader returned an EOF, or until the context is cancelled.
	select {
	case <-ctx.Done():
		return SkyfileMetadata{}, errors.AddContext(ErrSkyfileMetadataUnavailable, "context cancelled")
	case <-sr.metadataAvail:
	}

	// Check whether we found multipart files
	if len(sr.metadata.Subfiles) == 0 {
		return SkyfileMetadata{}, errors.New("could not find multipart file")
	}

	// Use the filename of the first subfile if it's not passed as query
	// string parameter and there's only one subfile.
	if sr.metadata.Filename == "" && len(sr.metadata.Subfiles) == 1 {
		for _, sf := range sr.metadata.Subfiles {
			sr.metadata.Filename = sf.Filename
			break
		}
	}

	// Set the total length as the sum of the lengths of every subfile
	if sr.metadata.Length == 0 {
		for _, sf := range sr.metadata.Subfiles {
			sr.metadata.Length += sf.Len
		}
	}

	return sr.metadata, nil
}

// Read implements the io.Reader part of the interface and reads data from the
// underlying multipart reader. While the data is being read, the metadata is
// being constructed.
func (sr *skyfileMultipartReader) Read(p []byte) (n int, err error) {
	if len(sr.readBuf) > 0 {
		n = copy(p, sr.readBuf)
		sr.readBuf = sr.readBuf[n:]
	}

	// check if we've already read until EOF, that will be the case if
	// `metadataAvail` is closed.
	select {
	case <-sr.metadataAvail:
		return n, io.EOF
	default:
	}

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

// createSubfileFromCurrPart adds a subfile for the current part.
func (sr *skyfileMultipartReader) createSubfileFromCurrPart() error {
	// sanity check the reader has a current part set
	if sr.currPart == nil {
		build.Critical("createSubfileFromCurrPart called when currPart is nil")
		return errors.New("could not create metadata for subfile")
	}

	// parse the mode from the part header
	mode, err := parseMode(sr.currPart.Header.Get("Mode"))
	if err != nil {
		return errors.AddContext(err, "failed to parse file mode")
	}

	// parse the filename
	filename := sr.currPart.FileName()
	if filename == "" {
		return ErrEmptyFilename
	}

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
