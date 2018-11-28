package siafile

import (
	"fmt"
	"io"

	"github.com/klauspost/reedsolomon"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// RSCode is a Reed-Solomon encoder/decoder. It implements the
// modules.ErasureCoder interface.
type RSCode struct {
	enc reedsolomon.Encoder

	numPieces  int
	dataPieces int
}

// NumPieces returns the number of pieces returned by Encode.
func (rs *RSCode) NumPieces() int { return rs.numPieces }

// MinPieces return the minimum number of pieces that must be present to
// recover the original data.
func (rs *RSCode) MinPieces() int { return rs.dataPieces }

// Encode splits data into equal-length pieces, some containing the original
// data and some containing parity data.
func (rs *RSCode) Encode(data []byte) ([][]byte, error) {
	pieces, err := rs.enc.Split(data)
	if err != nil {
		return nil, err
	}
	// err should not be possible if Encode is called on the result of Split,
	// but no harm in checking anyway.
	err = rs.enc.Encode(pieces)
	if err != nil {
		return nil, err
	}
	return pieces, nil
}

// EncodeShards creates the parity shards for an already sharded input.
func (rs *RSCode) EncodeShards(pieces [][]byte, pieceSize uint64) ([][]byte, error) {
	// Check that the caller provided the minimum amount of pieces.
	if len(pieces) != rs.MinPieces() {
		return nil, fmt.Errorf("invalid number of pieces given %v %v", len(pieces), rs.MinPieces())
	}
	// Add the parity shards to pieces.
	for len(pieces) < rs.NumPieces() {
		pieces = append(pieces, make([]byte, pieceSize))
	}
	err := rs.enc.Encode(pieces)
	if err != nil {
		return nil, err
	}
	return pieces, nil
}

// EncodeSubShards encodes data in a way that every 64 bytes of the encoded
// data can be decoded independently.
// The input 'pieces' needs to have the following properties:
//   len(pieces) == pieceSize / segmentSize
//   len(pieces[i]) == rs.MinPieces
//   len(pieces[i][j]) == segmentSize
func (rs *RSCode) EncodeSubShards(pieces [][][]byte, pieceSize, segmentSize uint64) ([][][]byte, error) {
	// pieceSize must be divisible by segmentSize
	if pieceSize%segmentSize != 0 {
		return nil, errors.New("pieceSize not divisible by segmentSize")
	}
	// Check that there are enough subshards to make up for a whole piece.
	if uint64(len(pieces)) != pieceSize/segmentSize {
		return nil, fmt.Errorf("not enough subshards expected %v but was %v",
			pieceSize/segmentSize, len(pieces))
	}
	for i := range pieces {
		// Check that the caller provided the minimum amount of pieces.
		if len(pieces[i]) != rs.MinPieces() {
			return nil, fmt.Errorf("invalid number of pieces given %v %v",
				len(pieces), rs.MinPieces())
		}
		// Check that each subshard consists of the minimum amount of pieces.
		for j := range pieces[i] {
			if uint64(len(pieces[i][j])) != segmentSize {
				return nil, errors.New("segments have wrong size")
			}
		}
	}
	// Add pieces until pieces has the correct length.
	for i := range pieces {
		for len(pieces[i]) < rs.NumPieces() {
			pieces[i] = append(pieces[i], make([]byte, segmentSize))
		}
	}
	// Encode the pieces.
	for i := range pieces {
		if err := rs.enc.Encode(pieces[i]); err != nil {
			return nil, err
		}
	}
	return pieces, nil
}

// Recover recovers the original data from pieces and writes it to w.
// pieces should be identical to the slice returned by Encode (length and
// order must be preserved), but with missing elements set to nil.
func (rs *RSCode) Recover(pieces [][]byte, n uint64, w io.Writer) error {
	err := rs.enc.ReconstructData(pieces)
	if err != nil {
		return err
	}
	return rs.enc.Join(w, pieces, int(n))
}

// NewRSCode creates a new Reed-Solomon encoder/decoder using the supplied
// parameters.
func NewRSCode(nData, nParity int) (modules.ErasureCoder, error) {
	enc, err := reedsolomon.New(nData, nParity)
	if err != nil {
		return nil, err
	}
	return &RSCode{
		enc:        enc,
		numPieces:  nData + nParity,
		dataPieces: nData,
	}, nil
}
