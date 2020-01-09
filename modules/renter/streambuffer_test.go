package renter

import (
	// "testing"
)

// mockDataSource implements a stream buffer data source that can be used to
// test the stream buffer. It's a simple in-memory buffer.
type mockDataSource struct {
	staticData []byte
	staticRequestSize uint64
}

// newMockDataSource will return a data source that is ready to use.
func newMockDataSource(data []byte, requestSize uint64) *mockDataSource {
	return &mockDataSource{
		staticData: data,
		staticRequestSize: requestSize,
	}
}

// DataSize implements streamBufferDataSource
func (mds *mockDataSource) DataSize() uint64 {
	return uint64(len(mds.staticData))
}

// RequestSize implements streamBufferDataSource.
func (mds *mockDataSource) RequestSize() uint64 {
	return mds.staticRequestSize
}

// ReadAt implements streamBufferDataSource.
func (mds *mockDataSource) ReadAt(b []byte, offset int64) (int, error) {
	// panic if these error during testing - the error being returned may not
	// make it all the way back to the test program because of failovers and
	// such, but this is an incorrect call that should never be made by the
	// stream buffer.
	if offset < 0 {
		panic("bad call to mocked ReadAt")
	}
	if uint64(offset + int64(len(b))) > mds.DataSize() {
		panic("bad call to mocked ReadAt")
	}
	if uint64(offset) % mds.RequestSize() != 0 {
		panic("bad call to mocked ReadAt")
	}
	if uint64(len(b)) > mds.DataSize() {
		panic("bad call to mocked ReadAt")
	}
	if uint64(len(b)) != mds.RequestSize() && uint64(offset+int64(len(b))) != mds.DataSize() {
		panic("bad call to mocked ReadAt")
	}
	n := copy(b, mds.staticData[offset:])
	return n, nil
}

// Close implements streamBufferDataSource.
func (mds *mockDataSource) Close() error {
	mds.staticData = nil
	return nil
}
