package renter

// skyfilefanout.go implements the encoding and decoding of skyfile fanouts. A
// fanout is a description of all of the Merkle roots in a file, organized by
// chunk. Each chunk has N pieces, and each piece has a Merkle root which is a
// 32 byte hash.
//
// The fanout is encoded such that the first 32 bytes are chunk 0 index 0, the
// second 32 bytes are chunk 0 index 1, etc... and then the second chunk is
// appended immediately after, and so on.

import (
	"fmt"
	"io"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem/siafile"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/errors"
)

// fanoutStreamBufferDataSource implements streamBufferDataSource with the
// skyfile so that it can be used to open a stream from the streamBufferSet.
type fanoutStreamBufferDataSource struct {
	// Each chunk is an array of sector hashes that correspond to pieces which
	// can be fetched.
	staticChunks       [][]crypto.Hash
	staticChunkSize    uint64
	staticErasureCoder modules.ErasureCoder
	staticLayout       modules.SkyfileLayout
	staticMasterKey    crypto.CipherKey
	staticMetadata     modules.SkyfileMetadata
	staticStreamID     modules.DataSourceID

	// staticTimeout defines a timeout that is applied to every chunk download
	staticTimeout time.Duration

	// Utils.
	staticRenter *Renter
	mu           sync.Mutex
}

// newFanoutStreamer will create a modules.Streamer from the fanout of a
// skyfile. The streamer is created by implementing the streamBufferDataSource
// interface on the skyfile, and then passing that to the stream buffer set.
func (r *Renter) newFanoutStreamer(link modules.Skylink, sl modules.SkyfileLayout, metadata modules.SkyfileMetadata, fanoutBytes []byte, timeout time.Duration, sk skykey.Skykey) (modules.Streamer, error) {
	masterKey, err := r.deriveFanoutKey(&sl, sk)
	if err != nil {
		return nil, errors.AddContext(err, "count not recover siafile fanout because cipher key was unavailable")
	}

	// Create the erasure coder
	ec, err := modules.NewRSSubCode(int(sl.FanoutDataPieces), int(sl.FanoutParityPieces), crypto.SegmentSize)
	if err != nil {
		return nil, errors.New("unable to initialize erasure code")
	}

	// Build the base streamer object.
	fs := &fanoutStreamBufferDataSource{
		staticChunkSize:    modules.SectorSize * uint64(sl.FanoutDataPieces),
		staticErasureCoder: ec,
		staticLayout:       sl,
		staticMasterKey:    masterKey,
		staticMetadata:     metadata,
		staticStreamID:     link.DataSourceID(),
		staticTimeout:      timeout,
		staticRenter:       r,
	}
	err = fs.decodeFanout(fanoutBytes)
	if err != nil {
		return nil, errors.AddContext(err, "unable to decode fanout of skyfile")
	}

	// Grab and return the stream.
	stream := r.staticStreamBufferSet.callNewStream(fs, 0)
	return stream, nil
}

// decodeFanout will take the fanout bytes from a skyfile and decode them in to
// the staticChunks filed of the fanoutStreamBufferDataSource.
func (fs *fanoutStreamBufferDataSource) decodeFanout(fanoutBytes []byte) error {
	// Decode piecesPerChunk, chunkRootsSize, and numChunks
	piecesPerChunk, chunkRootsSize, numChunks, err := modules.DecodeFanout(fs.staticLayout, fanoutBytes)
	if err != nil {
		return err
	}

	// Decode the fanout data into the list of chunks for the
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
	return nil
}

// skyfileEncodeFanout will create the serialized fanout for a fileNode. The
// encoded fanout is just the list of hashes that can be used to retrieve a file
// concatenated together, where piece 0 of chunk 0 is first, piece 1 of chunk
// 0 is second, etc. The full set of erasure coded pieces are included.
//
// There is a special case for unencrypted 1-of-N files. Because every piece is
// identical for an unencrypted 1-of-N file, only the first piece of each chunk
// is included.
//
// NOTE: This method should not be called unless the fileNode is available,
// meaning that all the dataPieces have been uploaded.
func skyfileEncodeFanout(fileNode *filesystem.FileNode, reader io.Reader) ([]byte, error) {
	// Grab the erasure coding scheme and encryption scheme from the file.
	cipherType := fileNode.MasterKey().Type()
	dataPieces := fileNode.ErasureCode().MinPieces()
	onlyOnePieceNeeded := dataPieces == 1 && cipherType == crypto.TypePlain

	// If only one piece is needed, or if no reader was passed in, then we can
	// generate the encoded fanout from the fileNode.
	if onlyOnePieceNeeded || reader == nil {
		return skyfileEncodeFanoutFromFileNode(fileNode, onlyOnePieceNeeded)
	}

	// If we need all the pieces, then we need to generate the encoded fanout
	// from the reader since we cannot assume that all the parity pieces have
	// been uploaded.
	return skyfileEncodeFanoutFromReader(fileNode, reader)
}

