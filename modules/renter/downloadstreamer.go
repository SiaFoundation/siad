package renter

import (
	"bytes"
	"io"
	"math"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

type (
	// streamer is a modules.Streamer that can be used to stream downloads from
	// the sia network.
	streamer struct {
		staticFile      *siafile.Snapshot
		staticFileEntry *siafile.SiaFileSetEntry
		offset          int64
		r               *Renter
	}
)

// min is a helper function to find the minimum of multiple values.
func min(values ...uint64) uint64 {
	min := uint64(math.MaxUint64)
	for _, v := range values {
		if v < min {
			min = v
		}
	}
	return min
}

// Streamer creates a modules.Streamer that can be used to stream downloads from
// the sia network.
func (r *Renter) Streamer(siaPath string) (string, modules.Streamer, error) {
	// Lookup the file associated with the nickname.
	entry, err := r.staticFileSet.Open(siaPath)
	if err != nil {
		return "", nil, err
	}
	// Create the streamer
	s := &streamer{
		staticFile:      entry.Snapshot(),
		staticFileEntry: entry,
		r:               r,
	}
	return entry.SiaPath(), s, nil
}

// Close closes the streamer and let's the fileSet know that the SiaFile is no
// longer in use.
func (s *streamer) Close() error {
	err1 := s.staticFileEntry.SiaFile.UpdateAccessTime()
	err2 := s.staticFileEntry.Close()
	return errors.Compose(err1, err2)
}

// Read implements the standard Read interface. It will download the requested
// data from the sia network and block until the download is complete.  To
// prevent http.ServeContent from requesting too much data at once, Read can
// only request a single chunk at once.
func (s *streamer) Read(p []byte) (n int, err error) {
	// Get the file's size
	fileSize := int64(s.staticFile.Size())

	// Make sure we haven't reached the EOF yet.
	if s.offset >= fileSize {
		return 0, io.EOF
	}

	// Calculate how much we can download. We never download more than a single chunk.
	chunkIndex, chunkOffset := s.staticFile.ChunkIndexByOffset(uint64(s.offset))
	if chunkIndex == s.staticFile.NumChunks() {
		return 0, io.EOF
	}
	remainingData := uint64(fileSize - s.offset)
	requestedData := uint64(len(p))
	remainingChunk := s.staticFile.ChunkSize() - chunkOffset
	length := min(remainingData, requestedData, remainingChunk)

	// Download data.
	buffer := bytes.NewBuffer([]byte{})
	ddw := newDownloadDestinationWriter(buffer)
	d, err := s.r.managedNewDownload(downloadParams{
		destination:       ddw,
		destinationType:   destinationTypeSeekStream,
		destinationString: "httpresponse",
		file:              s.staticFile,

		latencyTarget: 50 * time.Millisecond, // TODO low default until full latency suport is added.
		length:        length,
		needsMemory:   true,
		offset:        uint64(s.offset),
		overdrive:     5,    // TODO: high default until full overdrive support is added.
		priority:      1000, // TODO: high default until full priority support is added.
	})
	if err != nil {
		err = errors.Compose(err, ddw.Close())
		return 0, errors.AddContext(err, "failed to create new download")
	}

	// Register some cleanup for when the download is done.
	d.OnComplete(func(_ error) error {
		// close the destination buffer to avoid deadlocks.
		return ddw.Close()
	})

	// Set the in-memory buffer to nil just to be safe in case of a memory
	// leak.
	defer func() {
		d.destination = nil
	}()

	// Block until the download has completed.
	select {
	case <-d.completeChan:
		if d.Err() != nil {
			return 0, errors.AddContext(d.Err(), "download failed")
		}
	case <-s.r.tg.StopChan():
		return 0, errors.New("download interrupted by shutdown")
	}

	// Copy downloaded data into buffer.
	copy(p, buffer.Bytes())

	// Adjust offset
	s.offset += int64(length)
	return int(length), nil
}

// Seek sets the offset for the next Read to offset, interpreted
// according to whence: SeekStart means relative to the start of the file,
// SeekCurrent means relative to the current offset, and SeekEnd means relative
// to the end. Seek returns the new offset relative to the start of the file
// and an error, if any.
func (s *streamer) Seek(offset int64, whence int) (int64, error) {
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = 0
	case io.SeekCurrent:
		newOffset = s.offset
	case io.SeekEnd:
		newOffset = int64(s.staticFile.Size())
	}
	newOffset += offset

	if newOffset < 0 {
		return s.offset, errors.New("cannot seek to negative offset")
	}
	s.offset = newOffset
	return s.offset, nil
}
