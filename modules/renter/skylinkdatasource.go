package renter

import (
	"context"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
)

const (
	// skylinkDataSourceRequestSize is the size that is suggested by the data
	// source to be used when reading data from it.
	skylinkDataSourceRequestSize = 1 << 18 // 256 KiB
)

type (
	// skylinkDataSource implements streamBufferDataSource on a Skylink.
	// Notably, it creates a pcws for every single chunk in the Skylink and
	// keeps them in memory, to reduce latency on seeking through the file.
	skylinkDataSource struct {
		// Metadata.
		staticID       modules.DataSourceID
		staticLayout   modules.SkyfileLayout
		staticMetadata modules.SkyfileMetadata

		// The "price per millisecond" is the budget that we are willing to
		// spend on faster workers. See projectchunkworkset.go.
		staticPricePerMS types.Currency

		// The first chunk contains all of the raw data for the skylink, and the
		// chunk fetchers contains one pcws for every chunk in the fanout. The
		// worker sets are spun up in advance so that the HasSector queries have
		// completed by the time that someone needs to fetch the data.
		staticFirstChunk    []byte
		staticChunkFetchers []chunkFetcher

		// Utilities
		staticCancelFunc context.CancelFunc
		staticCtx        context.Context
		staticRenter     *Renter
	}

	// skylinkReadResponse is a helper struct that contains the download
	// response
	skylinkReadResponse struct {
		staticData []byte
		staticErr  error
	}
)

// DataSize implements streamBufferDataSource
func (sds *skylinkDataSource) DataSize() uint64 {
	return sds.staticLayout.Filesize
}

// ID implements streamBufferDataSource
func (sds *skylinkDataSource) ID() modules.DataSourceID {
	return sds.staticID
}

// Metadata implements streamBufferDataSource
func (sds *skylinkDataSource) Metadata() modules.SkyfileMetadata {
	return sds.staticMetadata
}

// RequestSize implements streamBufferDataSource
func (sds *skylinkDataSource) RequestSize() uint64 {
	return skylinkDataSourceRequestSize
}

// SilentClose implements streamBufferDataSource
func (sds *skylinkDataSource) SilentClose() {
	// Cancelling the context for the data source should be sufficient. As all
	// child processes (such as the pcws for each chunk) should be using
	// contexts derived from the sds context.
	sds.staticCancelFunc()
}

// ReadStream implements streamBufferDataSource
func (sds *skylinkDataSource) ReadStream(off, fetchSize uint64) chan *skylinkReadResponse {
	// Prepare the response channel
	responseChan := make(chan *skylinkReadResponse, 1)

	// Determine how large each chunk is.
	chunkSize := uint64(sds.staticLayout.FanoutDataPieces) * modules.SectorSize
	firstChunkLength := uint64(len(sds.staticFirstChunk))

	// Prepare an array of download chans on which we'll receive the data.
	numChunks := fetchSize / chunkSize
	if fetchSize%chunkSize != 0 {
		numChunks += 1
	}
	if off%chunkSize != 0 {
		numChunks += 1
	}
	downloadChans := make([]chan *downloadResponse, 0, numChunks)

	// If there is data in the first chunk it means we are dealing with a small
	// skyfile without fanout bytes, that means we can simply read from that and
	// return early.
	if len(sds.staticFirstChunk) != 0 {
		bytesLeft := firstChunkLength - off
		if fetchSize > bytesLeft {
			fetchSize = bytesLeft
		}
		responseChan <- &skylinkReadResponse{
			staticData: sds.staticFirstChunk[off : off+fetchSize],
		}
		return responseChan
	}

	// Otherwise we are dealing with a large skyfile and have to aggregate the
	// download responses for every chunk in the fanout. We keep reading from
	// chunks until all the data has been read.
	var n uint64
	for n < fetchSize && off < sds.staticLayout.Filesize {
		// Determine which chunk the offset is currently in.
		chunkIndex := off / chunkSize
		offsetInChunk := off % chunkSize
		remainingBytes := fetchSize - n

		// Determine how much data to read from the chunk.
		remainingInChunk := chunkSize - offsetInChunk
		downloadSize := remainingInChunk
		if remainingInChunk > remainingBytes {
			downloadSize = remainingBytes
		}

		// Schedule the download.
		respChan, err := sds.staticChunkFetchers[chunkIndex].Download(sds.staticCtx, sds.staticPricePerMS, offsetInChunk, downloadSize)
		if err != nil {
			responseChan <- &skylinkReadResponse{
				staticErr: errors.AddContext(err, "unable to start download"),
			}
			return responseChan
		}
		downloadChans = append(downloadChans, respChan)
		if len(downloadChans) > int(numChunks) {
			responseChan <- &skylinkReadResponse{
				staticErr: errors.New("unexpected amount of chunks scheduled for download"),
			}
			return responseChan
		}

		off += downloadSize
		n += downloadSize
	}

	// Launch a goroutine that collects all download responses, aggregates them
	// and sends it as a single response over the response channel.
	err := sds.staticRenter.tg.Launch(func() {
		data := make([]byte, fetchSize)
		offset := 0
		failed := false
		for _, respChan := range downloadChans {
			resp := <-respChan
			if resp.err == nil {
				n := copy(data[offset:], resp.data)
				offset += n
				continue
			}
			if !failed {
				failed = true
				responseChan <- &skylinkReadResponse{staticErr: resp.err}
				close(responseChan)
			}
		}

		if !failed {
			responseChan <- &skylinkReadResponse{staticData: data}
			close(responseChan)
		}
	})
	if err != nil {
		responseChan <- &skylinkReadResponse{staticErr: err}
	}
	return responseChan
}

