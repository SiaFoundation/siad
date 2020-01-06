package renter

// TODO: This file is pretty much going to devlove into just implementing a
// dataSource for the streambuffer.

import (
	"bytes"
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
	staticChunkSize    uint64
	staticErasureCoder modules.ErasureCoder
	staticLayout       linkfileLayout
	staticMasterKey    crypto.CipherKey

	// Utils.
	staticRenter *Renter
	mu           sync.Mutex

	// Embed the stream so that the fanout streamer satisfies the
	// modules.Streamer interface.
	*stream
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
	piecesPerChunk := uint64(ll.fanoutDataPieces) + uint64(ll.fanoutParityPieces)
	chunkSize := crypto.HashSize * piecesPerChunk
	fs := &fanoutStreamer{
		staticChunkSize:    chunkSize,
		staticErasureCoder: ec,
		staticLayout:       ll,
		staticMasterKey:    masterKey,

		staticRenter: r,
	}

	// Decode the fanout to get the chunk fetch data.
	for uint64(len(fanoutBytes)) >= chunkSize {
		chunk := make([]crypto.Hash, piecesPerChunk)
		for i := 0; i < len(chunk); i++ {
			copy(chunk[i][:], fanoutBytes)
			fanoutBytes = fanoutBytes[crypto.HashSize:]
		}
		fs.staticChunks = append(fs.staticChunks, chunk)
	}

	// TODO: Hash the sialink instead?
	fsStreamID := streamDataSourceID(crypto.HashObject(ll))
	stream := r.staticStreamBufferSet.callNewStream(fs, fsStreamID, 0)
	fs.stream = stream
	return fs, nil
}

// fetchChunkState is a helper struct for coordinating goroutines that are
// attempting to download a chunk for a fanout streamer.
type fetchChunkState struct {
	staticDataPieces uint64
	staticChunkSize  uint64

	pieces          [][]byte
	piecesCompleted uint64
	piecesFailed    uint64

	doneChan chan struct{}
	mu       sync.Mutex
}

// completed returns whether enough data pieces were retrieved for the chunk to
// be recovered successfully.
func (fcs *fetchChunkState) completed() bool {
	return fcs.piecesCompleted >= fcs.staticDataPieces
}

// TODO: We may have some resrouces to clean up, this blank implementation is
// probably not correct.
func (fs *fanoutStreamer) Close() error {
	return nil
}

// DataSize returns the amount of file data in the underlying linkfile.
func (fs *fanoutStreamer) DataSize() uint64 {
	return fs.staticLayout.filesize
}

// ReadAt will fetch data from the siafile at the provided offset.
func (fs *fanoutStreamer) ReadAt(b []byte, offset int64) (int, error) {
	// Input checking.
	if offset < 0 {
		return 0, errors.New("cannot read from a negative offset")
	}
	if uint64(len(b)) != fs.staticChunkSize {
		return 0, errors.New("request needs to be SuggestedRequestSize()")
	}
	if uint64(offset)%fs.staticChunkSize != 0 {
		return 0, errors.New("request needs to be aligned to SuggestedRequestSize()")
	}

	// Determine which chunk contains the data.
	chunkIndex := uint64(offset) / fs.staticChunkSize

	// Perform a download to fetch the chunk.
	chunkData, err := fs.managedFetchChunk(chunkIndex)
	if err != nil {
		return 0, err
	}
	n := copy(b, chunkData)
	return n, nil
}

// SuggestedRequestSize implements streamBufferDataSource and will return the
// chunk size of the file.
func (fs *fanoutStreamer) SuggestedRequestSize() uint64 {
	return fs.staticChunkSize
}

// managedFetchChunk will grab the data of a specific chunk index from the Sia
// network.
func (fs *fanoutStreamer) managedFetchChunk(chunkIndex uint64) ([]byte, error) {
	if int(fs.staticLayout.fanoutDataPieces) > len(fs.staticChunks[chunkIndex]) {
		return nil, errors.New("not enough pieces in the chunk to recover the chunk")
	}

	// Spin up one thread per piece and try to fetch them all. This is wasteful,
	// but easier to implement. Intention at least for now is just to get
	// end-to-end testing working for this feature.
	//
	// TODO: Restructure the concurrency here to interrupt/kill DownloadByRoot
	// calls which aren't needed anymore.
	//
	// TODO: Really just re-write this whole beast of a thing, it's crappy code.
	var blankHash crypto.Hash
	fcs := fetchChunkState{
		staticChunkSize:  fs.staticChunkSize,
		staticDataPieces: uint64(fs.staticLayout.fanoutDataPieces),

		pieces:   make([][]byte, len(fs.staticChunks[chunkIndex])),
		doneChan: make(chan struct{}),
	}
	piecesFetched := make(map[crypto.Hash]struct{})
	for i := uint64(0); i < uint64(len(fcs.pieces)); i++ {
		// Skip pieces where the Merkle root is not supplied.
		if fs.staticChunks[chunkIndex][i] == blankHash {
			fcs.mu.Lock()
			fcs.piecesFailed++
			allTried := fcs.piecesCompleted+fcs.piecesFailed == uint64(len(fcs.pieces))
			if !fcs.completed() && allTried {
				close(fcs.doneChan)
			}
			fcs.mu.Unlock()
			continue
		}
		// Skip pieces where the download has already been issued. This is
		// particularly useful for 1-of-N files.
		_, exists := piecesFetched[fs.staticChunks[chunkIndex][i]]
		if exists {
			continue
		}
		piecesFetched[fs.staticChunks[chunkIndex][i]] = struct{}{}

		// Spin up a thread to fetch this piece.
		go func(i uint64) {
			pieceData, err := fs.staticRenter.DownloadByRoot(fs.staticChunks[chunkIndex][i], 0, modules.SectorSize)
			if err != nil {
				fcs.mu.Lock()
				fcs.piecesFailed++
				allTried := fcs.piecesCompleted+fcs.piecesFailed == uint64(len(fcs.pieces))
				if !fcs.completed() && allTried {
					close(fcs.doneChan)
				}
				fcs.mu.Unlock()
				// TODO: Log that there was a failure to fetch a root?
				return
			}
			key := fs.staticMasterKey.Derive(chunkIndex, i)
			key.DecryptBytesInPlace(pieceData, 0)
			fcs.mu.Lock()
			if fcs.completed() {
				pieceData = nil
				fcs.mu.Unlock()
				return
			}
			fcs.pieces[i] = pieceData
			fcs.piecesCompleted++
			if fcs.completed() {
				close(fcs.doneChan)
			}
			fcs.mu.Unlock()
		}(i)
	}
	<-fcs.doneChan

	// Check how many pieces came back.
	fcs.mu.Lock()
	completed := fcs.completed()
	pieces := fcs.pieces
	fcs.mu.Unlock()
	if !completed {
		fcs.mu.Lock()
		for i := 0; i < len(fcs.pieces); i++ {
			fcs.pieces[i] = nil
		}
		fcs.mu.Unlock()
		return nil, errors.New("not enough pieces could be recovered to fetch chunk")
	}

	// Recover the data.
	buf := bytes.NewBuffer(nil)
	chunkSize := (modules.SectorSize - fs.staticLayout.cipherType.Overhead()) * uint64(fs.staticLayout.fanoutDataPieces)
	err := fs.staticErasureCoder.Recover(pieces, chunkSize, buf)
	if err != nil {
		return nil, errors.New("erasure decoding of chunk failed.")
	}
	return buf.Bytes(), nil
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
