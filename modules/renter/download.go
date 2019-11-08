package renter

import (
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/types"
)

type (
	// A download is a file download that has been queued by the renter.
	download struct {
		// Data progress variables.
		atomicDataReceived         uint64 // Incremented as data completes, will stop at 100% file progress.
		atomicTotalDataTransferred uint64 // Incremented as data arrives, includes overdrive, contract negotiation, etc.

		// Other progress variables.
		chunksRemaining uint64        // Number of chunks whose downloads are incomplete.
		completeChan    chan struct{} // Closed once the download is complete.
		err             error         // Only set if there was an error which prevented the download from completing.

		// downloadCompleteFunc is a slice of functions which are called when
		// completeChan is closed.
		downloadCompleteFuncs []func(error) error

		// Timestamp information.
		endTime         time.Time // Set immediately before closing 'completeChan'.
		staticStartTime time.Time // Set immediately when the download object is created.

		// Basic information about the file/download.
		destination           downloadDestination
		destinationString     string             // The string reported to the user to indicate the download's destination.
		staticDestinationType string             // "memory buffer", "http stream", "file", etc.
		staticLength          uint64             // Length to download starting from the offset.
		staticOffset          uint64             // Offset within the file to start the download.
		staticSiaPath         modules.SiaPath    // The path of the siafile at the time the download started.
		staticUID             modules.DownloadID // unique identifier for the download

		staticParams downloadParams

		// Retrieval settings for the file.
		staticLatencyTarget time.Duration // In milliseconds. Lower latency results in lower total system throughput.
		staticOverdrive     int           // How many extra pieces to download to prevent slow hosts from being a bottleneck.
		staticPriority      uint64        // Downloads with higher priority will complete first.

		// Utilities.
		r  *Renter    // The renter that was used to create the download.
		mu sync.Mutex // Unique to the download object.
	}

	// downloadParams is the set of parameters to use when downloading a file.
	downloadParams struct {
		destination       downloadDestination // The place to write the downloaded data.
		destinationType   string              // "file", "buffer", "http stream", etc.
		destinationString string              // The string to report to the user for the destination.
		disableLocalFetch bool                // Whether or not the file can be fetched from disk if available.
		file              *siafile.Snapshot   // The file to download.

		latencyTarget time.Duration // Workers above this latency will be automatically put on standby initially.
		length        uint64        // Length of download. Cannot be 0.
		needsMemory   bool          // Whether new memory needs to be allocated to perform the download.
		offset        uint64        // Offset within the file to start the download. Must be less than the total filesize.
		overdrive     int           // How many extra pieces to download to prevent slow hosts from being a bottleneck.
		priority      uint64        // Files with a higher priority will be downloaded first.
	}
)

// managedCancel cancels a download by marking it as failed.
func (d *download) managedCancel() {
	d.managedFail(modules.ErrDownloadCancelled)
}

// managedFail will mark the download as complete, but with the provided error.
// If the download has already failed, the error will be updated to be a
// concatenation of the previous error and the new error.
func (d *download) managedFail(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// If the download is already complete, extend the error.
	complete := d.staticComplete()
	if complete && d.err != nil {
		return
	} else if complete && d.err == nil {
		d.r.log.Critical("download is marked as completed without error, but then managedFail was called with err:", err)
		return
	}

	// Mark the download as complete and set the error.
	d.err = err
	d.markComplete()
}

// markComplete is a helper method which closes the completeChan and and
// executes the downloadCompleteFuncs. The completeChan should always be closed
// using this method.
func (d *download) markComplete() {
	// Avoid calling markComplete multiple times. In a production build
	// build.Critical won't panic which is fine since we set
	// downloadCompleteFunc to nil after executing them. We still don't want to
	// close the completeChan again though to avoid a crash.
	if d.staticComplete() {
		build.Critical("Can't call markComplete multiple times")
	} else {
		defer close(d.completeChan)
	}
	// Execute the downloadCompleteFuncs before closing the channel. This gives
	// the initiator of the download the nice guarantee that waiting for the
	// completeChan to be closed also means that the downloadCompleteFuncs are
	// done.
	var err error
	for _, f := range d.downloadCompleteFuncs {
		err = errors.Compose(err, f(d.err))
	}
	// Log potential errors.
	if err != nil {
		d.r.log.Println("Failed to execute at least one downloadCompleteFunc", err)
	}
	// Set downloadCompleteFuncs to nil to avoid executing them multiple times.
	d.downloadCompleteFuncs = nil
}

