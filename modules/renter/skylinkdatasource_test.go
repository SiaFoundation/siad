package renter

import (
	"bytes"
	"context"
	"reflect"
	"sync/atomic"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// mockProjectChunkWorkerSet is a mock object implementing the chunkFetcher
// interface
type mockProjectChunkWorkerSet struct {
	staticDownloadResponseChan chan *downloadResponse
	staticDownloadData         []byte
	staticErr                  error
}

// Download implements the chunkFetcher interface.
func (m *mockProjectChunkWorkerSet) Download(ctx context.Context, pricePerMS types.Currency, offset, length uint64) (chan *downloadResponse, error) {
	m.staticDownloadResponseChan <- &downloadResponse{
		data: m.staticDownloadData[offset : offset+length],
		err:  nil,
	}
	return m.staticDownloadResponseChan, m.staticErr
}

// newChunkFetcher returns a chunk fetcher.
func newChunkFetcher(data []byte, err error) chunkFetcher {
	responseChan := make(chan *downloadResponse, 1)
	return &mockProjectChunkWorkerSet{
		staticDownloadResponseChan: responseChan,
		staticDownloadData:         data,
		staticErr:                  err,
	}
}

// TestSkylinkDataSource is a unit test that verifies the behaviour of a
// SkylinkDataSource. Note that we are using mocked data, testing of the
// datasource with live PCWSs attached will happen through integration tests.
func TestSkylinkDataSource(t *testing.T) {
	baseChunk := fastrand.Bytes(int(modules.SectorSize))
	fanoutChunk1 := fastrand.Bytes(int(modules.SectorSize))
	fanoutChunk2 := fastrand.Bytes(int(modules.SectorSize) / 2)
	datasize := modules.SectorSize*2 + modules.SectorSize/2

	ctx, cancel := context.WithCancel(context.Background())

	sds := &skylinkDataSource{
		staticID: modules.DataSourceID(crypto.Hash{1, 2, 3}),
		staticLayout: modules.SkyfileLayout{
			Version:            modules.SkyfileVersion,
			Filesize:           datasize,
			MetadataSize:       14e3,
			FanoutSize:         75e3,
			FanoutDataPieces:   1,
			FanoutParityPieces: 10,
			CipherType:         crypto.TypePlain,
		},
		staticMetadata: modules.SkyfileMetadata{
			Filename: "thisisafilename",
			Length:   datasize,
		},

		staticFirstChunk: baseChunk,
		staticChunkFetchers: []chunkFetcher{
			newChunkFetcher(fanoutChunk1, nil),
			newChunkFetcher(fanoutChunk2, nil),
		},

		staticCancelFunc: cancel,
		staticCtx:        ctx,
		staticRenter:     new(Renter),
	}

	closed := atomic.LoadUint64(&sds.atomicClosed)
	if closed != 0 {
		t.Fatal("unexpected")
	}

	if sds.DataSize() != datasize {
		t.Fatal("unexpected", sds.DataSize(), datasize)
	}
	if sds.ID() != modules.DataSourceID(crypto.Hash{1, 2, 3}) {
		t.Fatal("unexpected")
	}
	if !reflect.DeepEqual(sds.Metadata(), modules.SkyfileMetadata{
		Filename: "thisisafilename",
		Length:   datasize,
	}) {
		t.Fatal("unexpected")
	}
	if sds.RequestSize() != 1<<18 {
		t.Fatal("unexpected")
	}

	allData := append(baseChunk, fanoutChunk1...)
	allData = append(allData, fanoutChunk2...)

	length := fastrand.Uint64n(datasize/4) + 1
	offset := fastrand.Uint64n(datasize - length)

	dataChan, errorChan := sds.ReadChannel(offset, length)
	if errorChan == nil {
		t.Fatal("unexpected")
	}
	var readErr error
	select {
	case readErr = <-errorChan:
	default:
	}
	if readErr != nil {
		t.Fatal("unexpected")
	}

	buf := make([]byte, length)
	for i := range buf {
		buf[i] = <-dataChan
	}
	if !bytes.Equal(buf, allData[offset:offset+length]) {
		t.Log("expected: ", allData[offset:offset+length], len(allData[offset:offset+length]))
		t.Log("actual:   ", buf, len(buf))
		t.Fatal("unexepected data")
	}

	sds.SilentClose()
	closed = atomic.LoadUint64(&sds.atomicClosed)
	if closed != 1 {
		t.Fatal("unexpected")
	}
}
