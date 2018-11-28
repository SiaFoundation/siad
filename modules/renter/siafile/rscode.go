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
// The result will be a slice of rs.NumPieces pieces of pieceSize.
func (rs *RSCode) EncodeSubShards(segments [][][]byte, pieceSize, segmentSize uint64) ([][]byte, error) {
	// pieceSize must be divisible by segmentSize
	if pieceSize%segmentSize != 0 {
		return nil, errors.New("pieceSize not divisible by segmentSize")
	}
	// Check that there are enough segments.
	if uint64(len(segments)) != pieceSize/segmentSize {
		return nil, fmt.Errorf("not enough segments expected %v but was %v",
			pieceSize/segmentSize, len(segments))
	}
	for i := range segments {
		// Check that the caller provided the minimum amount of pieces.
		if len(segments[i]) != rs.MinPieces() {
			return nil, fmt.Errorf("invalid number of pieces given %v %v",
				len(segments), rs.MinPieces())
		}
		// Check that each subshard consists of the minimum amount of segments.
		for j := range segments[i] {
			if uint64(len(segments[i][j])) != segmentSize {
				return nil, errors.New("segments have wrong size")
			}
		}
	}
	// Add segments until segments has the correct length.
	for i := range segments {
		for len(segments[i]) < rs.NumPieces() {
			segments[i] = append(segments[i], make([]byte, segmentSize))
		}
	}
	// Encode the segments.
	for i := range segments {
		if err := rs.enc.Encode(segments[i]); err != nil {
			return nil, err
		}
	}
	// Convert the encoded segments to the output format.
	// TODO this is not in-place yet.
	var pieces [][]byte
	for i := 0; i < rs.NumPieces(); i++ {
		piece := make([]byte, pieceSize)
		for j := uint64(0); j < pieceSize/segmentSize; j++ {
			piece = append(piece, segments[j][i]...)
		}
	}
	return pieces, nil
}

// RecoverSegment accepts encoded pieces and decodes the segment at
// segmentIndex.
func (rs *RSCode) RecoverSegment(pieces [][]byte, segmentIndex int, w io.Writer) error {
	panic("not yet implemented yet")
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