// onComplete registers a function to be called when the download is completed.
// This can either mean that the download succeeded or failed. The registered
// functions are executed in the same order as they are registered and waiting
// for the download's completeChan to be closed implies that the registered
// functions were executed.
func (d *download) onComplete(f func(error) error) {
	select {
	case <-d.completeChan:
		if err := f(d.err); err != nil {
			d.r.log.Println("Failed to execute downloadCompleteFunc", err)
		}
		return
	default:
	}
	d.downloadCompleteFuncs = append(d.downloadCompleteFuncs, f)
}

// staticComplete is a helper function to indicate whether or not the download
// has completed.
func (d *download) staticComplete() bool {
	select {
	case <-d.completeChan:
		return true
	default:
		return false
	}
}

// Err returns the error encountered by a download, if it exists.
func (d *download) Err() (err error) {
	d.mu.Lock()
	err = d.err
	d.mu.Unlock()
	return err
}

// OnComplete registers a function to be called when the download is completed.
// This can either mean that the download succeeded or failed. The registered
// functions are executed in the same order as they are registered and waiting
// for the download's completeChan to be closed implies that the registered
// functions were executed.
func (d *download) OnComplete(f func(error) error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onComplete(f)
}

// UID returns the unique identifier of the download.
func (d *download) UID() modules.DownloadID {
	return d.staticUID
}

// Download creates a file download using the passed parameters and blocks until
// the download is finished. The download needs to be started by calling the
// returned method.
func (r *Renter) Download(p modules.RenterDownloadParameters) (modules.DownloadID, func() error, error) {
	if err := r.tg.Add(); err != nil {
		return "", nil, err
	}
	defer r.tg.Done()
	d, err := r.managedDownload(p)
	if err != nil {
		return "", nil, err
	}
	return d.UID(), func() error {
		// Start download.
		if err := d.Start(); err != nil {
			return err
		}
		// Block until the download has completed
		select {
		case <-d.completeChan:
			return d.Err()
		case <-r.tg.StopChan():
			return errors.New("download interrupted by shutdown")
		}
	}, nil
}

// DownloadAsync creates a file download using the passed parameters without
// blocking until the download is finished. The download needs to be started
// using the method returned by DownloadAsync. DownloadAsync also accepts an
// optional input function which will be registered to be called when the
// download is finished.
func (r *Renter) DownloadAsync(p modules.RenterDownloadParameters, f func(error) error) (id modules.DownloadID, start func() error, cancel func(), err error) {
	if err := r.tg.Add(); err != nil {
		return "", nil, nil, err
	}
	defer r.tg.Done()
	d, err := r.managedDownload(p)
	if err != nil {
		return "", nil, nil, err
	}
	if f != nil {
		d.onComplete(f)
	}
	return d.UID(), func() error {
		return d.Start()
	}, d.managedCancel, nil
}

