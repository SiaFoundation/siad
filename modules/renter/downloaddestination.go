package renter

// Downloads can be written directly to a file, can be written to an http
// stream, or can be written to an in-memory buffer. The core download loop only
// has the concept of writing using WriteAt, and then calling Close when the
// download is complete.
//
// To support streaming and writing to memory buffers, the downloadDestination
// interface exists. It is used to map things like a []byte or an io.WriteCloser
// to a downloadDestination. This interface is implemented by:
//		+ os.File
//		+ downloadDestinationBuffer (an alias of a []byte)
//		+ downloadDestinationWriteCloser (created using an io.WriteCloser)
//
// There is also a helper function to convert an io.Writer to an io.WriteCloser,
// so that an io.Writer can be used to create a downloadDestinationWriteCloser
// as well.

import (
	"bufio"
	"io"
	"os"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// skipWriter is a helper type that ignores the first 'skip' bytes written to it.
type skipWriter struct {
	writer io.Writer
	skip   int
}

// Write will write bytes to the skipWriter, being sure to skip over any bytes
// which the skipWriter was initialized to skip
func (sw *skipWriter) Write(p []byte) (int, error) {
	if sw.skip == 0 {
		return sw.writer.Write(p)
	} else if sw.skip > len(p) {
		sw.skip -= len(p)
		return len(p), nil
	}
	n, err := sw.writer.Write(p[sw.skip:])
	n += sw.skip
	sw.skip = 0
	return n, err
}

// SectionWriter implements Write on a section
// of an underlying WriterAt.
type SectionWriter struct {
	w     io.WriterAt
	base  int64
	off   int64
	limit int64
}

// errSectionWriteOutOfBounds is an error returned by the section writer if a
// write would cross the boundaries between section.
var errSectionWriteOutOfBounds = errors.New("section write is out of bounds")

// NewSectionWriter returns a SectionWriter that writes to w
// starting at offset off and stops with EOF after n bytes.
func NewSectionWriter(w io.WriterAt, off int64, n int64) *SectionWriter {
	return &SectionWriter{w, off, off, off + n}
}

// Write implements the io.Writer interface using WriteAt.
func (s *SectionWriter) Write(p []byte) (n int, err error) {
	if s.off >= s.limit {
		return 0, errSectionWriteOutOfBounds
	}
	if int64(len(p)) > s.limit-s.off {
		return 0, errSectionWriteOutOfBounds
	}
	n, err = s.w.WriteAt(p, s.off)
	s.off += int64(n)
	return
}

// downloadDestination is the interface that receives the data recovered by the
// download process. The call to WritePieces is in `threadedRecoverLogicalData`.
//
// The downloadDestination interface takes a bunch of pieces because different
// types of destinations will prefer receiving pieces over receiving continuous
// data. The destinations that prefer taking continuous data can have the
// WritePieces method of the interface convert the pieces into continuous data.
type downloadDestination interface {
	// WritePieces takes the set of pieces from the chunk as input. There should
	// be at least `minPieces` pieces, but they do not need to be the data
	// pieces - the downloadDestination can check and determine if a recovery is
	// required.
	//
	// The pieces are provided decrypted.
	WritePieces(ec modules.ErasureCoder, pieces [][]byte, dataOffset uint64, writeOffset int64, length uint64) error
}

// downloadDestinationBuffer writes logical chunk data to an in-memory buffer.
// This buffer is primarily used when performing repairs on uploads.
type downloadDestinationBuffer struct {
	pieces [][]byte
}

// NewDownloadDestinationBuffer allocates the necessary number of shards for
// the downloadDestinationBuffer and returns the new buffer.
func NewDownloadDestinationBuffer() *downloadDestinationBuffer {
	return &downloadDestinationBuffer{}
}

// WritePieces stores the provided pieces for later processing.
func (dw *downloadDestinationBuffer) WritePieces(_ modules.ErasureCoder, pieces [][]byte, _ uint64, _ int64, _ uint64) error {
	dw.pieces = pieces
	return nil
}

// downloadDestinationFile wraps an os.File into a downloadDestination.
type downloadDestinationFile struct {
	deps            modules.Dependencies
	f               *os.File
	staticChunkSize int64
}

// Close implements the io.Closer interface for downloadDestinationFile.
func (ddf *downloadDestinationFile) Close() error {
	return ddf.f.Close()
}

// WritePieces will decode the pieces and write them to a file at the provided
// offset, using the provided length.
func (ddf *downloadDestinationFile) WritePieces(ec modules.ErasureCoder, pieces [][]byte, dataOffset uint64, offset int64, length uint64) error {
	sectionWriter := NewSectionWriter(ddf.f, offset, ddf.staticChunkSize)
	if ddf.deps.Disrupt("PostponeWritePiecesRecovery") {
		time.Sleep(time.Duration(fastrand.Intn(1000)) * time.Millisecond)
	}
	skipWriter := &skipWriter{
		writer: sectionWriter,
		skip:   int(dataOffset),
	}
	bufioWriter := bufio.NewWriter(skipWriter)
	err := ec.Recover(pieces, dataOffset+length, bufioWriter)
	err2 := bufioWriter.Flush()
	return errors.AddContext(errors.Compose(err, err2), "unable to write pieces to destination file")
}

// downloadDestinationWriter is a downloadDestination that writes to an
// underlying data stream. The data stream is expecting sequential data while
// the download chunks will be written in an arbitrary order using calls to
// WriteAt. We need to block the calls to WriteAt until all prior data has been
// written.
//
// NOTE: If the caller accidentally leaves a gap between calls to WriteAt, for
// example writes bytes 0-100 and then writes bytes 110-200, and accidentally
// never writes bytes 100-110, the downloadDestinationWriteCloser will block
// forever waiting for those gap bytes to be written.
//
// NOTE: Calling WriteAt has linear time performance in the number of concurrent
// calls to WriteAt.
type downloadDestinationWriter struct {
	closed    bool
	mu        sync.Mutex // Protects the underlying data structures.
	progress  int64      // How much data has been written yet.
	io.Writer            // The underlying writer.

	// A list of write calls and their corresponding locks. When one write call
	// completes, it'll search through the list of write calls for the next one.
	// The next write call can be unblocked by unlocking the corresponding mutex
	// in the next array.
	blockingWriteCalls   []int64 // A list of write calls that are waiting for their turn
	blockingWriteSignals []*sync.Mutex
}

var (
	// errClosedStream gets returned if the stream was closed but we are trying
	// to write.
	errClosedStream = errors.New("unable to write because stream has been closed")

	// errOffsetAlreadyWritten gets returned if a call to WriteAt tries to write
	// to a place in the stream which has already had data written to it.
	errOffsetAlreadyWritten = errors.New("cannot write to that offset in stream, data already written")
)

// newDownloadDestinationWriter takes an io.Writer and converts it
// into a downloadDestination.
func newDownloadDestinationWriter(w io.Writer) *downloadDestinationWriter {
	return &downloadDestinationWriter{Writer: w}
}

// unblockNextWrites will iterate over all of the blocking write calls and
// unblock any whose offsets have been reached by the current progress of the
// stream.
//
// NOTE: unblockNextWrites has linear time performance in the number of currently
// blocking calls.
func (ddw *downloadDestinationWriter) unblockNextWrites() {
	for i, offset := range ddw.blockingWriteCalls {
		if offset <= ddw.progress {
			ddw.blockingWriteSignals[i].Unlock()
			ddw.blockingWriteCalls = append(ddw.blockingWriteCalls[0:i], ddw.blockingWriteCalls[i+1:]...)
			ddw.blockingWriteSignals = append(ddw.blockingWriteSignals[0:i], ddw.blockingWriteSignals[i+1:]...)
		}
	}
}

// Close will unblock any hanging calls to WriteAt, and then call Close on the
// underlying WriteCloser.
func (ddw *downloadDestinationWriter) Close() error {
	ddw.mu.Lock()
	if ddw.closed {
		ddw.mu.Unlock()
		return errClosedStream
	}
	ddw.closed = true
	for i := range ddw.blockingWriteSignals {
		ddw.blockingWriteSignals[i].Unlock()
	}
	ddw.mu.Unlock()
	return nil
}

// WritePieces will block until the stream has progressed to 'offset', and then
// decode the pieces and write them. An error will be returned if the stream has
// already progressed beyond 'offset'.
func (ddw *downloadDestinationWriter) WritePieces(ec modules.ErasureCoder, pieces [][]byte, dataOffset uint64, offset int64, length uint64) error {
	write := func() error {
		// Error if the stream has been closed.
		if ddw.closed {
			return errClosedStream
		}
		// Error if the stream has progressed beyond 'offset'.
		if offset < ddw.progress {
			ddw.mu.Unlock()
			return errOffsetAlreadyWritten
		}

		// Write the data to the stream, and the update the progress and unblock
		// the next write.
		err := ec.Recover(pieces, dataOffset+length, &skipWriter{writer: ddw, skip: int(dataOffset)})
		ddw.progress += int64(length)
		ddw.unblockNextWrites()
		return err
	}

	ddw.mu.Lock()
	// Attempt to write if the stream progress is at or beyond the offset. The
	// write call will perform error handling.
	if offset <= ddw.progress {
		err := write()
		ddw.mu.Unlock()
		return err
	}

	// The stream has not yet progressed to 'offset'. We will block until the
	// stream has made progress. We perform the block by creating a
	// thread-specific mutex 'myMu' and adding it to the object's list of
	// blocking threads. When other threads successfully call WriteAt, they will
	// reference this list and unblock any which have enough progress. The
	// result is a somewhat strange construction where we lock myMu twice in a
	// row, but between those two calls to lock, we put myMu in a place where
	// another thread can unlock myMu.
	//
	// myMu will be unblocked when another thread calls 'unblockNextWrites'.
	myMu := new(sync.Mutex)
	myMu.Lock()
	ddw.blockingWriteCalls = append(ddw.blockingWriteCalls, offset)
	ddw.blockingWriteSignals = append(ddw.blockingWriteSignals, myMu)
	ddw.mu.Unlock()
	myMu.Lock()
	ddw.mu.Lock()
	err := write()
	ddw.mu.Unlock()
	return err
}
