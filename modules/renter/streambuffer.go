package renter

// NOTE: This stream buffer is uninfished in a couple of ways. The first way is
// that it's not possible to cancel fetches. The second way is that fetches are
// not prioritized, there should be a higher priority on data that is closer to
// the current stream offset. The third is that the amount of data which gets
// fetched is not dynamically adjusted. The streamer really should be monitoring
// the total amount of time it takes for a call to the data source to return
// some data, and should buffer accordingly. If auto-adjusting the lookahead
// size, care needs to be taken to ensure not to exceed the
// bytesBufferedPerStream size, as exceeding that will cause issues with the
// lru, and cause data fetches to be evicted before they become useful.

import (
	"context"
	"io"
	"sync"
	"time"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/threadgroup"
)

const (
	// minimumDataSections is set to two because the streamer always tries to
	// buffer at least the current data section and the next data section for
	// the current offset of a stream.
	//
	// Three as a number was considered so that in addition to buffering one
	// piece ahead, a previous piece could also be cached. This was considered
	// to be less valuable than keeping memory requirements low -
	// minimumDataSections is only at play if there is not enough room for
	// multiple cache nodes in the bytesBufferedPerStream.
	minimumDataSections = 2
)

var (
	// bytesBufferedPerStream is the total amount of data that gets allocated
	// per stream. If the RequestSize of a stream buffer is less than three
	// times the bytesBufferedPerStream, that much data will be allocated
	// instead.
	//
	// For example, if the RequestSize is 10kb and the bytesBufferedPerStream is
	// 100kb, then each stream is going to buffer 10 segments that are each 10kb
	// long in the LRU.
	//
	// But if the RequestSize is 50kb and the bytesBufferedPerStream is 100kb,
	// then each stream is going to buffer 3 segments that are each 50kb long in
	// the LRU, for a total of 150kb.
	bytesBufferedPerStream = build.Select(build.Var{
		Dev:      uint64(1 << 25), // 32 MiB
		Standard: uint64(1 << 25), // 32 MiB
		Testnet:  uint64(1 << 25), // 32 MiB
		Testing:  uint64(1 << 8),  // 256 bytes
	}).(uint64)

	// keepOldBuffersDuration specifies how long a stream buffer will stay in
	// the buffer set after the final stream is closed. This gives some buffer
	// time for a new request to the same resource, without having the data
	// source fully cleared out. This optimization is particularly useful for
	// certain video players and web applications.
	keepOldBuffersDuration = build.Select(build.Var{
		Dev:      time.Second * 15,
		Standard: time.Second * 60,
		Testnet:  time.Second * 60,
		Testing:  time.Second * 2,
	}).(time.Duration)

	// minimumLookahead defines the minimum amount that the stream will fetch
	// ahead of the current seek position in a stream.
	//
	// Note that there is a throughput vs. latency tradeoff here. The maximum
	// speed of a stream has an upper bound of the lookahead / latency. So if it
	// takes 1 second to fetch data and the lookahead is 2 MB, the maximum speed
	// of a single stream is going to be 2 MB/s. When Sia is healthy, the
	// latency on a fetch should be under 200ms, which means with a 2 MB
	// lookahead a single stream should be able to do more than 10 MB/s.
	//
	// A smaller minimum lookahead means that less data is being buffered
	// simultaneously, so seek times should be lower. A smaller minimum
	// lookahead becomes less important if we get some way to ensure the earlier
	// parts are prioritized, but we don't have control over that at the moment.
	minimumLookahead = build.Select(build.Var{
		Dev:      uint64(1 << 21), // 2 MiB
		Standard: uint64(1 << 23), // 8 MiB
		Testnet:  uint64(1 << 23), // 8 MiB
		Testing:  uint64(1 << 6),  // 64 bytes
	}).(uint64)
)