// managedDownload performs a file download using the passed parameters and
// returns the download object and an error that indicates if the download
// setup was successful.
func (r *Renter) managedDownload(p modules.RenterDownloadParameters) (*download, error) {
	// Lookup the file associated with the nickname.
	entry, err := r.staticFileSet.Open(p.SiaPath)
	if err != nil {
		return nil, err
	}
	defer entry.Close()
	defer entry.UpdateAccessTime()

	// Validate download parameters.
	isHTTPResp := p.Httpwriter != nil
	if p.Async && isHTTPResp {
		return nil, errors.New("cannot async download to http response")
	}
	if isHTTPResp && p.Destination != "" {
		return nil, errors.New("destination cannot be specified when downloading to http response")
	}
	if !isHTTPResp && p.Destination == "" {
		return nil, errors.New("destination not supplied")
	}
	if p.Destination != "" && !filepath.IsAbs(p.Destination) {
		return nil, errors.New("destination must be an absolute path")
	}
	if p.Offset == entry.Size() && entry.Size() != 0 {
		return nil, errors.New("offset equals filesize")
	}
	// Sentinel: if length == 0, download the entire file.
	if p.Length == 0 {
		if p.Offset > entry.Size() {
			return nil, errors.New("offset cannot be greater than file size")
		}
		p.Length = entry.Size() - p.Offset
	}
	// Check whether offset and length is valid.
	if p.Offset < 0 || p.Offset+p.Length > entry.Size() {
		return nil, fmt.Errorf("offset and length combination invalid, max byte is at index %d", entry.Size()-1)
	}

	// Instantiate the correct downloadWriter implementation.
	var dw downloadDestination
	var destinationType string
	if isHTTPResp {
		dw = newDownloadDestinationWriter(p.Httpwriter)
		destinationType = "http stream"
	} else {
		osFile, err := os.OpenFile(p.Destination, os.O_CREATE|os.O_WRONLY, entry.Mode())
		if err != nil {
			return nil, err
		}
		dw = &downloadDestinationFile{deps: r.deps, f: osFile, staticChunkSize: int64(entry.ChunkSize())}
		destinationType = "file"
	}

	// If the destination is a httpWriter, we set the Content-Length in the
	// header.
	if isHTTPResp {
		w, ok := p.Httpwriter.(http.ResponseWriter)
		if ok {
			w.Header().Set("Content-Length", fmt.Sprint(p.Length))
		}
	}

	// Prepare snapshot.
	snap, err := entry.Snapshot()
	if err != nil {
		return nil, err
	}
	// Create the download object.
	d, err := r.managedNewDownload(downloadParams{
		destination:       dw,
		destinationType:   destinationType,
		destinationString: p.Destination,
		disableLocalFetch: p.DisableDiskFetch,
		file:              snap,

		latencyTarget: 25e3 * time.Millisecond, // TODO: high default until full latency support is added.
		length:        p.Length,
		needsMemory:   true,
		offset:        p.Offset,
		overdrive:     3, // TODO: moderate default until full overdrive support is added.
		priority:      5, // TODO: moderate default until full priority support is added.
	})
	if closer, ok := dw.(io.Closer); err != nil && ok {
		// If the destination can be closed we do so.
		return nil, errors.Compose(err, closer.Close())
	} else if err != nil {
		return nil, err
	}

	// Register some cleanup for when the download is done.
	d.OnComplete(func(_ error) error {
		// close the destination if possible.
		if closer, ok := dw.(io.Closer); ok {
			return closer.Close()
		}
		// sanity check that we close files.
		if destinationType == "file" {
			build.Critical("file wasn't closed after download")
		}
		return nil
	})

	// Add the download object to the download history if it's not a stream.
	if destinationType != destinationTypeSeekStream {
		r.downloadHistoryMu.Lock()
		r.downloadHistory[d.UID()] = d
		r.downloadHistoryMu.Unlock()
	}

	// Return the download object
	return d, nil
}

// managedNewDownload creates and initializes a download based on the provided
// parameters.
func (r *Renter) managedNewDownload(params downloadParams) (*download, error) {
	// Input validation.
	if params.file == nil {
		return nil, errors.New("no file provided when requesting download")
	}
	if params.length < 0 {
		return nil, errors.New("download length must be zero or a positive whole number")
	}
	if params.offset < 0 {
		return nil, errors.New("download offset cannot be a negative number")
	}
	if params.offset+params.length > params.file.Size() {
		return nil, errors.New("download is requesting data past the boundary of the file")
	}

	// Create the download object.
	d := &download{
		completeChan: make(chan struct{}),

		staticStartTime: time.Now(),

		destination:           params.destination,
		destinationString:     params.destinationString,
		staticDestinationType: params.destinationType,
		staticUID:             modules.DownloadID(hex.EncodeToString(fastrand.Bytes(16))),
		staticLatencyTarget:   params.latencyTarget,
		staticLength:          params.length,
		staticOffset:          params.offset,
		staticOverdrive:       params.overdrive,
		staticSiaPath:         params.file.SiaPath(),
		staticPriority:        params.priority,

		r:            r,
		staticParams: params,
	}

	// Update the endTime of the download when it's done. Also nil out the
	// destination pointer so that the garbage collector does not think any
	// memory is still being used.
	d.onComplete(func(_ error) error {
		d.endTime = time.Now()
		d.destination = nil
		return nil
	})

	return d, nil
}

