package renter

import (
	"bytes"
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

// fanoutStreamer implements streamBufferDataSource with the linkfile so that it
// can open a stream from the streamBufferSet. That stream is then embedded in
// the fanoutStreamer so that the fanoutStreamer satisfies the modules.Streamer
// interface necessary for streaming data.
type fanoutStreamer struct {
	// Each chunk is an array of sector hashes that correspond to pieces which
	// can be fetched.
	staticChunks       [][]crypto.Hash
	staticChunkSize    uint64
	staticErasureCoder modules.ErasureCoder
	staticLayout       linkfileLayout
	staticMasterKey    crypto.CipherKey
	staticStreamID     streamDataSourceID

	// Utils.
	staticRenter *Renter
	mu           sync.Mutex

	// Embed the stream so that the fanout streamer satisfies the
	// modules.Streamer interface.
	*stream
}

// linkfileDecodeFanout will take an encoded data fanout and convert it into a
// more consumable format.
func (r *Renter) newFanoutStreamer(link modules.Sialink, ll linkfileLayout, fanoutBytes []byte) (*fanoutStreamer, error) {
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
		staticChunkSize:    modules.SectorSize * uint64(ll.fanoutDataPieces),
		staticErasureCoder: ec,
		staticLayout:       ll,
		staticMasterKey:    masterKey,
		staticStreamID:     streamDataSourceID(crypto.HashObject(link.String())),

		staticRenter: r,
	}
	// Special case: if the data of the file is using 1-of-N erasure coding,
	// each piece will be identical, so the fanout will only encode a single
	// piece for each chunk.
	var piecesPerChunk uint64
	var chunkRootsSize uint64
	if ll.fanoutDataPieces == 1 && ll.cipherType == crypto.TypePlain {
		// Quick sanity check - the fanout bytes should be an even number of
		// chunks.
		if len(fanoutBytes)%crypto.HashSize != 0 {
			return nil, errors.New("the fanout bytes are not a multiple of crypto.HashSize")
		}
		piecesPerChunk = 1
		chunkRootsSize = crypto.HashSize
	} else {
		// This is the case where the file data is not 1-of-N. Every piece is
		// different, so every piece must get enumerated.
		//
		// Sanity check - the fanout bytes should be an even number of chunks.
		piecesPerChunk := uint64(ll.fanoutDataPieces) + uint64(ll.fanoutParityPieces)
		chunkRootsSize := crypto.HashSize * piecesPerChunk
		if uint64(len(fanoutBytes))%chunkRootsSize != 0 {
			return nil, errors.New("the fanout bytes do not contain an even number of chunks")
		}
	}

	// Copy the fanout data into the list of chunks for the fanoutStreamer.
	fs.staticChunks = make([][]crypto.Hash, 0, uint64(len(fanoutBytes))/chunkRootsSize)
	for i := uint64(0); i < uint64(len(fanoutBytes)); i += chunkRootsSize {
		chunk := make([]crypto.Hash, piecesPerChunk)
		for j := uint64(0); j < uint64(len(chunk)); j += crypto.HashSize {
			copy(chunk[j/chunkRootsSize][:], fanoutBytes[i+j:])
		}
		fs.staticChunks = append(fs.staticChunks, chunk)
	}

	// Grab and return the stream.
	stream := r.staticStreamBufferSet.callNewStream(fs, 0)
	fs.stream = stream
	return fs, nil
}

// completed returns whether enough data pieces were retrieved for the chunk to
// be recovered successfully.
func (fcs *fetchChunkState) completed() bool {
	return fcs.piecesCompleted >= fcs.staticDataPieces
}

// SilentClose will clean up any resources that the fanoutStreamer keeps open.
func (fs *fanoutStreamer) SilentClose() {
	// Close the stream.
	err := fs.stream.Close()
	if err != nil {
		fs.staticRenter.log.Println("error closing stream opened by fanoutStreamer:", err)
	}
	return
}

// DataSize returns the amount of file data in the underlying linkfile.
func (fs *fanoutStreamer) DataSize() uint64 {
	return fs.staticLayout.filesize
}

// ID returns the id of the sialink being fetched, this is just the hash of the
// sialink.
func (fs *fanoutStreamer) ID() streamDataSourceID {
	return fs.staticStreamID
}

