package renter

// TODO: Currently the access time is only updated when a file is closed, is
// that correct behavior? Should the access time be updated upon openeing the
// file? Upon each read?

import (
	"bytes"
	"io"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

type (
	// streamer is a modules.Streamer that can be used to stream downloads from
	// the sia network.
	streamer struct {
		// Reader variables. The snapshot is a snapshot of the file as it
		// existed when it was opened, something that we do to give the streamer
		// a consistent view of the file even if the file is being actively
		// updated. Having this snapshot also isolates the reader from events
		// such as name changes and deletions.
		//
		// We also keep the full file entry as it allows us to update metadata
		// items in the file such as the access time.
		staticFile      *siafile.Snapshot
		staticFileEntry *siafile.SiaFileSetEntry
		offset          int64
		r               *Renter

		// The cache itself is a []byte that is managed by threadedFillCache. The
		// 'cacheOffset' indicates the starting location of the cache within the
		// file, and all of the data in the []byte will be the actual file data
		// that follows that offset. If the cache is empty, the length will be
		// 0.
		//
		// Because the cache gets filled asynchronously, errors need to be
		// recorded and then delivered to the user later. The errors get stored
		// in readErr.
		//
		// cacheReady is a rotating channel which is used to signal to threads
		// that the cache has been updated. When a Read call is made, the first
		// action required is to grab a lock and then check if the cache has the
		// requested data. If not, while still holding the lock the Read thread
		// will grab a copy of cacheReady, and then release the lock. When the
		// threadedFillCache thread has finished updating the cache, the thread
		// will grab the lock and then the cacheReady channel will be closed and
		// replaced with a new channel. This allows any number of Read threads
		// to simultaneously block while waiting for cacheReady to be closed,
		// and once cacheReady is closed they know to check the cache again.
		//
		// Multiple asyncrhonous calls to fill the cache may be sent out at
		// once. To prevent race conditions, the 'cacheActive' channel is used
		// to ensure that only one instance of 'threadedFillCache' is running at
		// a time. If another instance of 'threadedFillCache' is active, the new
		// call will immediately return.
		cache           []byte
		cacheActive     chan struct{}
		cacheOffset     int64
		cacheReady      chan struct{}
		readErr         error
		targetCacheSize int64

		// Mutex to protect the offset variable, and all of the cacheing
		// variables.
		mu sync.Mutex
	}
)

// threadedFillCache is a method to fill or refill the cache for the streamer.
// The function will self-enforce that only one thread is running at a time.
// While the thread is running, multiple calls to 'Read' may happen, which will
// drain the cache and require additional filling. To ensure that the cache is
// always being filled if there is a need, threadedFillCache will finish by
// calling itself in a new goroutine if it updated the cache at all.
//
// TODO: This current code will potentially fetch more than one chunk at a time,
// and will potentially fetch a small amount of data that crosses chunk
// boundaries. Is that fine?
func (s *streamer) threadedFillCache() {
	// Before grabbing the cacheActive object, check whether this thread is
	// required to exist. This check needs to be made before checking the
	// cacheActive because threadedFillCache recursively calls itself after
	// grabbing the cacheActive object, so some base case is needed to guarantee
	// termination.
	s.mu.Lock()
	partialDownloadsSupported := s.staticFile.ErasureCode().SupportsPartialEncoding()
	chunkSize := s.staticFile.ChunkSize()
	cacheOffset := int64(s.cacheOffset)
	streamOffset := s.offset
	cacheLen := int64(len(s.cache))
	streamReadErr := s.readErr
	fileSize := int64(s.staticFile.Size())
	s.mu.Unlock()
	// If there has been a read error in the stream, abort.
	if streamReadErr != nil {
		return
	}
	// Check whether the cache has reached the end of the file and also the
	// streamOffset is contained within the cache. If so, no updates are needed.
	if cacheOffset <= streamOffset && cacheOffset+cacheLen == fileSize {
		return
	}
	// If partial downloads are supported and the stream offset is in the first
	// half of the cache, then no fetching is required.
	//
	// An extra check that there is any data in the cache needs to be made so
	// that the cache fill function runs immediately after initialization.
	if partialDownloadsSupported && cacheOffset <= streamOffset && streamOffset-cacheOffset < cacheLen / 2 {
		return
	}
	// If partial downloads are not supported, the full chunk containing the
	// current offset should be the cache. If the cache is the full chunk that
	// contains current offset, then nothing needs to be done as the cache is
	// already prepared.
	//
	// This should be functionally nearly identical to the previous cache that
	// we were using which has since been disabled.
	if !partialDownloadsSupported && cacheOffset <= streamOffset && streamOffset < cacheOffset+cacheLen && cacheLen > 0 {
		return
	}

	// Check cacheActive for an object. If no object exists, another
	// threadedFillCache thread is running, so this thread should terminate
	// immediately. The other thread is guaranteed to call 'threadedFillCache'
	// again upon termination (due the defer statement immediately following the
	// acquisition of the cacheActive object), meaning that any new need to
	// update the cache will eventually be satisfied.
	select {
	case <-s.cacheActive:
	default:
		return
	}
	// NOTE: The ordering here is delicate. After the function returns, the
	// first thing that happens is that the cacheReady channel is rotated, so
	// any Read calls blocking for a cache update know to check the cache for
	// the data they want.
	//
	// After the Read channels are unfrozen, the cacheActive object needs to be
	// returned so that the next fillCache thread is able to grab the object.
	//
	// Once the blocking read calls have been unstuck and the cacheActive object
	// has been returned, then we issue another call to threadedFillCache to add
	// more data to the cache if necessary. This has to go last so that when it
	// requests the cacheActive object, it is guaranteed that either some other
	// fillCache thread grabbed the object, or the object is there. This is
	// because fillCache threads won't block until the object is available, if
	// the object is not available they will terminate immediately.
	//
	// Because defer statements are a stack, the defers are set up in the
	// reverse order than how the functions need to be activated.
	defer func() {
		go s.threadedFillCache()
	}()
	defer func() {
		s.cacheActive <- struct{}{}
	}()
	defer func() {
		s.mu.Lock()
		close(s.cacheReady)
		s.cacheReady = make(chan struct{})
		s.mu.Unlock()
	}()

	// Re-fetch the variables important to gathering cache, in case they have
	// changed since the initial check.
	s.mu.Lock()
	partialDownloadsSupported = s.staticFile.ErasureCode().SupportsPartialEncoding()
	chunkSize = s.staticFile.ChunkSize()
	cacheOffset = int64(s.cacheOffset)
	streamOffset = s.offset
	cacheLen = int64(len(s.cache))
	targetCacheSize := s.targetCacheSize
	streamReadErr = s.readErr
	fileSize = int64(s.staticFile.Size())
	s.mu.Unlock()

	// Check one more time for the conditions that indicate no cache update is
	// necessary, given that we have released and re-grabbed the lock since the
	// previous check.
	//
	// An extra check is needed to see if there is any data in the cache. If
	// there is no data in the cache, this is the first call to fillCache since
	// initialization.
	if streamReadErr != nil {
		return
	}
	if cacheOffset <= streamOffset && cacheOffset+cacheLen == fileSize {
		return
	}
	if partialDownloadsSupported && cacheOffset <= streamOffset && streamOffset-cacheOffset < cacheLen / 2 {
		return
	}
	if !partialDownloadsSupported && cacheOffset <= streamOffset && streamOffset < cacheOffset+cacheLen && cacheLen > 0 {
		return
	}

	// Determine what data needs to be fetched.
	//
	// If there is no support for partial downloads, a whole chunk needs to be
	// fetched, and the cache will be set equal to the chunk that currently
	// contains the stream offset. This is because that amount of data will need
	// to be fetched anyway, so we may as well use the full amount of data in
	// the cache.
	//
	// If there is support for partial downloads but the stream offset is not
	// contained within the existing cache, we need to fully replace the cache.
	// At initialization, this will be the case (cacheLen of 0 cannot contain
	// the stream offset byte within it, because it contains no bytes at all),
	// so a check for 0-size cache is made. The full cache replacement will
	// consist of a partial download the size of the cache starting from the
	// stream offset.
	//
	// The final case is that the stream offset is contained within the current
	// cache, but the stream offset is not the first byte of the cache. This
	// means that we need to drop all of the bytes prior to the stream offset
	// and then more bytes so that the cache remains the same size.
	var fetchOffset, fetchLen int64
	if !partialDownloadsSupported {
		// Request a full chunk of data.
		chunkIndex, _ := s.staticFile.ChunkIndexByOffset(uint64(streamOffset))
		fetchOffset = int64(chunkIndex * chunkSize)
		fetchLen = int64(chunkSize)
	} else if streamOffset < cacheOffset || streamOffset >= cacheOffset+cacheLen {
		// Grab enough data to fill the cache entirely starting from the current
		// stream offset.
		fetchOffset = streamOffset
		fetchLen = targetCacheSize
	} else {
		// Set the fetch offset to the end of the current cache, and set the
		// length equal to the number of bytes that the streamOffset has already
		// consumed, so that the cache remains the same size after we drop all
		// of the consumed bytes and extend the cache with new data.
		fetchOffset = cacheOffset + cacheLen
		fetchLen = targetCacheSize - (streamOffset - cacheOffset)
	}

	// Finally, check if the fetchOffset and fetchLen goes beyond the boundaries
	// of the file. If so, the fetchLen will be truncated so that the cache only
	// goes up to the end of the file.
	if fetchOffset+fetchLen > fileSize {
		fetchLen = fileSize - fetchOffset
	}

	// Perform the actual download.
	buffer := bytes.NewBuffer([]byte{})
	ddw := newDownloadDestinationWriter(buffer)
	d, err := s.r.managedNewDownload(downloadParams{
		destination:       ddw,
		destinationType:   destinationTypeSeekStream,
		destinationString: "httpresponse",
		file:              s.staticFile,

		latencyTarget: 50 * time.Millisecond, // TODO: low default until full latency suport is added.
		length:        uint64(fetchLen),
		needsMemory:   true,
		offset:        uint64(fetchOffset),
		overdrive:     5,    // TODO: high default until full overdrive support is added.
		priority:      1000, // TODO: high default until full priority support is added.
	})
	if err != nil {
		closeErr := ddw.Close()
		s.mu.Lock()
		s.readErr = errors.Compose(s.readErr, err, closeErr)
		s.mu.Unlock()
		return
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
		err := d.Err()
		if err != nil {
			completeErr := errors.AddContext(err, "download failed")
			s.mu.Lock()
			s.readErr = errors.Compose(s.readErr, completeErr)
			s.mu.Unlock()
			return
		}
	case <-s.r.tg.StopChan():
		stopErr := errors.New("download interrupted by shutdown")
		s.mu.Lock()
		s.readErr = errors.Compose(s.readErr, stopErr)
		s.mu.Unlock()
	}

	// Update the cache.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Before updating the cache, check if the stream has caught up in the
	// current cache. If the stream has caught up, the cache is not filling fast
	// enough and the target cache size should be increased.
	//
	// streamOffsetInTail checks if the stream offset is in the final quarter of
	// the cache. If it is, we consider the cache to be not filling fast enough,
	// and we extend the size of the cache.
	//
	// A final check for cacheExists is performed, because if there currently is
	// no cache at all, this must be the first fetch, and there is no reason to
	// extend the cache size.
	cacheLen = int64(len(s.cache))
	streamOffsetInCache := s.cacheOffset <= s.offset && s.offset <= s.cacheOffset+cacheLen // NOTE: it's '<=' so that we also count being 1 byte beyond the cache
	streamOffsetInTail := streamOffsetInCache && s.offset >= s.cacheOffset + (cacheLen / 4) + (cacheLen / 2)
	targetCacheUnderLimit := s.targetCacheSize < maxStreamerCacheSize
	cacheExists := cacheLen > 0
	if cacheExists && partialDownloadsSupported && targetCacheUnderLimit && streamOffsetInTail {
		if s.targetCacheSize * 2 > maxStreamerCacheSize {
			s.targetCacheSize = maxStreamerCacheSize
		} else {
			s.targetCacheSize *= 2
		}
	}

	// Update the cache based on whether the entire cache needs to be replaced
	// or whether only some of the cache is being replaced. The whole cache
	// needs to be replaced in the even that partial downloads are not
	// supported, and also in the event that the stream offset is complete
	// outside the previous cache.
	if !partialDownloadsSupported || streamOffset >= cacheOffset+cacheLen || streamOffset < cacheOffset {
		s.cache = buffer.Bytes()
		s.cacheOffset = fetchOffset
	} else {
		s.cache = s.cache[streamOffset-cacheOffset:]
		s.cache = append(s.cache, buffer.Bytes()...)
		s.cacheOffset = streamOffset
	}
}

// Close closes the streamer and let's the fileSet know that the SiaFile is no
// longer in use.
func (s *streamer) Close() error {
	err1 := s.staticFileEntry.SiaFile.UpdateAccessTime()
	err2 := s.staticFileEntry.Close()
	return errors.Compose(err1, err2)
}

// Read will check the stream cache for the data that is being requested. If the
// data is fully or partially there, Read will return what data is available
// without error. If the data is not there, Read will issue a call to fill the
// cache and then block until the data is at least partially available.
func (s *streamer) Read(p []byte) (int, error) {
	// Wait in a loop until the requested data is available, or until an error
	// is recovered. The loop needs to release the lock between iterations, but
	// the lock that it grabs needs to be held after the loops termination if
	// the right conditions are met, resulting in an ugly/complex locking
	// strategy.
	for {
		// Grab the lock and check that the cache has data which we want. If the
		// cache does have data that we want, we will keep the lock and exit the
		// loop. If there's an error, we will drop the lock and return the
		// error. If the cache does not have the data we want but there is no
		// error, we will drop the lock and spin up a thread to fill the cache,
		// and then block until the cache has been updated.
		s.mu.Lock()
		// Get the file's size and check for EOF.
		fileSize := int64(s.staticFile.Size())
		if s.offset >= fileSize {
			s.mu.Unlock()
			return 0, io.EOF
		}

		// If there is a cache error, drop the lock and return. This check
		// should happen before anything else.
		if s.readErr != nil {
			err := s.readErr
			s.mu.Unlock()
			return 0, err
		}

		// Do a check that the cache size is at least twice as large as the read
		// size, to ensure that data is being fetched sufficiently far in
		// advance.
		twiceReadLen := int64(len(p)*2)
		if s.targetCacheSize < twiceReadLen {
			if twiceReadLen > maxStreamerCacheSize {
				s.targetCacheSize = maxStreamerCacheSize
			} else {
				s.targetCacheSize = twiceReadLen
			}
		}

		// Check if the cache continas data that we are interested in. If so,
		// break out of the cache-fetch loop while still holding the lock.
		if s.cacheOffset <= s.offset && s.offset < s.cacheOffset+int64(len(s.cache)) {
			break
		}

		// There is no error, but the data that we want is also unavailable.
		// Grab the cacheReady channel to detect when the cache has been
		// updated, and then drop the lock and block until there has been a
		// cache update.
		//
		// Notably, it should not be necessary to spin up a new cache thread.
		// There are four conditions which may cause the stream offset to be
		// located outside of the existing cache, and all conditions will result
		// with a thread being spun up regardless. The first condition is
		// initialization, where no cache exists. A fill cache thread is spun up
		// upon initialization. The second condition is after a Seek, which may
		// move the offset outside of the current cache. The call to Seek also
		// spins up a cache filling thread. The third condition is after a read,
		// which adjusts the stream offset. A new cache fill thread gets spun up
		// in this case as well, immediately after the stream offset is
		// adjusted. Finally, there is the case where a cache fill thread was
		// spun up, but then immediately spun down due to another cache fill
		// thread already running. But this case is handled as well, because a
		// cache fill thread will spin up another cache fill thread when it
		// finishes specifically to cover this case.
		cacheReady := s.cacheReady
		s.mu.Unlock()
		<-cacheReady

		// Upon iterating, the lock is not held, so the call to grab the lock at
		// the top of the function should not cause a deadlock.
	}
	// This code should only be reachable if the lock is still being held and
	// there is also data in the cache for us. Defer releasing the lock.
	defer s.mu.Unlock()

	dataStart := int(s.offset - s.cacheOffset)
	dataEnd := dataStart + len(p)
	// If the read request extends beyond the cache, truncate it to include
	// only up to where the cache ends.
	if dataEnd > len(s.cache) {
		dataEnd = len(s.cache)
	}
	copy(p, s.cache[dataStart:dataEnd])
	s.offset += int64(dataEnd - dataStart)
	go s.threadedFillCache() // Now that some data is consumed, fetch more data.
	return dataEnd - dataStart, nil
}

// Seek sets the offset for the next Read to offset, interpreted
// according to whence: SeekStart means relative to the start of the file,
// SeekCurrent means relative to the current offset, and SeekEnd means relative
// to the end. Seek returns the new offset relative to the start of the file
// and an error, if any.
func (s *streamer) Seek(offset int64, whence int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	// Update the offset of the stream and immediately send a thread to update
	// the cache.
	s.offset = newOffset
	go s.threadedFillCache()
	return newOffset, nil
}

// Streamer creates a modules.Streamer that can be used to stream downloads from
// the sia network.
//
// TODO: Why do we return entry.SiaPath() as a part of the call that opens the
// stream?
func (r *Renter) Streamer(siaPath string) (string, modules.Streamer, error) {
	// Lookup the file associated with the nickname.
	entry, err := r.staticFileSet.Open(siaPath)
	if err != nil {
		return "", nil, err
	}
	defer entry.Close()

	// Create the streamer
	s := &streamer{
		staticFile:      entry.Snapshot(),
		staticFileEntry: entry,
		r:               r,

		cacheActive:     make(chan struct{}, 1),
		cacheReady:      make(chan struct{}),
		targetCacheSize: initialStreamerCacheSize,
	}

	// Put an object into the cacheActive to indicate that there is no cache
	// thread running at the moment, and then spin up a cache thread to fill the
	// cache (the cache thread will consume the cacheActive object itself).
	s.cacheActive <- struct{}{}
	go s.threadedFillCache()

	return entry.SiaPath(), s, nil
}