// Start starts a download previously created with `managedNewDownload`.
func (d *download) Start() error {
	// Nothing more to do for 0-byte files or 0-length downloads.
	if d.staticLength == 0 {
		d.markComplete()
		return nil
	}

	// Determine which chunks to download.
	params := d.staticParams
	minChunk, minChunkOffset := params.file.ChunkIndexByOffset(params.offset)
	maxChunk, maxChunkOffset := params.file.ChunkIndexByOffset(params.offset + params.length)

	// If the maxChunkOffset is exactly 0 we need to subtract 1 chunk. e.g. if
	// the chunkSize is 100 bytes and we want to download 100 bytes from offset
	// 0, maxChunk would be 1 and maxChunkOffset would be 0. We want maxChunk
	// to be 0 though since we don't actually need any data from chunk 1.
	if maxChunk > 0 && maxChunkOffset == 0 {
		maxChunk--
	}
	// Make sure the requested chunks are within the boundaries.
	if minChunk == params.file.NumChunks() || maxChunk == params.file.NumChunks() {
		return errors.New("download is requesting a chunk that is past the boundary of the file")
	}

	// For each chunk, assemble a mapping from the contract id to the index of
	// the piece within the chunk that the contract is responsible for.
	chunkMaps := make([]map[string]downloadPieceInfo, maxChunk-minChunk+1)
	for chunkIndex := minChunk; chunkIndex <= maxChunk; chunkIndex++ {
		// Create the map.
		chunkMaps[chunkIndex-minChunk] = make(map[string]downloadPieceInfo)
		// Get the pieces for the chunk.
		pieces := params.file.Pieces(uint64(chunkIndex))
		for pieceIndex, pieceSet := range pieces {
			for _, piece := range pieceSet {
				// Sanity check - the same worker should not have two pieces for
				// the same chunk.
				_, exists := chunkMaps[chunkIndex-minChunk][piece.HostPubKey.String()]
				if exists {
					d.r.log.Println("ERROR: Worker has multiple pieces uploaded for the same chunk.", params.file.SiaPath(), chunkIndex, pieceIndex, piece.HostPubKey.String())
				}
				chunkMaps[chunkIndex-minChunk][piece.HostPubKey.String()] = downloadPieceInfo{
					index: uint64(pieceIndex),
					root:  piece.MerkleRoot,
				}
			}
		}
	}

	// Queue the downloads for each chunk.
	writeOffset := int64(0) // where to write a chunk within the download destination.
	d.chunksRemaining += maxChunk - minChunk + 1
	for i := minChunk; i <= maxChunk; i++ {
		udc := &unfinishedDownloadChunk{
			destination: params.destination,
			erasureCode: params.file.ErasureCode(),
			masterKey:   params.file.MasterKey(),

			staticChunkIndex: i,
			staticCacheID:    fmt.Sprintf("%v:%v", d.staticSiaPath, i),
			staticChunkMap:   chunkMaps[i-minChunk],
			staticChunkSize:  params.file.ChunkSize(),
			staticPieceSize:  params.file.PieceSize(),

			// TODO: 25ms is just a guess for a good default. Really, we want to
			// set the latency target such that slower workers will pick up the
			// later chunks, but only if there's a very strong chance that
			// they'll finish before the earlier chunks finish, so that they do
			// no contribute to low latency.
			//
			// TODO: There is some sane minimum latency that should actually be
			// set based on the number of pieces 'n', and the 'n' fastest
			// workers that we have.
			staticDisableDiskFetch: params.disableLocalFetch,
			staticLatencyTarget:    d.staticLatencyTarget + (25 * time.Duration(i-minChunk)), // Increase target by 25ms per chunk.
			staticNeedsMemory:      params.needsMemory,
			staticPriority:         params.priority,

			completedPieces:   make([]bool, params.file.ErasureCode().NumPieces()),
			physicalChunkData: make([][]byte, params.file.ErasureCode().NumPieces()),
			pieceUsage:        make([]bool, params.file.ErasureCode().NumPieces()),

			download:   d,
			renterFile: params.file,
		}

		// Set the fetchOffset - the offset within the chunk that we start
		// downloading from.
		if i == minChunk {
			udc.staticFetchOffset = minChunkOffset
		} else {
			udc.staticFetchOffset = 0
		}
		// Set the fetchLength - the number of bytes to fetch within the chunk
		// that we start downloading from.
		if i == maxChunk && maxChunkOffset != 0 {
			udc.staticFetchLength = maxChunkOffset - udc.staticFetchOffset
		} else {
			udc.staticFetchLength = params.file.ChunkSize() - udc.staticFetchOffset
		}
		// Set the writeOffset within the destination for where the data should
		// be written.
		udc.staticWriteOffset = writeOffset
		writeOffset += int64(udc.staticFetchLength)

		// TODO: Currently all chunks are given overdrive. This should probably
		// be changed once the hostdb knows how to measure host speed/latency
		// and once we can assign overdrive dynamically.
		udc.staticOverdrive = params.overdrive

		// Add this chunk to the chunk heap, and notify the download loop that
		// there is work to do.
		d.r.managedAddChunkToDownloadHeap(udc)
		select {
		case d.r.newDownloads <- struct{}{}:
		default:
		}
	}
	return nil
}