// ReadAt will fetch data from the siafile at the provided offset.
func (fs *fanoutStreamer) ReadAt(b []byte, offset int64) (int, error) {
	// Input checking.
	if offset < 0 {
		return 0, errors.New("cannot read from a negative offset")
	}
	// Can only grab one chunk.
	if uint64(len(b)) > fs.staticChunkSize {
		return 0, errors.New("request needs to be no more than RequestSize()")
	}
	// Must start at the chunk boundary.
	if uint64(offset)%fs.staticChunkSize != 0 {
		return 0, errors.New("request needs to be aligned to RequestSize()")
	}
	// Must not go beyond the end of the file.
	if uint64(offset)+uint64(len(b)) > fs.staticLayout.filesize {
		return 0, errors.New("making a read request that goes beyond the boundaries of the file")
	}

	// Determine which chunk contains the data.
	chunkIndex := uint64(offset) / fs.staticChunkSize

	// Perform a download to fetch the chunk.
	chunkData, err := fs.managedFetchChunk(chunkIndex)
	if err != nil {
		return 0, errors.AddContext(err, "unable to fetch chunk in ReadAt call on fanout streamer")
	}
	n := copy(b, chunkData)
	return n, nil
}

// RequestSize implements streamBufferDataSource and will return the
// chunk size of the file.
func (fs *fanoutStreamer) RequestSize() uint64 {
	return fs.staticChunkSize
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

// managedFetchChunk will grab the data of a specific chunk index from the Sia
// network.
func (fs *fanoutStreamer) managedFetchChunk(chunkIndex uint64) ([]byte, error) {
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

// linkfileEncodeFanout will create the serialized fanout for a fileNode. The
// encoded fanout is just the list of hashes that can be used to retrieve a file
// concatenated together, where piece 0 of chunk 0 is first, piece 1 of chunk 0
// is second, etc. The full set of erasure coded pieces are included.
//
// There is a special case for unencrypted 1-of-N files. Because every piece is
// identical for an unencrypted 1-of-N file, only the first piece of each chunk
// is included.
func linkfileEncodeFanout(fileNode *filesystem.FileNode) ([]byte, error) {
	// Grab the erasure coding scheme and encryption scheme from the file.
	cipherType := fileNode.MasterKey().Type()
	dataPieces := fileNode.ErasureCode().MinPieces()
	numPieces := fileNode.ErasureCode().NumPieces()
	onlyOnePieceNeeded := dataPieces == 1 && cipherType == crypto.TypePlain

	// Allocate the memory for the fanout.
	var fanout []byte
	if onlyOnePieceNeeded {
		fanout = make([]byte, 0, fileNode.NumChunks()*crypto.HashSize)
	} else {
		fanout = make([]byte, 0, fileNode.NumChunks()*uint64(numPieces)*crypto.HashSize)
	}

	// findPieceInPieceSet will scan through a piece set and return the first
	// non-empty piece in the set. If the set is empty, or every piece in the
	// set is empty, then the emptyHash is returned.
	var emptyHash crypto.Hash
	findPieceInPieceSet := func (pieceSet []siafile.Piece) crypto.Hash {
		for _, piece := range pieceSet {
			if piece.MerkleRoot != emptyHash {
				return piece.MerkleRoot
			}
		}
		return emptyHash
	}

	// Build the fanout one chunk at a time.
	for i := uint64(0); i < fileNode.NumChunks(); i++ {
		// Get the pieces for this chunk.
		allPieces, err := fileNode.Pieces(i)
		if err != nil {
			return nil, errors.AddContext(err, "unable to get sector roots from file")
		}

		// Special case: if only one piece is needed, only use the first piece
		// that is available. This is because 1-of-N files are encoded more
		// compactly in the fanout.
		if onlyOnePieceNeeded {
			for _, pieceSet := range allPieces {
				root := findPieceInPieceSet(pieceSet)
				if root != emptyHash {
					fanout = append(fanout, root[:]...)
					break
				}
			}
			continue
		}

		// General case: get one root per piece.
		for _, pieceSet := range allPieces {
			root := findPieceInPieceSet(pieceSet)
			fanout = append(fanout, root[:]...)
		}
	}
	return fanout, nil
}
