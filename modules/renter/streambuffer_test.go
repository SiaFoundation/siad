package renter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"sync"
	"testing"
	"time"

	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"

	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/threadgroup"
)

// mockDataSource implements a stream buffer data source that can be used to
// test the stream buffer. It's a simple in-memory buffer.
//
// staticDataLen is a separate field so we can have a sanity check on the reads
// in ReadAt of the mockDataSource without having a race condition if ReadAt is
// called after the stream is closed.
type mockDataSource struct {
	data              []byte
	staticDataLen     uint64
	staticRequestSize uint64
	mu                sync.Mutex
}

// newMockDataSource will return a data source that is ready to use.
func newMockDataSource(data []byte, requestSize uint64) *mockDataSource {
	return &mockDataSource{
		data:              data,
		staticDataLen:     uint64(len(data)),
		staticRequestSize: requestSize,
	}
}

// DataSize implements streamBufferDataSource
func (mds *mockDataSource) DataSize() uint64 {
	mds.mu.Lock()
	defer mds.mu.Unlock()
	return uint64(len(mds.data))
}

// ID implements streamBufferDataSource
func (mds *mockDataSource) ID() modules.DataSourceID {
	mds.mu.Lock()
	defer mds.mu.Unlock()
	return modules.DataSourceID(crypto.HashObject(mds.data))
}

// RequestSize implements streamBufferDataSource.
func (mds *mockDataSource) RequestSize() uint64 {
	return mds.staticRequestSize
}

// ReadStream implements streamBufferDataSource.
func (mds *mockDataSource) ReadStream(ctx context.Context, offset, fetchSize uint64, pricePerMS types.Currency) chan *readResponse {
	mds.mu.Lock()
	defer mds.mu.Unlock()

	// panic if these error during testing - the error being returned may not
	// make it all the way back to the test program because of failovers and
	// such, but this is an incorrect call that should never be made by the
	// stream buffer.
	if offset < 0 {
		panic("bad call to mocked ReadAt")
	}
	if offset+fetchSize > mds.staticDataLen {
		str := fmt.Sprintf("call to ReadAt is asking for data that exceeds the data size: %v - %v", offset+fetchSize, mds.staticDataLen)
		panic(str)
	}
	if offset%mds.RequestSize() != 0 {
		panic("bad call to mocked ReadAt")
	}
	if fetchSize > mds.staticDataLen {
		panic("bad call to mocked ReadAt")
	}
	if fetchSize != mds.RequestSize() && offset+fetchSize != mds.staticDataLen {
		panic("bad call to mocked ReadAt")
	}

	responseChan := make(chan *readResponse, 1)
	responseChan <- &readResponse{staticData: mds.data[offset : offset+fetchSize]}
	return responseChan
}

// SilentClose implements streamBufferDataSource.
func (mds *mockDataSource) SilentClose() {
	mds.mu.Lock()
	mds.data = nil
	mds.mu.Unlock()
}