// streamBufferDataSource is an interface that the stream buffer uses to fetch
// data. This type is internal to the renter as there are plans to expand on the
// type.
type streamBufferDataSource interface {
	// DataSize should return the size of the data. When the streamBuffer is
	// reading from the data source, it will ensure that none of the read calls
	// go beyond the boundary of the data source.
	DataSize() uint64

	// ID returns the ID of the data source. This should be unique to the data
	// source - that is, every data source that returns the same ID should have
	// identical data and be fully interchangeable.
	ID() modules.DataSourceID

	// RequestSize should return the request size that the dataSource expects
	// the streamBuffer to use. The streamBuffer will always make ReadAt calls
	// that are of the suggested request size and byte aligned.
	//
	// If the request size is small, many ReadAt calls will be made in parallel.
	// If the dataSource can handle high parallelism, a smaller request size
	// should be recommended to the streamBuffer, because that will reduce
	// latency. If the dataSource cannot handle high parallelism, a larger
	// request size should be used to optimize for total throughput.
	//
	// A general rule of thumb is that the streamer should be able to
	// comfortably handle 100 mbps (high end 4K video) if the user's local
	// connection has that much throughput.
	RequestSize() uint64

	// SilentClose is an io.Closer that does not return an error. The data
	// source is expected to handle any logging or reporting that is necessary
	// if the closing fails.
	SilentClose()

	// ReadStream allows the stream buffer to request specific data chunks from
	// the data source. It returns a channel containing a read response.
	ReadStream(context.Context, uint64, uint64, types.Currency) chan *readResponse
}

// readResponse is a helper struct that is returned when reading from the data
// source. It contains the data being downloaded and an error in case of
// failure.
type readResponse struct {
	staticData []byte
	staticErr  error
}

// dataSection represents a section of data from a data source. The data section
// includes a refcount of how many different streams have the data in their LRU.
// If the refCount is ever set to 0, the data section should be deleted. Because
// the dataSection has no mutex, the refCount falls under the consistency domain
// of the object holding it, which should always be a streamBuffer.
type dataSection struct {
	// dataAvailable, externData, and externErr work together. The data and
	// error are not allowed to be accessed by external threads until the data
	// available channel has been closed. Once the dataAvailable channel has
	// been closed, externData and externErr are to be treated like static
	// fields.
	dataAvailable chan struct{}
	externData    []byte
	externErr     error

	refCount uint64
}

// stream is a single stream that uses a stream buffer. The stream implements
// io.ReadSeeker and io.Closer, and must be closed when it is done being used.
// The stream will cache data, both data that has been accessed recently as well
// as data that is in front of the current read head. The stream buffer is a
// common cache that is used between all streams that are using the same data
// source, allowing each stream to depend on the other streams if data has
// already been loaded.
type stream struct {
	lru    *leastRecentlyUsedCache
	offset uint64

	mu                 sync.Mutex
	staticStreamBuffer *streamBuffer

	staticContext     context.Context
	staticReadTimeout time.Duration
}

// streamBuffer is a buffer for a single dataSource.
//
// The streamBuffer uses a threadgroup to ensure that it does not call ReadAt
// after calling SilentClose.
type streamBuffer struct {
	dataSections map[uint64]*dataSection

	// externRefCount is in the same consistency domain as the streamBufferSet,
	// it needs to be incremented and decremented simultaneously with the
	// creation and deletion of the streamBuffer.
	externRefCount uint64

	mu                    sync.Mutex
	staticTG              threadgroup.ThreadGroup
	staticDataSize        uint64
	staticDataSource      streamBufferDataSource
	staticDataSectionSize uint64
	staticStreamBufferSet *streamBufferSet
	staticStreamID        modules.DataSourceID
	staticPricePerMS      types.Currency
	staticWallet          modules.SiacoinSenderMulti
}

// streamBufferSet tracks all of the stream buffers that are currently active.
// When a new stream is created, the stream buffer set is referenced to check
// whether another stream using the same data source already exists.
type streamBufferSet struct {
	streams map[modules.DataSourceID]*streamBuffer

	staticTG *threadgroup.ThreadGroup
	mu       sync.Mutex
}

// newStreamBufferSet initializes and returns a stream buffer set.
func newStreamBufferSet(tg *threadgroup.ThreadGroup) *streamBufferSet {
	return &streamBufferSet{
		streams: make(map[modules.DataSourceID]*streamBuffer),

		staticTG: tg,
	}
}

