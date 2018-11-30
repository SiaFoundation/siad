package siafile

import (
	"bytes"
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
	if len(pieces) < rs.MinPieces() {
		return nil, fmt.Errorf("invalid number of pieces given %v < %v", len(pieces), rs.MinPieces())
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
func EncodeSubShards(rs modules.ErasureCoder, pieces [][]byte, pieceSize, segmentSize uint64) ([][]byte, error) {
	// pieceSize must be divisible by segmentSize
	if pieceSize%segmentSize != 0 {
		return nil, errors.New("pieceSize not divisible by segmentSize")
	}
	// Check that there are enough pieces.
	if len(pieces) != rs.MinPieces() {
		return nil, fmt.Errorf("not enough segments expected %v but was %v",
			rs.MinPieces(), len(pieces))
	}
	// Each piece should've pieceSize bytes.
	for _, piece := range pieces {
		if uint64(len(piece)) != pieceSize {
			return nil, fmt.Errorf("pieces don't have right size expected %v but was %v",
				pieceSize, len(piece))
		}
	}
	// Convert pieces.
	tmpPieces := make([][]byte, len(pieces))
	i := 0
	for _, piece := range pieces {
		for buf := bytes.NewBuffer(piece); buf.Len() > 0; {
			segment := buf.Next(int(segmentSize))
			pieceIndex := i % len(pieces)
			segmentIndex := i / len(pieces)
			if tmpPieces[pieceIndex] == nil {
				tmpPieces[pieceIndex] = make([]byte, pieceSize)
			}
			copy(tmpPieces[pieceIndex][uint64(segmentIndex)*segmentSize:][:segmentSize], segment)
			i++
		}
	}
	pieces = tmpPieces
	// Add the parity shards to pieces.
	for len(pieces) < rs.NumPieces() {
		pieces = append(pieces, make([]byte, pieceSize))
	}
	// Convert the pieces to segments.
	segments := make([][][]byte, pieceSize/segmentSize)
	for pieceIndex, piece := range pieces {
		for segmentIndex := uint64(0); segmentIndex < pieceSize/segmentSize; segmentIndex++ {
			// Allocate space for segments as needed.
			if segments[segmentIndex] == nil {
				segments[segmentIndex] = make([][]byte, rs.NumPieces())
			}
			segment := piece[segmentIndex*segmentSize:][:segmentSize]
			segments[segmentIndex][pieceIndex] = segment
		}
	}
	// Encode the segments.
	for i := range segments {
		encodedSegment, err := rs.EncodeShards(segments[i], pieceSize)
		if err != nil {
			return nil, err
		}
		segments[i] = encodedSegment
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

// RecoverSegment accepts encoded pieces and decodes the segment at
// segmentIndex. The size of the decoded data is segmentSize * dataPieces.
func RecoverSegment(rs modules.ErasureCoder, pieces [][]byte, segmentIndex int, pieceSize, segmentSize uint64, w io.Writer) error {
	// pieceSize must be divisible by segmentSize
	if pieceSize%segmentSize != 0 {
		return errors.New("pieceSize not divisible by segmentSize")
	}
	// Check the length of pieces.
	if len(pieces) != rs.NumPieces() {
		return fmt.Errorf("expected pieces to have len %v but was %v",
			rs.NumPieces(), len(pieces))
	}
	// Extract the segment from the pieces.
	segment := make([][]byte, uint64(rs.NumPieces()))
	for i, piece := range pieces {
		off := uint64(segmentIndex) * segmentSize
		if uint64(len(piece)) > off {
			segment[i] = piece[off : off+segmentSize]
		} else {
			segment[i] = nil
		}
	}
	// Reconstruct the segment.
	return rs.Recover(segment, segmentSize*uint64(rs.MinPieces()), w)
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
