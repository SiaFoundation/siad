package renter

import (
	"bytes"
	"io"
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

// fanoutStreamer implements modules.Streamer for a linkfile fanout.
type fanoutStreamer struct {
	// Each chunk is an array of sector hashes that correspond to pieces which
	// can be fetched.
	staticChunks       [][]crypto.Hash
	staticErasureCoder modules.ErasureCoder
	staticLayout       linkfileLayout
	staticMasterKey    crypto.CipherKey

	// Variables to correctly implement the reader.
	offset uint64

	// Data buffer for reading additional data. This current fetcher is super
	// naive, and intended to be thrown away and integrated properly with the
	// general download streamer, this is just to get end-to-end examples
	// working as fast as possible.
	chunkAvailable   chan struct{}
	chunkDataCurrent []byte
	chunkDataNext    []byte
	fetchErr         error

	// Utils.
	staticRenter *Renter
	mu           sync.Mutex
}

// linkfileDecodeFanout will take an encoded data fanout and convert it into a
// more consumable format.
func (r *Renter) newFanoutStreamer(ll linkfileLayout, fanoutBytes []byte) (*fanoutStreamer, error) {
	// Create the erasure coder and the master key.
	masterKey, err := crypto.NewSiaKey(ll.cipherType, ll.cipherKey[:])
	if err != nil {
		return nil, errors.AddContext(err, "count not recover siafile fanout because cipher key was unavailable")
	}
	ec, err := siafile.NewRSSubCode(int(ll.fanoutDataPieces), int(ll.fanoutParityPieces), crypto.SegmentSize)
	if err != nil {
		return nil, errors.New("unable to initialize erasure code")
	}

	// Build the base streamer object.
	fs := &fanoutStreamer{
		staticErasureCoder: ec,
		staticLayout:       ll,
		staticMasterKey:    masterKey,

		chunkAvailable: make(chan struct{}),

		staticRenter: r,
	}

	// Decode the fanout to get the chunk fetch data.
	piecesPerChunk := uint64(ll.fanoutDataPieces) + uint64(ll.fanoutParityPieces)
	chunkSize := crypto.HashSize * piecesPerChunk
	for uint64(len(fanoutBytes)) >= chunkSize {
		chunk := make([]crypto.Hash, piecesPerChunk)
		for i := 0; i < len(chunk); i++ {
			copy(chunk[i][:], fanoutBytes)
			fanoutBytes = fanoutBytes[crypto.HashSize:]
		}
		fs.staticChunks = append(fs.staticChunks, chunk)
	}

	// Kick off a thread to start downloading the first chunk, then return the
	// streamer.
	go fs.threadedFetchChunk(0)
	return fs, nil
}

// threadedFetchChunk will fetch a chunk at the provided index.
func (fs *fanoutStreamer) threadedFetchChunk(chunkIndex uint64) {
	// Spin up one thread per piece and try to fetch them all. This is wasteful,
	// but easier to implement. Intention at least for now is just to get
	// end-to-end testing working for this feature.
	var blankHash crypto.Hash
	pieces := make([][]byte, len(fs.staticChunks[chunkIndex]))
	var wg sync.WaitGroup
	for i := uint64(0); i < uint64(len(fs.staticChunks[chunkIndex])); i++ {
		// Skip pieces where the Merkle root is not supplied.
		if fs.staticChunks[chunkIndex][i] == blankHash {
			continue
		}

		// Spin up a thread to fetch this piece.
		wg.Add(1)
		go func(i uint64) {
			defer wg.Done()
			pieceData, err := fs.staticRenter.DownloadByRoot(fs.staticChunks[chunkIndex][i], 0, modules.SectorSize)
			if err == nil {
				key := fs.staticMasterKey.Derive(chunkIndex, i)
				key.DecryptBytesInPlace(pieceData, 0)
				pieces[i] = pieceData
			}
		}(i)
	}
	wg.Wait()

	// Check how many pieces came back.
	piecesReceived := uint8(0)
	for i := uint8(0); i < uint8(len(pieces)); i++ {
		// Count this as a new piece if less than a full set of pieces have been
		// discovered.
		if pieces[i] != nil && piecesReceived < fs.staticLayout.fanoutDataPieces {
			piecesReceived++
		} else {
			// If the full set of pieces have been discovered, nil this piece
			// out to save on memory.
			pieces[i] = nil
		}
	}

	// If there are not enough pieces, the fetch has failed.
	if piecesReceived < fs.staticLayout.fanoutDataPieces {
		fs.mu.Lock()
		if fs.chunkDataCurrent == nil {
			close(fs.chunkAvailable)
		}
		fs.fetchErr = errors.New("chunk fetch has failed")
		fs.mu.Unlock()
		return
	}

	// Recover the data.
	buf := bytes.NewBuffer(nil)
	chunkSize := (modules.SectorSize - fs.staticLayout.cipherType.Overhead()) * uint64(fs.staticLayout.fanoutDataPieces)
	err := fs.staticErasureCoder.Recover(pieces, chunkSize, buf)
	if err != nil {
		fs.mu.Lock()
		if fs.chunkDataCurrent == nil {
			close(fs.chunkAvailable)
		}
		fs.fetchErr = errors.New("erasure decoding of chunk failed.")
		fs.mu.Unlock()
		return
	}
	chunkData := buf.Bytes()

	fs.mu.Lock()
	defer fs.mu.Unlock()
	// Add the data to a chunk.
	if fs.chunkDataCurrent == nil {
		// Set the fetched chunk to the current chunk, and close the channel to
		// indicate to waiting threads that data is available.
		fs.chunkDataCurrent = chunkData
		close(fs.chunkAvailable)

		// There is no next chunk, and no thread fetching the next chunk. If the
		// file has a next chunk, fetch that.
		if fs.staticLayout.filesize > chunkSize*(chunkIndex+1) {
			go fs.threadedFetchChunk(chunkIndex + 1)
		}
	} else {
		// Set the next chunk to the fetched data. Buffers are now full, no need
		// to isuse another thread. There is also nobody waiting for the next
		// buffer, so the wait channel does not need to be set or closed.
		fs.chunkDataNext = chunkData
	}
}

// Close will close the fanout fetcher.
func (fs *fanoutStreamer) Close() error {
	// Release all arrays to clear up memory faster.
	fs.staticChunks = nil
	fs.chunkDataCurrent = nil
	fs.chunkDataNext = nil
	return nil
}

// Read will fetch the next set of data for the fanout fetcher. The current,
// naive implementation of the fanout fetcher will fetch 2 chunks in advance to
// support smooth, linear streaming. Partial downloads and seeking not currently
// supported.
func (fs *fanoutStreamer) Read(b []byte) (int, error) {
	// Loop, blocking until there is data in the cache. Hold the lock after
	// finding data in the cache.
	for {
		fs.mu.Lock()
		// Check whether there has been an error fetching the data.
		if fs.fetchErr != nil {
			err := fs.fetchErr
			fs.mu.Unlock()
			return 0, errors.AddContext(err, "error while fetching data")
		}
		// Check whether the fetch offset is equal to the filesize.
		if fs.offset == fs.staticLayout.filesize {
			fs.mu.Unlock()
			return 0, io.EOF
		}
		// Correct the input value if it exceeds the bounds of the file.
		if fs.offset+uint64(len(b)) > fs.staticLayout.filesize {
			b = b[:fs.staticLayout.filesize-fs.offset]
		}
		// Check whether the next data is available.
		if fs.chunkDataCurrent == nil {
			// Data is not available, block until there is data available.
			//
			// TODO: swap this out for a queue so that Read calls are processed
			// in-order.
			wait := fs.chunkAvailable
			fs.mu.Unlock()
			<-wait
			continue
		}

		// Data is available and this thread holds the mutex.
		break
	}
	defer fs.mu.Unlock()

	// Determine the offset of the current chunk to copy from.
	chunkSize := (modules.SectorSize - fs.staticLayout.cipherType.Overhead()) * uint64(fs.staticLayout.fanoutDataPieces)
	chunkOffset := fs.offset % chunkSize
	n := copy(b, fs.chunkDataCurrent[chunkOffset:])
	fs.offset += uint64(n)

	// Determine whether EOF has been reached. If EOF is reached, nothing left
	// to do. Do not return EOF, the next call to Read will return EOF.
	if fs.offset == fs.staticLayout.filesize {
		return n, nil
	}

	// Determine whether there is still data in the current chunk. If so,
	// nothing to do. Next call to read will pick up further along in the chunk.
	if chunkOffset+uint64(n) < chunkSize {
		return n, nil
	}

	// The current chunk is empty, rotate the next chunk into the current chunks
	// place.
	fs.chunkDataCurrent = fs.chunkDataNext
	fs.chunkDataNext = nil

	// If the new current chunk is nil, no thread needs to be issued to fetch
	// data, because that will have already happened. However, the channel for
	// blocking for data being available will need to be set.
	if fs.chunkDataCurrent == nil {
		fs.chunkAvailable = make(chan struct{})
		return n, nil
	}

	// If the new current chunk contains all remaining data, nothing needs to be
	// done, as all the remaining data has been fetched.
	if fs.offset+uint64(len(fs.chunkDataCurrent)) >= fs.staticLayout.filesize {
		return n, nil
	}

	// The next chunk needs to be fetched. Prior to this call to read, both
	// buffers had data in them, meaning that there was no work being performed.
	currentChunkIndex := fs.offset / chunkSize
	nextIndex := currentChunkIndex + 1
	go fs.threadedFetchChunk(nextIndex)
	return n, nil
}

// Seek will move the pointer for the streamer.
func (fs *fanoutStreamer) Seek(offset int64, whence int) (int64, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// If there's an immediate seek to the beginning, support that.
	if offset == 0 && whence == 0 && (fs.offset < uint64(len(fs.chunkDataCurrent)) || fs.offset == 0) {
		fs.offset = 0
		return 0, nil
	}
	// If there's a seek to the end while the offset is at the beginning,
	// support that too.
	if offset == 0 && whence == 2 && fs.offset == 0 {
		return int64(fs.staticLayout.filesize), nil
	}

	return 0, errors.New("seeking is not yet supported")
}

// linkfileEncodeFanout will create the serialized fanout for a fileNode.
func linkfileEncodeFanout(fileNode *filesystem.FileNode) ([]byte, error) {
	var fanout []byte
	var emptyHash crypto.Hash
	for i := uint64(0); i < fileNode.NumChunks(); i++ {
		pieces, err := fileNode.Pieces(i)
		if err != nil {
			return nil, errors.AddContext(err, "unable to get sector roots from file")
		}
		for _, pieceSet := range pieces {
			if len(pieceSet) > 0 {
				fanout = append(fanout, pieceSet[0].MerkleRoot[:]...)
			} else {
				fanout = append(fanout, emptyHash[:]...)
			}
		}
	}
	return fanout, nil
}