// DownloadByUID returns a single download from the history by it's UID.
func (r *Renter) DownloadByUID(uid modules.DownloadID) (modules.DownloadInfo, bool) {
	r.downloadHistoryMu.Lock()
	defer r.downloadHistoryMu.Unlock()
	d, exists := r.downloadHistory[uid]
	if !exists {
		return modules.DownloadInfo{}, false
	}
	return modules.DownloadInfo{
		Destination:     d.destinationString,
		DestinationType: d.staticDestinationType,
		Length:          d.staticLength,
		Offset:          d.staticOffset,
		SiaPath:         d.staticSiaPath,

		Completed:            d.staticComplete(),
		EndTime:              d.endTime,
		Received:             atomic.LoadUint64(&d.atomicDataReceived),
		StartTime:            d.staticStartTime,
		StartTimeUnix:        d.staticStartTime.UnixNano(),
		TotalDataTransferred: atomic.LoadUint64(&d.atomicTotalDataTransferred),
	}, true
}

// DownloadHistory returns the list of downloads that have been performed. Will
// include downloads that have not yet completed. Downloads will be roughly,
// but not precisely, sorted according to start time.
//
// TODO: Currently the DownloadHistory only contains downloads from this
// session, does not contain downloads that were executed for the purposes of
// repairing, and has no way to clear the download history if it gets long or
// unwieldy. It's not entirely certain which of the missing features are
// actually desirable, please consult core team + app dev community before
// deciding what to implement.
func (r *Renter) DownloadHistory() []modules.DownloadInfo {
	r.downloadHistoryMu.Lock()
	defer r.downloadHistoryMu.Unlock()

	// Get a slice of the history sorted from least recent to most recent.
	downloadHistory := make([]*download, 0, len(r.downloadHistory))
	for _, d := range r.downloadHistory {
		downloadHistory = append(downloadHistory, d)
	}
	sort.Slice(downloadHistory, func(i, j int) bool {
		return downloadHistory[i].staticStartTime.Before(downloadHistory[j].staticStartTime)
	})

	downloads := make([]modules.DownloadInfo, len(downloadHistory))
	for i := range downloadHistory {
		// Order from most recent to least recent.
		d := downloadHistory[len(r.downloadHistory)-i-1]
		d.mu.Lock() // Lock required for d.endTime only.
		downloads[i] = modules.DownloadInfo{
			Destination:     d.destinationString,
			DestinationType: d.staticDestinationType,
			Length:          d.staticLength,
			Offset:          d.staticOffset,
			SiaPath:         d.staticSiaPath,

			Completed:            d.staticComplete(),
			EndTime:              d.endTime,
			Received:             atomic.LoadUint64(&d.atomicDataReceived),
			StartTime:            d.staticStartTime,
			StartTimeUnix:        d.staticStartTime.UnixNano(),
			TotalDataTransferred: atomic.LoadUint64(&d.atomicTotalDataTransferred),
		}
		// Release download lock before calling d.Err(), which will acquire the
		// lock. The error needs to be checked separately because we need to
		// know if it's 'nil' before grabbing the error string.
		d.mu.Unlock()
		if d.Err() != nil {
			downloads[i].Error = d.Err().Error()
		} else {
			downloads[i].Error = ""
		}
	}
	return downloads
}

// ClearDownloadHistory clears the renter's download history inclusive of the
// provided before and after timestamps
//
// TODO: This function can be improved by implementing a binary search, the
// trick will be making the binary search be just as readable while handling
// all the edge cases
func (r *Renter) ClearDownloadHistory(after, before time.Time) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	r.downloadHistoryMu.Lock()
	defer r.downloadHistoryMu.Unlock()

	// Check to confirm there are downloads to clear
	if len(r.downloadHistory) == 0 {
		return nil
	}

	// Timestamp validation
	if before.Before(after) {
		return errors.New("before timestamp can not be newer then after timestamp")
	}

	// Clear download history if both before and after timestamps are zero values
	if before.Equal(types.EndOfTime) && after.IsZero() {
		r.downloadHistory = make(map[modules.DownloadID]*download)
		return nil
	}

	// Find and return downloads that are not within the given range
	withinTimespan := func(t time.Time) bool {
		return (t.After(after) || t.Equal(after)) && (t.Before(before) || t.Equal(before))
	}
	filtered := make(map[modules.DownloadID]*download)
	for _, d := range r.downloadHistory {
		if !withinTimespan(d.staticStartTime) {
			filtered[d.UID()] = d
		}
	}
	r.downloadHistory = filtered
	return nil
}