// callNewStream will create a stream that implements io.Close and
// io.ReadSeeker. A dataSource must be provided for the stream so that the
// stream can fetch data in advance of calls to 'Read' and attempt to provide a
// smooth streaming experience.
//
// The 'sourceID' is a unique identifier for the dataSource which allows
// multiple streams fetching data from the same source to combine their cache.
// This shared cache only comes into play if the streams are simultaneously
// accessing the same data, allowing the buffer to save on memory and access
// latency.
//
// Each stream has a separate LRU for determining what data to buffer. Because
// the LRU is distinct to the stream, the shared cache feature will not result
// in one stream evicting data from another stream's LRU.
func (sbs *streamBufferSet) callNewStream(dataSource streamBufferDataSource, initialOffset uint64, timeout time.Duration, pricePerMS types.Currency) *stream {
	// Grab the streamBuffer for the provided sourceID. If no streamBuffer for
	// the sourceID exists, create a new one.
	sourceID := dataSource.ID()
	sbs.mu.Lock()
	streamBuf, exists := sbs.streams[sourceID]
	if !exists {
		streamBuf = &streamBuffer{
			dataSections: make(map[uint64]*dataSection),

			staticDataSize:        dataSource.DataSize(),
			staticDataSource:      dataSource,
			staticDataSectionSize: dataSource.RequestSize(),
			staticPricePerMS:      pricePerMS,
			staticStreamBufferSet: sbs,
			staticStreamID:        sourceID,
		}
		sbs.streams[sourceID] = streamBuf
	} else {
		// Another data source already exists for this content which will be
		// used instead of the input data source. Close the input source.
		dataSource.SilentClose()
	}
	streamBuf.externRefCount++
	sbs.mu.Unlock()
	return streamBuf.managedPrepareNewStream(initialOffset, timeout)
}

// callNewStreamFromID will check the stream buffer set to see if a stream
// buffer exists for the given data source id. If so, a new stream will be
// created using the data source, and the bool will be set to 'true'. Otherwise,
// the stream returned will be nil and the bool will be set to 'false'.
func (sbs *streamBufferSet) callNewStreamFromID(id modules.DataSourceID, initialOffset uint64, timeout time.Duration) (*stream, bool) {
	sbs.mu.Lock()
	streamBuf, exists := sbs.streams[id]
	if !exists {
		sbs.mu.Unlock()
		return nil, false
	}
	streamBuf.externRefCount++
	sbs.mu.Unlock()
	return streamBuf.managedPrepareNewStream(initialOffset, timeout), true
}

// managedData will block until the data for a data section is available, and
// then return the data. The data is not safe to modify.
func (ds *dataSection) managedData(ctx context.Context) ([]byte, error) {
	select {
	case <-ds.dataAvailable:
	case <-ctx.Done():
		return nil, errors.New("could not get data from data section, context timed out")
	}
	return ds.externData, ds.externErr
}

// Close will release all of the resources held by a stream.
//
// Before removing the stream, this function will sleep for some time. This is
// specifically to address the use case where an application may be using the
// same file or resource continuously, but doing so by repeatedly opening new
// connections to siad rather than keeping a single stable connection. Some
// video players do this. This sleep here to delay the release of a resource
// substantially improves performance in practice, in many cases causing a 4x
// reduction in response latency.
func (s *stream) Close() error {
	s.staticStreamBuffer.staticStreamBufferSet.staticTG.Launch(func() {
		// Convenience variables.
		sb := s.staticStreamBuffer
		sbs := sb.staticStreamBufferSet
		// Keep the memory for a while after closing.
		sbs.staticTG.Sleep(keepOldBuffersDuration)

		// Drop all nodes from the lru.
		s.lru.callEvictAll()

		// Remove the stream from the streamBuffer.
		sbs.managedRemoveStream(sb)
	})
	return nil
}

