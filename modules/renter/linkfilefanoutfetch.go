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
	staticChunkIndex uint64
	staticChunkSize  uint64
	staticDataPieces uint64
	staticMasterKey  crypto.CipherKey

	pieces          [][]byte
	piecesCompleted uint64
	piecesFailed    uint64

	doneChan     chan struct{}
	mu           sync.Mutex
	staticRenter *Renter
}

// completed returns whether enough data pieces were retrieved for the chunk to
// be recovered successfully.
func (fcs *fetchChunkState) completed() bool {
	return fcs.piecesCompleted >= fcs.staticDataPieces
}

// managedFailPiece will update the fcs to indicate that an additional piece has
// failed. Nothing will be logged, if the cause of failure is worth logging, the
// log message should be handled by the calling function.
func (fcs *fetchChunkState) managedFailPiece() {
	fcs.mu.Lock()
	defer fcs.mu.Unlock()

	fcs.piecesFailed++
	allTried := fcs.piecesCompleted+fcs.piecesFailed == uint64(len(fcs.pieces))
	if !fcs.completed() && allTried {
		close(fcs.doneChan)
	}
}

// threadedFetchPiece is intended to run as a separate thread which fetches a
// particular piece of a chunk in the fanout.
func (fcs *fetchChunkState) threadedFetchPiece(pieceIndex uint64, pieceRoot crypto.Hash) {
	// Fetch the piece.
	//
	// TODO: This is fetching from 0 to modules.SectorSize, for the final chunk
	// we don't need to fetch the whole piece. Fine for now as it only impacts
	// larger files.
	//
	// TODO: Ideally would be able to insert 'doneChan' as a cancelChan on the
	// DownloadByRoot call.
	pieceData, err := fcs.staticRenter.DownloadByRoot(pieceRoot, 0, modules.SectorSize)
	if err != nil {
		// TODO: May want to log here.
		fcs.managedFailPiece()
		return
	}

	// Decrypt the piece.
	key := fcs.staticMasterKey.Derive(fcs.staticChunkIndex, pieceIndex)
	_, err = key.DecryptBytesInPlace(pieceData, 0)
	if err != nil {
		// TODO: Definitely want to log here.
		fcs.managedFailPiece()
		return
	}

	// Update the fetchChunkState to reflect that the piece has been recovered.
	fcs.mu.Lock()
	defer fcs.mu.Unlock()
	// If the chunk is already completed, the data should be discarded.
	if fcs.completed() {
		pieceData = nil
		return
	}
	// Add the piece to the fcs.
	fcs.pieces[pieceIndex] = pieceData
	fcs.piecesCompleted++
	// Close out the chunk download if this was the final piece needed to
	// complete the download.
	if fcs.completed() {
		close(fcs.doneChan)
	}
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

	// Build the state that is used to coordinate the goroutines fetching
	// various pieces.
	fcs := fetchChunkState{
		staticChunkIndex: chunkIndex,
		staticChunkSize:  fs.staticChunkSize,
		staticDataPieces: uint64(fs.staticLayout.fanoutDataPieces),
		staticMasterKey:  fs.staticMasterKey,

		pieces: make([][]byte, len(fs.staticChunks[chunkIndex])),

		doneChan:     make(chan struct{}),
		staticRenter: fs.staticRenter,
	}

	// Spin up one goroutine per piece to fetch the pieces.
	//
	// TODO: Currently this means that if there are 30 pieces for a chunk, all
	// 30 pieces will be requested. This is wasteful, much better would be to
	// attempt to fetch some fraction with some amount of overdrive.
	var blankHash crypto.Hash
	for i := uint64(0); i < uint64(len(fcs.pieces)); i++ {
		// Skip pieces where the Merkle root is not supplied.
		//
		// TODO: May want to log here.
		if fs.staticChunks[chunkIndex][i] == blankHash {
			fcs.managedFailPiece()
			continue
		}

		// Spin up a thread to fetch this piece.
		go fcs.threadedFetchPiece(i, fs.staticChunks[chunkIndex][i])
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
		fcs.pieces = nil
		pieces = nil
		fcs.mu.Unlock()
		return nil, errors.New("not enough pieces could be recovered to fetch chunk")
	}

	// Special case: if there is only 1 piece, there is no need to run erasure
	// coding.
	if len(pieces) == 1 {
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