// skylinkDataSource will create a streamBufferDataSource for the data contained
// inside of a Skylink. The function will not return until the base sector and
// all skyfile metadata has been retrieved.
//
// NOTE: Because multiple different callers may want to use the same data
// source, we want the data source to outlive the initial call. That is why
// there is no input for a context - the data source will live as long as the
// stream buffer determines is appropriate.
//
// TODO: this should return a streamBufferDataSource, and will do so when we
// have adjusted the interface
func (r *Renter) skylinkDataSource(link modules.Skylink, pricePerMS types.Currency) (*skylinkDataSource, error) {
	// Create the context for the data source - a child of the renter
	// threadgroup but otherwise independent.
	ctx, cancelFunc := context.WithCancel(r.tg.StopCtx())

	// If this function exits with an error we need to call cancel, due to the
	// many returns here we use a boolean that cancels by default, only if we
	// reach the very end of this function we do not call cancel.
	cancel := true
	defer func() {
		if cancel {
			cancelFunc()
		}
	}()

	// Create the pcws for the first chunk. We use a passthrough cipher and
	// erasure coder. If the base sector is encrypted, we will notice and be
	// able to decrypt it once we have fully downloaded it and are able to
	// access the layout. We can make the assumption on the erasure coding being
	// of 1-n seeing as we currently always upload the basechunk using 1-N
	// redundancy.
	ptec := modules.NewPassthroughErasureCoder()
	tpsk, err := crypto.NewSiaKey(crypto.TypePlain, nil)
	if err != nil {
		return nil, errors.AddContext(err, "unable to create plain skykey")
	}
	pcws, err := r.newPCWSByRoots(ctx, []crypto.Hash{link.MerkleRoot()}, ptec, tpsk, 0)
	if err != nil {
		return nil, errors.AddContext(err, "unable to create the worker set for this skylink")
	}

	// Download the base sector. The base sector contains the metadata, without
	// it we can't provide a completed data source.
	offset, fetchSize, err := link.OffsetAndFetchSize()
	if err != nil {
		return nil, errors.AddContext(err, "unable to parse skylink")
	}
	respChan, err := pcws.managedDownload(ctx, pricePerMS, offset, fetchSize)
	if err != nil {
		return nil, errors.AddContext(err, "unable to start download")
	}
	resp := <-respChan
	if resp.err != nil {
		return nil, errors.AddContext(err, "base sector download did not succeed")
	}
	baseSector := resp.data
	if len(baseSector) < modules.SkyfileLayoutSize {
		return nil, errors.New("download did not fetch enough data, layout cannot be decoded")
	}

	// Check if the base sector is encrypted, and attempt to decrypt it.
	// This will fail if we don't have the decryption key.
	var fileSpecificSkykey skykey.Skykey
	if modules.IsEncryptedBaseSector(baseSector) {
		fileSpecificSkykey, err = r.decryptBaseSector(baseSector)
		if err != nil {
			return nil, errors.AddContext(err, "unable to decrypt skyfile base sector")
		}
	}

	// Parse out the metadata of the skyfile.
	layout, fanoutBytes, metadata, firstChunk, err := modules.ParseSkyfileMetadata(baseSector)
	if err != nil {
		return nil, errors.AddContext(err, "error parsing skyfile metadata")
	}

	// Derive the fanout key and erasure coder
	fanoutKey, err := r.deriveFanoutKey(&layout, fileSpecificSkykey)
	if err != nil {
		return nil, errors.AddContext(err, "unable to derive encryption key")
	}
	ec, err := modules.NewRSSubCode(int(layout.FanoutDataPieces), int(layout.FanoutParityPieces), crypto.SegmentSize)
	if err != nil {
		return nil, errors.AddContext(err, "unable to derive erasure coding settings for fanout")
	}

	// Create PCWSs for every chunk in the fanout
	fanoutChunks, err := layout.DecodeFanoutIntoChunks(fanoutBytes)
	if err != nil {
		return nil, errors.AddContext(err, "error parsing skyfile fanout")
	}
	fanoutChunkFetchers := make([]chunkFetcher, len(fanoutChunks))
	for i, chunk := range fanoutChunks {
		pcws, err := r.newPCWSByRoots(ctx, chunk, ec, fanoutKey, uint64(i))
		if err != nil {
			return nil, errors.AddContext(err, "unable to create worker set for all chunk indices")
		}
		fanoutChunkFetchers[i] = pcws
	}

	cancel = false
	sds := &skylinkDataSource{
		staticID:       link.DataSourceID(),
		staticLayout:   layout,
		staticMetadata: metadata,

		staticPricePerMS: pricePerMS,

		staticFirstChunk:    firstChunk,
		staticChunkFetchers: fanoutChunkFetchers,

		staticCancelFunc: cancelFunc,
		staticCtx:        ctx,
		staticRenter:     r,
	}
	return sds, nil
}