// Read will read data into 'b', returning the number of bytes read and any
// errors. Read will not fill 'b' up all the way if only part of the data is
// available.
func (s *stream) Read(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create a context.
	ctx := s.staticContext
	if s.staticReadTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.staticReadTimeout)
		defer cancel()
	}

	// Convenience variables.
	dataSize := s.staticStreamBuffer.staticDataSize
	dataSectionSize := s.staticStreamBuffer.staticDataSectionSize
	sb := s.staticStreamBuffer

	// Check for EOF.
	if s.offset == dataSize {
		return 0, io.EOF
	}

	// Get the index of the current section and the offset within the current
	// section.
	currentSection := s.offset / dataSectionSize
	offsetInSection := s.offset % dataSectionSize

	// Determine how many bytes are remaining within the current section, this
	// forms an upper bound on how many bytes can be read.
	var bytesRemaining uint64
	lastSection := (currentSection+1)*dataSectionSize >= dataSize
	if !lastSection {
		bytesRemaining = dataSectionSize - offsetInSection
	} else {
		bytesRemaining = dataSize - s.offset
	}

	// Determine how many bytes should be read.
	var bytesToRead uint64
	if bytesRemaining > uint64(len(b)) {
		bytesToRead = uint64(len(b))
	} else {
		bytesToRead = bytesRemaining
	}

	// Fetch the dataSection that has the data we want to read.
	sb.mu.Lock()
	dataSection, exists := sb.dataSections[currentSection]
	sb.mu.Unlock()
	if !exists {
		err := errors.New("data section should always in the stream buffer for the current offset of a stream")
		build.Critical(err)
		return 0, err
	}

	// Block until the data is available.
	data, err := dataSection.managedData(ctx)
	if err != nil {
		return 0, errors.AddContext(err, "read call failed because data section fetch failed")
	}
	// Copy the data into the read request.
	n := copy(b, data[offsetInSection:offsetInSection+bytesToRead])
	s.offset += uint64(n)

	// Send the call to prepare the next data section.
	s.prepareOffset()
	return n, nil
}

// Seek will move the read head of the stream to the provided offset.
func (s *stream) Seek(offset int64, whence int) (int64, error) {
	// Input checking.
	if offset < 0 {
		return int64(s.offset), errors.New("offset cannot be negative in call to seek")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update the offset of the stream according to the inputs.
	dataSize := s.staticStreamBuffer.staticDataSize
	switch whence {
	case io.SeekStart:
		s.offset = uint64(offset)
	case io.SeekCurrent:
		newOffset := s.offset + uint64(offset)
		if newOffset > dataSize {
			return int64(s.offset), errors.New("offset cannot seek beyond the bounds of the file")
		}
		s.offset = newOffset
	case io.SeekEnd:
		if uint64(offset) > dataSize {
			return int64(s.offset), errors.New("cannot seek before the front of the file")
		}
		s.offset = dataSize - uint64(offset)
	default:
		return int64(s.offset), errors.New("invalid value for 'whence' in call to seek")
	}

	// Prepare the fetch of the updated offset.
	s.prepareOffset()
	return int64(s.offset), nil
}

// prepareOffset will ensure that the dataSection containing the offset is made
// available in the LRU, and that the following dataSection is also available.
func (s *stream) prepareOffset() {
	// Convenience variables.
	dataSize := s.staticStreamBuffer.staticDataSize
	dataSectionSize := s.staticStreamBuffer.staticDataSectionSize

	// If the offset is already at the end of the data, there is nothing to do.
	if s.offset == dataSize {
		return
	}

	// Update the current data section. The update call will trigger the
	// streamBuffer to fetch the dataSection if the dataSection is not already
	// in the streamBuffer cache.
	index := s.offset / dataSectionSize
	s.lru.callUpdate(index)

	// If there is a following data section, update that as well. This update is
	// done regardless of the minimumLookahead, we always want to buffer at
	// least one more piece than the current piece.
	nextIndex := index + 1
	if nextIndex*dataSectionSize < dataSize {
		s.lru.callUpdate(nextIndex)
	}

	// Keep adding more pieces to the buffer until we have buffered at least
	// minimumLookahead total data or have reached the end of the stream.
	nextIndex++
	for i := dataSectionSize * 2; i < minimumLookahead && nextIndex*dataSectionSize < dataSize; i += dataSectionSize {
		s.lru.callUpdate(nextIndex)
		nextIndex++
	}
}

// callFetchDataSection will increment the refcount of a dataSection in the
// stream buffer. If the dataSection is not currently available in the stream
// buffer, the data section will be fetched from the dataSource.
func (sb *streamBuffer) callFetchDataSection(index uint64) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	// Fetch the relevant dataSection, creating a new one if necessary.
	dataSection, exists := sb.dataSections[index]
	if !exists {
		dataSection = sb.newDataSection(index)
	}
	// Increment the refcount of the dataSection.
	dataSection.refCount++
}

