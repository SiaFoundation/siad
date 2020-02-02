package renter

import (
	"bytes"
	"io"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
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
		staticFile *siafile.Snapshot
		offset     int64
		r          *Renter

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
		// Multiple asynchronous calls to fill the cache may be sent out at
		// once. To prevent race conditions, the 'cacheActive' channel is used
		// to ensure that only one instance of 'threadedFillCache' is running at
		// a time. If another instance of 'threadedFillCache' is active, the new
		// call will immediately return.
		cache                   []byte
		activateCache           chan struct{}
		cacheOffset             int64
		cacheReady              chan struct{}
		staticDisableLocalFetch bool
		readErr                 error
		targetCacheSize         int64

		// Mutex to protect the offset variable, and all of the cacheing
		// variables.
		mu sync.Mutex
	}
)

// managedFillCache will determine whether or not the cache of the streamer
// needs to be filled, and if it does it will add data to the streamer.
func (s *streamer) managedFillCache() bool {
	// Before creating a download request to fill out the cache, check whether
	// the cache is actually in need of being filled. The cache will only fill
	// if the current reader approaching the point of running out of data.
	s.mu.Lock()
	partialDownloadsSupported := s.staticFile.ErasureCode().SupportsPartialEncoding()
	chunkSize := s.staticFile.ChunkSize()
	cacheOffset := int64(s.cacheOffset)
	streamOffset := s.offset
	cacheLen := int64(len(s.cache))
	streamReadErr := s.readErr
	fileSize := int64(s.staticFile.Size())
	targetCacheSize := s.targetCacheSize
	s.mu.Unlock()
	// If there has been a read error in the stream, abort.
	if streamReadErr != nil {
		return false
	}
	// Check whether the cache has reached the end of the file and also the
	// streamOffset is contained within the cache. If so, no updates are needed.
	if cacheOffset <= streamOffset && cacheOffset+cacheLen == fileSize {
		return false
	}
	// If partial downloads are supported and more than half of the target cache
	// size is remaining, then no fetching is required.
	if partialDownloadsSupported && cacheOffset <= streamOffset && streamOffset < (cacheOffset+cacheLen-(targetCacheSize/2)) {
		return false
	}
	// If partial downloads are not supported, the full chunk containing the
	// current offset should be the cache. If the cache is the full chunk that
	// contains current offset, then nothing needs to be done as the cache is
	// already prepared.
	//
	// This should be functionally nearly identical to the previous cache that
	// we were using which has since been disabled.
	if !partialDownloadsSupported && cacheOffset <= streamOffset && streamOffset < cacheOffset+cacheLen && cacheLen > 0 {
		return false
	}

	// Defer a function to rotate out the cacheReady channel, to notify all
	// calls blocking for more cache that more data is now available.
	defer func() {
		s.mu.Lock()
		close(s.cacheReady)
		s.cacheReady = make(chan struct{})
		s.mu.Unlock()
	}()

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
		fetchLen = targetCacheSize - (cacheOffset + cacheLen - streamOffset)
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
		disableLocalFetch: s.staticDisableLocalFetch,
		file:              s.staticFile,

		latencyTarget: 50 * time.Millisecond, // TODO: low default until full latency support is added.
		length:        uint64(fetchLen),
		needsMemory:   true,
		offset:        uint64(fetchOffset),
		overdrive:     5,    // TODO: high default until full overdrive support is added.
		priority:      1000, // TODO: high default until full priority support is added.
	})
	if err != nil {
		closeErr := ddw.Close()
		s.mu.Lock()
		readErr := errors.Compose(s.readErr, err, closeErr)
		s.readErr = readErr
		s.mu.Unlock()
		s.r.log.Println("Error downloading for stream file:", readErr)
		return false
	}
	// Register some cleanup for when the download is done.
	d.OnComplete(func(_ error) error {
		// close the destination buffer to avoid deadlocks.
		return ddw.Close()
	})
	// Start the download.
	if err := d.Start(); err != nil {
		return false
	}
	// Block until the download has completed.
	select {
	case <-d.completeChan:
		err := d.Err()
		if err != nil {
			completeErr := errors.AddContext(err, "download failed")
			s.mu.Lock()
			readErr := errors.Compose(s.readErr, completeErr)
			s.readErr = readErr
			s.mu.Unlock()
			s.r.log.Println("Error during stream download:", readErr)
			return false
		}
	case <-s.r.tg.StopChan():
		stopErr := errors.New("download interrupted by shutdown")
		s.mu.Lock()
		readErr := errors.Compose(s.readErr, stopErr)
		s.readErr = readErr
		s.mu.Unlock()
		s.r.log.Debugln(stopErr)
		return false
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
	streamOffsetInTail := streamOffsetInCache && s.offset >= s.cacheOffset+(cacheLen/4)+(cacheLen/2)
	targetCacheUnderLimit := s.targetCacheSize < maxStreamerCacheSize
	cacheExists := cacheLen > 0
	if cacheExists && partialDownloadsSupported && targetCacheUnderLimit && streamOffsetInTail {
		if s.targetCacheSize*2 > maxStreamerCacheSize {
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

	// Return true, indicating that this function should be called again,
	// because there may be more cache that has been requested or used since the
	// previous request.
	return true
}

// threadedFillCache is a background thread that keeps the cache full as data is
// read out of the cache. The Read and Seek functions have access to a channel
// that they can use to signal that the cache should be refilled. To ensure that
// the cache is always being filled, 'managedFillCache' will return a value
// indicating whether it should be called again after completion based on
// whether the cache was emptied further since the previous call.
func (s *streamer) threadedFillCache() {
	// Add this thread to the renter's threadgroup.
	err := s.r.tg.Add()
	if err != nil {
		s.r.log.Debugln("threadedFillCache terminating early because renter has stopped")
	}
	defer s.r.tg.Done()

	// Kick things off by filling the cache for the first time.
	fetchMore := s.managedFillCache()
	for fetchMore {
		fetchMore = s.managedFillCache()
	}

	for {
		// Block until receiving notice that the cache needs to be updated,
		// shutting down if a shutdown signal is received.
		select {
		case <-s.activateCache:
		case <-s.r.tg.StopChan():
			return
		}

		// Update the cache. Sometimes the cache will know that it is already
		// out of date by the time it is returning, in those cases call the
		// function again.
		fetchMore = s.managedFillCache()
		for fetchMore {
			fetchMore = s.managedFillCache()
		}
	}
}

// Close closes the streamer.
func (s *streamer) Close() error {
	return nil
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
		twiceReadLen := int64(len(p) * 2)
		if s.targetCacheSize < twiceReadLen {
			if twiceReadLen > maxStreamerCacheSize {
				s.targetCacheSize = maxStreamerCacheSize
			} else {
				s.targetCacheSize = twiceReadLen
			}
		}

		// Check if the cache contains data that we are interested in. If so,
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

	// Now that data has been consumed, request more data.
	select {
	case s.activateCache <- struct{}{}:
	default:
	}

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
	// If the Seek is a no-op, do not invalidate the cache.
	if newOffset == s.offset {
		return 0, nil
	}

	// Reset the target cache size upon seek to be the default again. This is in
	// place because some programs will rapidly consume the cache to build up
	// their own buffer. This can result in the cache growing very large, which
	// hurts seek times. By resetting the cache size upon seek, we ensure that
	// the user gets a consistent experience when seeking. In a perfect world,
	// we'd have an easy way to measure the bitrate of the file being streamed,
	// so that we could set a target cache size according to that, but at the
	// moment we don't have an easy way to get that information.
	s.targetCacheSize = initialStreamerCacheSize

	// Update the offset of the stream and immediately send a thread to update
	// the cache.
	s.offset = newOffset

	// Now that data has been consumed, request more data.
	select {
	case s.activateCache <- struct{}{}:
	default:
	}

	return newOffset, nil
}

// Streamer creates a modules.Streamer that can be used to stream downloads from
// the sia network.
func (r *Renter) Streamer(siaPath modules.SiaPath, disableLocalFetch bool) (string, modules.Streamer, error) {
	if err := r.tg.Add(); err != nil {
		return "", nil, err
	}
	defer r.tg.Done()

	// Lookup the file associated with the nickname.
	node, err := r.staticFileSystem.OpenSiaFile(siaPath)
	if err != nil {
		return "", nil, err
	}
	defer node.Close()

	// Create the streamer
	snap, err := node.Snapshot(siaPath)
	if err != nil {
		return "", nil, err
	}
	s := r.managedStreamer(snap, disableLocalFetch)
	return siaPath.String(), s, nil
}

// StreamerByNode will open a streamer for the renter, taking a FileNode as
// input instead of a siapath. This is important for fuse, which has filenodes
// that could be getting renamed before the streams are opened.
func (r *Renter) StreamerByNode(node *filesystem.FileNode, disableLocalFetch bool) (modules.Streamer, error) {
	if err := r.tg.Add(); err != nil {
		return nil, err
	}
	defer r.tg.Done()

	// Grab the current SiaPath of the FileNode and then create a snapshot.
	sp := r.staticFileSystem.FileSiaPath(node)
	snap, err := node.Snapshot(sp)
	if err != nil {
		return nil, err
	}
	s := r.managedStreamer(snap, disableLocalFetch)
	return s, nil
}

// managedStreamer creates a streamer from a siafile snapshot and starts filling
// its cache.
func (r *Renter) managedStreamer(snapshot *siafile.Snapshot, disableLocalFetch bool) modules.Streamer {
	s := &streamer{
		staticFile: snapshot,
		r:          r,

		activateCache:           make(chan struct{}),
		cacheReady:              make(chan struct{}),
		staticDisableLocalFetch: disableLocalFetch,
		targetCacheSize:         initialStreamerCacheSize,
	}
	go s.threadedFillCache()
	return s
}
