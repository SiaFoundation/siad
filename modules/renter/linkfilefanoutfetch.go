package renter

import (
	"bytes"
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

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

// managedFetchChunk will grab the data of a specific chunk index from the Sia
// network.
func (fs *fanoutStreamBufferDataSource) managedFetchChunk(chunkIndex uint64) ([]byte, error) {
	// Input verification.
	if chunkIndex*fs.staticChunkSize >= fs.staticLayout.filesize {
		return nil, errors.New("requesting a chunk index that does not exist within the file")
	}
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

	// Special case: if there is only 1 data piece, return the data directly.
	//
	// TODO: May need to parse these pieces out.
	if fs.staticLayout.fanoutDataPieces == 1 {
		return pieces[0], nil
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
