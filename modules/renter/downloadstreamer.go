package renter

import (
	"bytes"
	"io"
	"math"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

type (
	// streamer is a modules.Streamer that can be used to stream downloads from
	// the sia network.
	streamer struct {
		// File variables.
		staticFile      *siafile.Snapshot
		staticFileEntry *siafile.SiaFileSetEntry
		offset          int64
		r               *Renter

		// Caching variables. The cache is a memory store with the most recent
		// data. The offset indicates where in the file the cache starts.
		// requestDepth is how far in the file th current request extends.
		// cacheActive indicates whether another thread is currently extending
		// the cache, which prevents multiple threads from extending the cache
		// with the same data simultaneously.
		//
		// TODO: Currently we use a []byte and we do a lot of copying to keep
		// this []byte full of the most recent data. An optimization we should
		// make before shipping is to turn this []byte into a ring buffer.
		cache      []byte
		cacheActive chan struct{}
		cacheErr    error
		cacheOffset int
		cacheMu     sync.Mutex
		cacheReady  chan struct{}
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

		cacheActive: make(chan struct{}, 1),
		cacheReady: make(chan struct{}),
	}
	// Put an object into the cacheActive to indicate that there is no cache
	// thread running at the moment.
	s.cacheActive <-struct{}{}
	s.managedFillCache()

	return entry.SiaPath(), s, nil
}

// managedFillCache will fill out the cache, starting from the current offset of the
// file 
func (s *streamer) managedFillCache() {
	// Check whether the cache needs to be filled at all.
	s.cacheMu.Lock()
	partialDownloadsSupported := s.staticFile.ErasureCode().SupportsPartialEncoding()
	chunkSize := s.staticFile.ChunkSize()
	cacheOffset := int64(s.cacheOffset)
	streamOffset := s.offset
	cacheLen := int64(len(s.cache))
	s.cacheMu.Unlock()
	if partialDownloadsSupported && cacheOffset == streamOffset && cacheLen > 0 {
		// If partial downloads are supported, the cache offset should start at
		// the same place as the current stream offset. If so, then the cache is
		// already updated and no action is needed. An extra check for cacheLen
		// > 0 needs to be added to ensure that some part of the cache is
		// fetched on the very first call.
		return
	}
	if !partialDownloadsSupported && cacheOffset <= streamOffset && streamOffset < cacheOffset + cacheLen && uint64(cacheLen) == chunkSize {
		// If partial downloads are not supported, the full chunk containing the
		// current offset should be the cache. If the cache is the full chunk
		// that contains current offset, then nothing needs to be done as the
		// cache is already prepared.
		return
	}

	// If the offset is one byte beyond the current cache size, then the stream
	// data is being read faster than the cacheing has been able to supply data,
	// which means that the cache size needs to be increased. The cache size
	// should not be increased if it is already greater than or equal to the max
	// cache size.
	increaseCacheSize := false
	if streamOffset == cacheOffset + cacheLen && cacheLen <= maxStreamerCacheSize {
		increaseCacheSize = true
	}

	// Filling the cache happens in a goroutine so that the managedFillCache function
	// returns immediately.
	go func() {
		// Check if another thread is currently extending the cache. If we are
		// unable to instantly grab the cacheActive object, it means that another
		// thread is already extending the cache.
		select{
		case <-s.cacheActive:
		default:
			return
		}
		// NOTE: The ordering here is delicate. When the function returns, the
		// first thing that happens is that the function returns the object to
		// the cacheActive channel, which allows another thread to extend the
		// cache. After this has been done, managedFillCache is called again to
		// check whether more of the cache has been drained and whether the
		// cache needs to be topped up again.
		defer s.managedFillCache()
		defer func() {
			s.cacheActive <-struct{}{}
		}()

		// Re-fetch the variables important to gathering cache, in case they
		// have changed.
		s.cacheMu.Lock()
		partialDownloadsSupported := s.staticFile.ErasureCode().SupportsPartialEncoding()
		chunkSize := s.staticFile.ChunkSize()
		cacheOffset := int64(s.cacheOffset)
		streamOffset := s.offset
		cacheLen := int64(len(s.cache))
		s.cacheMu.Unlock()

		// Determine what data needs to be fetched.
		var fetchOffset, fetchLen int64
		if !partialDownloadsSupported {
			// Fetch the full chunk containing this offset, partial downloads
			// are not supported so we try to use all of the data that will be
			// getting fetched anyway.
			chunkIndex, _ := s.staticFile.ChunkIndexByOffset(uint64(streamOffset))
			fetchOffset = int64(chunkIndex * chunkSize)
			fetchLen = int64(chunkSize)
		} else if streamOffset >= cacheOffset + cacheLen || streamOffset < cacheOffset {
			// The stream offset is completely outside the range of the existing
			// cache, therefore the full range of cache should be replaced.
			fetchOffset = streamOffset
			fetchLen = cacheLen
			if fetchLen == 0 {
				// Check for an un-initialized cache
				fetchLen = initialStreamerCacheSize
			}
			if increaseCacheSize {
				fetchLen += cacheLen
			}
		} else {
			// The stream offset is contained within the current cache, but the
			// current cache does not extend to the full desired range of data.
			fetchOffset = cacheOffset + cacheLen
			fetchLen = cacheLen - (streamOffset - cacheOffset)
			if increaseCacheSize {
				fetchLen += cacheLen
			}
		}

		// Perform the actual download.
		buffer := bytes.NewBuffer([]byte{})
		ddw := newDownloadDestinationWriter(buffer)
		d, err := s.r.managedNewDownload(downloadParams{
			destination:       ddw,
			destinationType:   destinationTypeSeekStream,
			destinationString: "httpresponse",
			file:              s.staticFile,

			latencyTarget: 50 * time.Millisecond, // TODO low default until full latency suport is added.
			length:        uint64(fetchLen),
			needsMemory:   true,
			offset:        uint64(fetchOffset),
			overdrive:     5,    // TODO: high default until full overdrive support is added.
			priority:      1000, // TODO: high default until full priority support is added.
		})
		if err != nil {
			s.cacheMu.Lock()
			s.cacheErr = errors.Compose(err, ddw.Close())
			s.cacheMu.Unlock()
			return
		}
		// Register some cleanup for when the download is done.
		d.OnComplete(func(_ error) error {
			// close the destination buffer to avoid deadlocks.
			err := ddw.Close()
			s.cacheMu.Lock()
			if s.cacheErr == nil && err != nil {
				s.cacheErr = err
			}
			s.cacheMu.Unlock()
			return err
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
				s.cacheMu.Lock()
				s.cacheErr = errors.AddContext(d.Err(), "download failed")
				s.cacheMu.Unlock()
			}
		case <-s.r.tg.StopChan():
			s.cacheMu.Lock()
			s.cacheErr = errors.New("download interrupted by shutdown")
			s.cacheMu.Unlock()
		}

		// Update the cache.
		s.cacheMu.Lock()
		defer s.cacheMu.Unlock()
		if int64(s.cacheOffset) != cacheOffset {
			// Sanity check to verify that we are still writing the correct data
			// to the cache.
			build.Critical("The stream cache offset changed while new cache data was being fetched")
		}
		if !partialDownloadsSupported || streamOffset >= cacheOffset + cacheLen || streamOffset < cacheOffset {
			// The whole cache needs to be replaced. Either because partial
			// downloads are not supported, or because the stream offset was
			// outside the range of the cache.
			s.cache = buffer.Bytes()
			s.cacheOffset = int(fetchOffset)
		} else {
			s.cache = s.cache[streamOffset-cacheOffset:]
			s.cache = append(s.cache, buffer.Bytes()...)
			s.cacheOffset = int(streamOffset)
		}
	}()
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
	// TODO: Should we add locking here to ensure read is only being called
	// sequentially and not in parallel? I believe that the current code
	// structure can deadlock if Read is called in parallel.

	// Get the file's size and check for EOF.
	//
	// TODO: Is this all the checking we need to do before fetching data from
	// the cache?
	fileSize := int64(s.staticFile.Size())
	if s.offset >= fileSize {
		return 0, io.EOF
	}

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	// Loop until the cache has loaded data that we are interested in.
	for s.offset < int64(s.cacheOffset) || int64(s.cacheOffset+len(s.cache)) <= s.offset {
		// The blocking must happen without holding the cache lock.
		cacheReady := s.cacheReady
		s.cacheMu.Unlock()
		s.managedFillCache()
		<-cacheReady
		s.cacheMu.Lock()
	}

	// Check for any errors that may have occurred while waiting for the cache.
	if s.cacheErr != nil {
		return 0, s.cacheErr
	}

	dataStart := int(s.offset) - s.cacheOffset
	dataEnd := dataStart + len(p)
	// If the read request extends beyond the cache, truncate it to include
	// only up to where the cache ends.
	if dataEnd > s.cacheOffset + len(s.cache) {
		dataEnd = s.cacheOffset + len(s.cache)
	}
	copy(p, s.cache[dataStart:dataEnd])
	s.offset += int64(dataEnd-dataStart)
	s.managedFillCache() // Now that some data is consumed, fetch more data.
	return dataEnd-dataStart, nil
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