// TestStreamSmoke checks basic logic on the stream to see that reading and
// seeking and closing works.
func TestStreamSmoke(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create a usable stream, starting at offset 0.
	var tg threadgroup.ThreadGroup
	data := fastrand.Bytes(15999) // 1 byte short of 1000 data sections.
	dataSectionSize := uint64(16)
	dataSource := newMockDataSource(data, dataSectionSize)
	sbs := newStreamBufferSet(&tg)
	stream := sbs.callNewStream(dataSource, 0, 0, types.ZeroCurrency)

	// Check that there is one reference in the stream buffer.
	sbs.mu.Lock()
	refs := stream.staticStreamBuffer.externRefCount
	sbs.mu.Unlock()
	if refs != 1 {
		t.Fatal("bad")
	}
	// Create a new stream from an id, check that the ref count goes up.
	streamFromID, exists := sbs.callNewStreamFromID(dataSource.ID(), 0, 0)
	if !exists {
		t.Fatal("bad")
	}
	sbs.mu.Lock()
	refs = stream.staticStreamBuffer.externRefCount
	sbs.mu.Unlock()
	if refs != 2 {
		t.Fatal("bad")
	}
	// Create a second, different data source with the same id and try to use
	// that.
	dataSource2 := newMockDataSource(data, dataSectionSize)
	repeatStream := sbs.callNewStream(dataSource2, 0, 0, types.ZeroCurrency)
	sbs.mu.Lock()
	refs = stream.staticStreamBuffer.externRefCount
	sbs.mu.Unlock()
	if refs != 3 {
		t.Fatal("bad")
	}
	err := repeatStream.Close()
	if err != nil {
		t.Fatal(err)
	}
	err = streamFromID.Close()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(keepOldBuffersDuration)
	time.Sleep(keepOldBuffersDuration / 3)
	sbs.mu.Lock()
	refs = stream.staticStreamBuffer.externRefCount
	sbs.mu.Unlock()
	if refs != 1 {
		t.Fatal("bad")
	}

	// Perform the ritual that the http.ResponseWriter performs - seek to front,
	// seek to back, read 512 bytes, seek to front, read a bigger chunk of data.
	offset, err := stream.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatal(err)
	}
	if offset != 0 {
		t.Fatal("bad")
	}
	offset, err = stream.Seek(0, io.SeekEnd)
	if err != nil {
		t.Fatal(err)
	}
	if offset != 15999 {
		t.Fatal("bad")
	}
	offset, err = stream.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatal(err)
	}
	if offset != 0 {
		t.Fatal("bad")
	}
	// Check that the right pieces have been buffered. 4 sections should be in
	// the buffer total.
	streamBuf := stream.staticStreamBuffer
	streamBuf.mu.Lock()
	_, exists0 := streamBuf.dataSections[0]
	_, exists1 := streamBuf.dataSections[1]
	_, exists2 := streamBuf.dataSections[2]
	_, exists3 := streamBuf.dataSections[3]
	_, exists4 := streamBuf.dataSections[4]
	streamBuf.mu.Unlock()
	if !exists0 || !exists1 || !exists2 || !exists3 || exists4 {
		t.Fatal("bad")
	}
	buf := make([]byte, 512)
	bytesRead, err := io.ReadFull(stream, buf)
	if err != nil {
		t.Fatal(err)
	}
	if bytesRead != 512 {
		t.Fatal("bad", bytesRead)
	}
	if !bytes.Equal(buf, data[:512]) {
		t.Fatal("bad")
	}
	streamBuf.mu.Lock()
	_, exists0 = streamBuf.dataSections[32]
	_, exists1 = streamBuf.dataSections[33]
	_, exists2 = streamBuf.dataSections[34]
	_, exists3 = streamBuf.dataSections[35]
	_, exists4 = streamBuf.dataSections[36]
	streamBuf.mu.Unlock()
	if !exists0 || !exists1 || !exists2 || !exists3 || exists4 {
		t.Fatal("bad")
	}
	offset, err = stream.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatal(err)
	}
	if offset != 0 {
		t.Fatal("bad")
	}
	streamBuf.mu.Lock()
	_, exists0 = streamBuf.dataSections[0]
	_, exists1 = streamBuf.dataSections[1]
	_, exists2 = streamBuf.dataSections[2]
	_, exists3 = streamBuf.dataSections[3]
	_, exists4 = streamBuf.dataSections[4]
	streamBuf.mu.Unlock()
	if !exists0 || !exists1 || !exists2 || !exists3 || exists4 {
		t.Fatal("bad")
	}
	buf = make([]byte, 1000)
	bytesRead, err = io.ReadFull(stream, buf)
	if err != nil {
		t.Fatal(err)
	}
	if bytesRead != 1000 {
		t.Fatal("bad")
	}
	if !bytes.Equal(buf, data[:1000]) {
		t.Fatal("bad")
	}
	streamBuf.mu.Lock()
	_, exists0 = streamBuf.dataSections[62]
	_, exists1 = streamBuf.dataSections[63]
	_, exists2 = streamBuf.dataSections[64]
	_, exists3 = streamBuf.dataSections[65]
	_, exists4 = streamBuf.dataSections[66]
	streamBuf.mu.Unlock()
	if !exists0 || !exists1 || !exists2 || !exists3 || exists4 {
		t.Fatal("bad")
	}
	// Seek to near the end to see that the cache tool collects the correct
	// pieces.
	offset, err = stream.Seek(35, io.SeekEnd)
	if err != nil {
		t.Fatal(err)
	}
	streamBuf.mu.Lock()
	_, existsi := streamBuf.dataSections[996] // Should not be buffered
	_, exists0 = streamBuf.dataSections[997]  // Up to byte 15968
	_, exists1 = streamBuf.dataSections[998]  // Up to byte 15984
	_, exists2 = streamBuf.dataSections[999]  // Up to byte 16000
	_, exists3 = streamBuf.dataSections[1000] // Beyond end of file.
	streamBuf.mu.Unlock()
	if existsi || !exists0 || !exists1 || !exists2 || exists3 {
		t.Fatal("bad")
	}
	// Seek back to the beginning one more time to do a full read of the data.
	offset, err = stream.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatal(err)
	}
	if offset != 0 {
		t.Fatal("bad")
	}
	// Read all of the data.
	fullRead, err := ioutil.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fullRead, data) {
		t.Fatal("bad")
	}

	// The reads should have exceeded the cache size. Check that the number of
	// nodes in the stream buffer and lru match what is expected.
	expectedNodes := int(bytesBufferedPerStream / dataSectionSize)
	if len(stream.staticStreamBuffer.dataSections) != expectedNodes {
		t.Fatal("bad")
	}
	if len(stream.lru.nodes) != expectedNodes {
		t.Fatal("bad")
	}

	// Open up a second stream and read the front of the data. This should cause
	// the stream buffer to have a full cache for each stream and stream2, since
	// there is no overlap between their lrus.
	//
	// NOTE: Need to use a second data source, because it'll be closed when it's
	// not used. The stream buffer expects that when multiple data sources have
	// the same ID, they are actually separate objects which need to be closed
	// individually.
	dataSource3 := newMockDataSource(data, dataSectionSize)
	stream2 := sbs.callNewStream(dataSource3, 0, 0, types.ZeroCurrency)
	bytesRead, err = io.ReadFull(stream2, buf)
	if err != nil {
		t.Fatal(err)
	}
	if bytesRead != len(buf) {
		t.Fatal("bad")
	}
	if !bytes.Equal(buf, data[:len(buf)]) {
		t.Fatal("bad")
	}
	if len(stream.staticStreamBuffer.dataSections) != (expectedNodes * 2) {
		t.Fatal("bad")
	}
	if len(stream2.staticStreamBuffer.dataSections) != (expectedNodes * 2) {
		t.Fatal("bad")
	}
	if len(stream.lru.nodes) != expectedNodes {
		t.Fatal("bad")
	}
	if len(stream2.lru.nodes) != expectedNodes {
		t.Fatal("bad")
	}

	// Read the full data on stream2, this should cause the lrus to match, and
	// therefore the stream buffer to only have 1 set of data.
	fullRead, err = ioutil.ReadAll(stream2)
	if err != nil {
		t.Fatal(err)
	}
	if len(stream.staticStreamBuffer.dataSections) != expectedNodes {
		t.Fatal("bad")
	}
	if len(stream2.staticStreamBuffer.dataSections) != expectedNodes {
		t.Fatal("bad")
	}
	if len(stream.lru.nodes) != expectedNodes {
		t.Fatal("bad")
	}
	if len(stream2.lru.nodes) != expectedNodes {
		t.Fatal("bad")
	}

	// Close the stream and see that all resources for the stream are dropped,
	// but that the stream buffer sticks around.
	err = stream.Close()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(keepOldBuffersDuration / 3)
	// Check that the stream hangs around a bit after close.
	stream.lru.mu.Lock()
	if len(stream.lru.nodes) == 0 {
		stream.lru.mu.Unlock()
		t.Fatal("bad")
	}
	if stream.lru.head == nil {
		stream.lru.mu.Unlock()
		t.Fatal("bad")
	}
	if stream.lru.tail == nil {
		stream.lru.mu.Unlock()
		t.Fatal("bad")
	}
	stream.lru.mu.Unlock()
	// Sleep until the stream is cleared.
	time.Sleep(keepOldBuffersDuration)
	if dataSource.data == nil {
		t.Fatal("bad")
	}
	stream.lru.mu.Lock()
	if len(stream.lru.nodes) != 0 {
		stream.lru.mu.Unlock()
		t.Fatal("bad")
	}
	if stream.lru.head != nil {
		stream.lru.mu.Unlock()
		t.Fatal("bad")
	}
	if stream.lru.tail != nil {
		stream.lru.mu.Unlock()
		t.Fatal("bad")
	}
	stream.lru.mu.Unlock()
	if len(sbs.streams) != 1 {
		t.Fatal("bad")
	}
	if len(stream.staticStreamBuffer.dataSections) != expectedNodes {
		t.Fatal("bad")
	}

	// Close the second stream and see that all resources are dropped.
	err = stream2.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Sleep until the stream is cleared.
	time.Sleep(keepOldBuffersDuration / 3)
	time.Sleep(keepOldBuffersDuration)
	dataSource.mu.Lock()
	if dataSource.data != nil {
		dataSource.mu.Unlock()
		t.Fatal("bad")
	}
	dataSource.mu.Unlock()
	stream2.lru.mu.Lock()
	if len(stream2.lru.nodes) != 0 {
		stream2.lru.mu.Unlock()
		t.Fatal("bad")
	}
	if stream2.lru.head != nil {
		stream2.lru.mu.Unlock()
		t.Fatal("bad")
	}
	if stream2.lru.tail != nil {
		stream2.lru.mu.Unlock()
		t.Fatal("bad")
	}
	stream2.lru.mu.Unlock()
	if len(sbs.streams) != 0 {
		t.Fatal("bad")
	}
	if len(stream2.staticStreamBuffer.dataSections) != 0 {
		t.Fatal("bad")
	}

	// Check that if the tg is stopped, the stream closes immediately.
	dataSource4 := newMockDataSource(data, dataSectionSize)
	stream3 := sbs.callNewStream(dataSource4, 0, 0, types.ZeroCurrency)
	bytesRead, err = io.ReadFull(stream3, buf)
	if err != nil {
		t.Fatal(err)
	}
	err = stream3.Close()
	if err != nil {
		t.Fatal(err)
	}
	err = tg.Stop()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(keepOldBuffersDuration / 5)
	dataSource.mu.Lock()
	if dataSource.data != nil {
		dataSource.mu.Unlock()
		t.Fatal("bad")
	}
	dataSource.mu.Unlock()
	stream3.lru.mu.Lock()
	if len(stream3.lru.nodes) != 0 {
		stream3.lru.mu.Unlock()
		t.Fatal("bad")
	}
	if stream3.lru.head != nil {
		stream3.lru.mu.Unlock()
		t.Fatal("bad")
	}
	if stream3.lru.tail != nil {
		stream3.lru.mu.Unlock()
		t.Fatal("bad")
	}
	stream3.lru.mu.Unlock()
	sbs.mu.Lock()
	if len(sbs.streams) != 0 {
		sbs.mu.Unlock()
		t.Fatal("bad")
	}
	sbs.mu.Unlock()
	if len(stream3.staticStreamBuffer.dataSections) != 0 {
		t.Fatal("bad")
	}
}