// callRemoveDataSection will decrement the refcount of a data section in the
// stream buffer. If the refcount reaches zero, the data section will be deleted
// from the stream buffer.
func (sb *streamBuffer) callRemoveDataSection(index uint64) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	// Fetch the data section.
	dataSection, exists := sb.dataSections[index]
	if !exists {
		build.Critical("remove called on data section that does not exist")
		return
	}
	// Decrement the refcount.
	dataSection.refCount--
	// Delete the data section if the refcount has fallen to zero.
	if dataSection.refCount == 0 {
		delete(sb.dataSections, index)
	}
}

// managedPrepareNewStream creates a new stream from an existing stream buffer.
// The ref count for the buffer needs to be incremented under the
// streamBufferSet lock, before this method is called.
func (sb *streamBuffer) managedPrepareNewStream(initialOffset uint64, timeout time.Duration) *stream {
	// Determine how many data sections the stream should cache.
	dataSectionsToCache := bytesBufferedPerStream / sb.staticDataSectionSize
	if dataSectionsToCache < minimumDataSections {
		dataSectionsToCache = minimumDataSections
	}

	// Create a stream that points to the stream buffer.
	stream := &stream{
		lru:    newLeastRecentlyUsedCache(dataSectionsToCache, sb),
		offset: initialOffset,

		staticContext:      sb.staticTG.StopCtx(),
		staticReadTimeout:  timeout,
		staticStreamBuffer: sb,
	}
	stream.prepareOffset()
	return stream
}

// newDataSection will create a new data section for the streamBuffer and spin
// up a goroutine to pull the data from the data source.
func (sb *streamBuffer) newDataSection(index uint64) *dataSection {
	// Convenience variables.
	dataSize := sb.staticDataSize
	dataSectionSize := sb.staticDataSectionSize

	// Determine the fetch size for the data section. The fetch size should be
	// equal to the dataSectionSize unless this is the final section, in which
	// case the section size should be exactly big enough to request all
	// remaining bytes.
	var fetchSize uint64
	if (index+1)*dataSectionSize > dataSize {
		fetchSize = dataSize - (index * dataSectionSize)
	} else {
		fetchSize = dataSectionSize
	}

	// Create the data section, allocating the right number of bytes for the
	// ReadAt call to fill out.
	ds := &dataSection{
		dataAvailable: make(chan struct{}),
		externData:    make([]byte, fetchSize),
	}
	sb.dataSections[index] = ds

	// Perform the data fetch in a goroutine. The dataAvailable channel will be
	// closed when the data is available.
	go func() {
		defer close(ds.dataAvailable)

		// Ensure that the streambuffer has not closed.
		err := sb.staticTG.Add()
		if err != nil {
			ds.externErr = errors.AddContext(err, "stream buffer has been shut down")
			return
		}
		defer sb.staticTG.Done()

		// Grab the data from the data source.
		responseChan := sb.staticDataSource.ReadStream(sb.staticTG.StopCtx(), index*dataSectionSize, fetchSize, sb.staticPricePerMS)

		select {
		case response := <-responseChan:
			ds.externErr = errors.AddContext(response.staticErr, "data section ReadStream failed")
			ds.externData = response.staticData
		case <-sb.staticTG.StopChan():
			ds.externErr = errors.New("failed to read response from ReadStream")
		}
	}()
	return ds
}

// managedRemoveStream will remove a stream from a stream buffer. If the total
// number of streams using that stream buffer reaches zero, the stream buffer
// will be removed from the stream buffer set.
//
// The reference counter for a stream buffer needs to be in the domain of the
// stream buffer set because the stream buffer needs to be deleted from the
// stream buffer set simultaneously with the reference counter reaching zero.
func (sbs *streamBufferSet) managedRemoveStream(sb *streamBuffer) {
	// Decrement the refcount of the streamBuffer.
	sbs.mu.Lock()
	sb.externRefCount--
	if sb.externRefCount > 0 {
		// streamBuffer still in use, nothing to do.
		sbs.mu.Unlock()
		return
	}
	delete(sbs.streams, sb.staticStreamID)
	sbs.mu.Unlock()

	// Close out the streamBuffer and its data source. Calling Stop() will block
	// any new calls to ReadAt from executing, and will block until all existing
	// calls are completed. This prevents any issues that could be caused by the
	// data source being accessed after it has been closed.
	sb.staticTG.Stop()
	sb.staticDataSource.SilentClose()
}
