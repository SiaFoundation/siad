package renter

import (
	"sync"
	"sync/atomic"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

// linkfileDecodeFanout will take an encoded data fanout and convert it into a
// more consumable format.
func linkfileDecodeFanout(encodedFanout []byte, dataPieces, parityPieces uint8) [][]crypto.Hash {
	var chunks [][]crypto.Hash
	piecesPerChunk := uint64(dataPieces) + uint64(parityPieces)
	chunkSize := crypto.HashSize * piecesPerChunk

	// Decode chunks until there is no longer enough data for a full chunk.
	for uint64(len(encodedFanout)) > chunkSize {
		chunk := make([]crypto.Hash, piecesPerChunk)
		for i := 0; i < len(chunk); i++ {
			copy(chunk[i][:], encodedFanout)
			encodedFanout = encodedFanout[crypto.HashSize:]
		}
		chunks = append(chunks, chunk)
	}
	return chunks
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

// threadedFetchPiece is a helper function which is a thread that tries to fetch
// a piece.
func (r *Renter) threadedFetchPiece(ll linkfileLayout, chunk []crypto.Hash, atomicBackupsUsed *uint64, indexToTry uint64) (indexFetched uint64, dataFetched []byte, err error) {
	sectorData, err := r.DownloadByRoot(chunk[indexToTry], 0, modules.SectorSize)
	if err != nil {
		backup := atomic.AddUint64(atomicBackupsUsed, 1)
		if uint64(ll.fanoutParityPieces) < backup {
			return 0, nil, errors.New("chunk fetch failed, a critical thread never found a piece")
		}
		nextIndexToTry := uint64(ll.fanoutDataPieces) + backup
		return r.threadedFetchPiece(ll, chunk, atomicBackupsUsed, nextIndexToTry)
	}
	return indexToTry, sectorData, nil
}

// fetchFanoutData will fetch the data of a file from its fanout. This is a
// method of a linkfileLayout because fetching the full fanout information
// requires most of the linkfile fields.
func (r *Renter) fetchFanoutData(ll linkfileLayout, chunks [][]crypto.Hash, dest downloadDestination) error {
	// Try grabbing the master key and erasure coder for the file before doing
	// any more work.
	masterKey, err := crypto.NewSiaKey(ll.cipherType, ll.cipherKey[:])
	if err != nil {
		return errors.AddContext(err, "count not recover siafile fanout because cipher key was unavailable")
	}
	ec, err := siafile.NewRSSubCode(int(ll.fanoutDataPieces), int(ll.fanoutParityPieces), crypto.SegmentSize)
	if err != nil {
		return errors.New("unable to initialize erasure code")
	}

	// Decrypt each chunk one at a time, no parallelism.
	for chunkIndex, chunk := range chunks {
		// Create an inline fetching function which will attempt to fetch a
		// piece, and then upon failure
		atomicBackupsUsed := new(uint64)

		// Spin up one fetch thread
		var wg sync.WaitGroup
		var chunkResult [][]byte
		var atomicErrors uint64
		for i := uint64(0); i < uint64(ll.fanoutDataPieces); i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				pieceIndexFetched, sectorData, err := r.threadedFetchPiece(ll, chunk, atomicBackupsUsed, i)
				if err != nil {
					atomic.AddUint64(&atomicErrors, 1)
					return
				}
				// Decrypt the result.
				key := masterKey.Derive(uint64(chunkIndex), pieceIndexFetched)
				key.DecryptBytesInPlace(sectorData, 0)
				chunkResult[pieceIndexFetched] = sectorData
			}()
		}
		wg.Wait()
		if atomicErrors > 0 {
			return errors.New("the fanout fetch failed")
		}

		// Enough pieces have been recovered, send them to the destination.
		pieceSize := modules.SectorSize - masterKey.Type().Overhead()
		fetchLen := uint64(ec.MinPieces()) * pieceSize
		err = dest.WritePieces(ec, chunkResult, 0, 0, fetchLen)
		if err != nil {
			return errors.AddContext(err, "unable to write pieces to the destination")
		}
	}
	return nil
}