// skyfileEncodeFanoutFromFileNode will create the serialized fanout for
// a fileNode. The encoded fanout is just the list of hashes that can be used to
// retrieve a file concatenated together, where piece 0 of chunk 0 is first,
// piece 1 of chunk 0 is second, etc. This method assumes the  special case for
// unencrypted 1-of-N files. Because every piece is identical for an unencrypted
// 1-of-N file, only the first piece of each chunk is included.
func skyfileEncodeFanoutFromFileNode(fileNode *filesystem.FileNode, onePiece bool) ([]byte, error) {
	// Allocate the memory for the fanout.
	fanout := make([]byte, 0, fileNode.NumChunks()*crypto.HashSize)

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
		if onePiece {
			root := emptyHash
			for _, pieceSet := range allPieces {
				root = findPieceInPieceSet(pieceSet)
				if root != emptyHash {
					fanout = append(fanout, root[:]...)
					break
				}
			}
			// If root is still equal to emptyHash it means that we didn't add a piece
			// root for this chunk.
			if root == emptyHash {
				err = fmt.Errorf("No piece root encoded for chunk %v", i)
				build.Critical(err)
				return nil, err
			}
			continue
		}

		// Generate all the piece roots
		for pi, pieceSet := range allPieces {
			root := findPieceInPieceSet(pieceSet)
			if root == emptyHash {
				err = fmt.Errorf("Empty piece root at index %v found for chunk %v", pi, i)
				build.Critical(err)
				return nil, err
			}
			fanout = append(fanout, root[:]...)
		}
	}
	return fanout, nil
}

// skyfileEncodeFanoutFromReader will create the serialized fanout for
// a fileNode. The encoded fanout is just the list of hashes that can be used to
// retrieve a file concatenated together, where piece 0 of chunk 0 is first,
// piece 1 of chunk 0 is second, etc. The full set of erasure coded pieces are
// included.
func skyfileEncodeFanoutFromReader(fileNode *filesystem.FileNode, reader io.Reader) ([]byte, error) {
	// Safety check
	if reader == nil {
		err := errors.New("skyfileEncodeFanoutFromReader called with nil reader")
		build.Critical(err)
		return nil, err
	}

	// Generate the remaining pieces of the each chunk to build the fanout bytes
	numPieces := fileNode.ErasureCode().NumPieces()
	fanout := make([]byte, 0, fileNode.NumChunks()*uint64(numPieces)*crypto.HashSize)
	for chunkIndex := uint64(0); chunkIndex < fileNode.NumChunks(); chunkIndex++ {
		// Allocate data pieces and fill them with data from the reader.
		dataPieces, _, err := readDataPieces(reader, fileNode.ErasureCode(), fileNode.PieceSize())
		if err != nil {
			return nil, errors.AddContext(err, "unable to get dataPieces from chunk")
		}

		// Encode the data pieces, forming the chunk's logical data.
		logicalChunkData, _ := fileNode.ErasureCode().EncodeShards(dataPieces)
		for pieceIndex := range logicalChunkData {
			// Encrypt and pad the piece with the given index.
			padAndEncryptPiece(chunkIndex, uint64(pieceIndex), logicalChunkData, fileNode.MasterKey())
			root := crypto.MerkleRoot(logicalChunkData[pieceIndex])
			// Unlike in skyfileEncodeFanoutFromFileNode we don't check for an
			// emptyHash here since if MerkleRoot returned an emptyHash it would mean
			// that an emptyHash is a valid MerkleRoot and a host should be able to
			// return the corresponding data.
			fanout = append(fanout, root[:]...)
		}
	}
	return fanout, nil
}

// DataSize returns the amount of file data in the underlying skyfile.
func (fs *fanoutStreamBufferDataSource) DataSize() uint64 {
	return fs.staticLayout.Filesize
}

// ID returns the id of the skylink being fetched, this is just the hash of the
// skylink.
func (fs *fanoutStreamBufferDataSource) ID() modules.DataSourceID {
	return fs.staticStreamID
}

// Metadata returns the metadata of the skylink being fetched.
func (fs *fanoutStreamBufferDataSource) Metadata() modules.SkyfileMetadata {
	return fs.staticMetadata
}

// Layout returns the layout of the skylink being fetched.
func (fs *fanoutStreamBufferDataSource) Layout() modules.SkyfileLayout {
	return fs.staticLayout
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
	if uint64(offset)+uint64(len(b)) > fs.staticLayout.Filesize {
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

// RequestSize implements streamBufferDataSource and will return the size of a
// logical data chunk.
func (fs *fanoutStreamBufferDataSource) RequestSize() uint64 {
	return fs.staticChunkSize
}

// SilentClose will clean up any resources that the fanoutStreamBufferDataSource
// keeps open.
func (fs *fanoutStreamBufferDataSource) SilentClose() {
	// Nothing to clean up.
	return
}
