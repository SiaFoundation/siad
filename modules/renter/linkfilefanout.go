package renter

import (
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

// fanoutStreamBufferDataSource implements streamBufferDataSource with the
// linkfile so that it can open a stream from the streamBufferSet. The stream is
// then added as a field of the fanoutStreamBufferDataSource so that it can be
// returned separately.
type fanoutStreamBufferDataSource struct {
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

}

// newFanoutStreamer will create a modules.Streamer from the fanout of a
// linkfile. The streamer is created by implementing the streamBufferDataSource
// interface on the linkfile, and then passing that to the stream buffer set.
func (r *Renter) newFanoutStreamer(link modules.Sialink, ll linkfileLayout, fanoutBytes []byte) (modules.Streamer, error) {
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
	fs := &fanoutStreamBufferDataSource{
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
		piecesPerChunk = uint64(ll.fanoutDataPieces) + uint64(ll.fanoutParityPieces)
		chunkRootsSize = crypto.HashSize * piecesPerChunk
		if uint64(len(fanoutBytes))%chunkRootsSize != 0 {
			return nil, errors.New("the fanout bytes do not contain an even number of chunks")
		}
	}
	numChunks := uint64(len(fanoutBytes))/chunkRootsSize

	// Copy the fanout data into the list of chunks for the
	// fanoutStreamBufferDataSource.
	fs.staticChunks = make([][]crypto.Hash, 0, numChunks)
	for i := uint64(0); i < numChunks; i++ {
		chunk := make([]crypto.Hash, piecesPerChunk)
		for j := uint64(0); j < piecesPerChunk; j++ {
			fanoutOffset := (i * chunkRootsSize) + (j * crypto.HashSize)
			copy(chunk[j][:], fanoutBytes[fanoutOffset:])
		}
		fs.staticChunks = append(fs.staticChunks, chunk)
	}

	// Grab and return the stream.
	stream := r.staticStreamBufferSet.callNewStream(fs, 0)
	return stream, nil
}

// completed returns whether enough data pieces were retrieved for the chunk to
// be recovered successfully.
func (fcs *fetchChunkState) completed() bool {
	return fcs.piecesCompleted >= fcs.staticDataPieces
}

// SilentClose will clean up any resources that the fanoutStreamBufferDataSource
// keeps open.
func (fs *fanoutStreamBufferDataSource) SilentClose() {
	// Nothing to clean up.
	return
}

// DataSize returns the amount of file data in the underlying linkfile.
func (fs *fanoutStreamBufferDataSource) DataSize() uint64 {
	return fs.staticLayout.filesize
}

// ID returns the id of the sialink being fetched, this is just the hash of the
// sialink.
func (fs *fanoutStreamBufferDataSource) ID() streamDataSourceID {
	return fs.staticStreamID
}

// ReadAt will fetch data from the siafile at the provided offset.
func (fs *fanoutStreamBufferDataSource) ReadAt(b []byte, offset int64) (int, error) {
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
func (fs *fanoutStreamBufferDataSource) RequestSize() uint64 {
	return fs.staticChunkSize
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
	findPieceInPieceSet := func(pieceSet []siafile.Piece) crypto.Hash {
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
