package siafile

import (
	"bytes"
	"fmt"
	"io"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// RSSubCode is a Reed-Solomon encoder/decoder. It implements the
// modules.ErasureCoder interface in a way that every crypto.SegmentSize bytes
// of encoded data can be recovered separately.
type RSSubCode struct {
	RSCode
}

// Encode splits data into equal-length pieces, some containing the original
// data and some containing parity data.
func (rs *RSSubCode) Encode(data []byte) ([][]byte, error) {
	pieces, err := rs.enc.Split(data)
	if err != nil {
		return nil, err
	}
	return rs.EncodeShards(pieces)
}

// EncodeShards encodes data in a way that every segmentSize bytes of the
// encoded data can be decoded independently.
func (rs *RSSubCode) EncodeShards(pieces [][]byte) ([][]byte, error) {
	// Check that there are enough pieces.
	if len(pieces) != rs.MinPieces() {
		return nil, fmt.Errorf("not enough segments expected %v but was %v",
			rs.MinPieces(), len(pieces))
	}
	// Since all the pieces should have the same length, get the pieceSize from
	// the first one.
	pieceSize := uint64(len(pieces[0]))
	segmentSize := uint64(crypto.SegmentSize)
	// pieceSize must be divisible by segmentSize
	if pieceSize%segmentSize != 0 {
		return nil, errors.New("pieceSize not divisible by segmentSize")
	}
	// Each piece should have pieceSize bytes.
	for _, piece := range pieces {
		if uint64(len(piece)) != pieceSize {
			return nil, fmt.Errorf("pieces don't have right size expected %v but was %v",
				pieceSize, len(piece))
		}
	}
	// Flatten the pieces into a byte slice.
	data := make([]byte, uint64(len(pieces))*pieceSize)
	for i, piece := range pieces {
		copy(data[uint64(i)*pieceSize:], piece)
		pieces[i] = pieces[i][:0]
	}
	// Add parity shards to pieces.
	parityShards := make([][]byte, rs.NumPieces()-len(pieces))
	pieces = append(pieces, parityShards...)
	// Encode the pieces.
	segmentOffset := uint64(0)
	for buf := bytes.NewBuffer(data); buf.Len() > 0; {
		// Get the next segments to encode.
		s := buf.Next(int(segmentSize) * rs.MinPieces())

		// Create a copy of it.
		segments := make([]byte, len(s))
		copy(segments, s)

		// Encode the segment
		encodedSegments, err := rs.RSCode.Encode(segments)
		if err != nil {
			return nil, err
		}

		// Write the encoded segments back to pieces.
		for i, segment := range encodedSegments {
			pieces[i] = append(pieces[i], segment...)
		}
		segmentOffset += segmentSize
	}
	return pieces, nil
}

// Recover accepts encoded pieces and decodes the segment at
// segmentIndex. The size of the decoded data is segmentSize * dataPieces.
func (rs *RSSubCode) Recover(pieces [][]byte, n uint64, w io.Writer) error {
	// Check the length of pieces.
	if len(pieces) != rs.NumPieces() {
		return fmt.Errorf("expected pieces to have len %v but was %v",
			rs.NumPieces(), len(pieces))
	}
	// Since all the pieces should have the same length, get the pieceSize from
	// the first piece that was set.
	var pieceSize uint64
	for _, piece := range pieces {
		if uint64(len(piece)) > pieceSize {
			pieceSize = uint64(len(piece))
			break
		}
	}
	segmentSize := uint64(crypto.SegmentSize)

	// pieceSize must be divisible by segmentSize
	if pieceSize%segmentSize != 0 {
		return errors.New("pieceSize not divisible by segmentSize")
	}

	// Extract the segment from the pieces.
	decodedSegmentSize := segmentSize * uint64(rs.MinPieces())
	for segmentIndex := 0; uint64(segmentIndex) < pieceSize/segmentSize && n > 0; segmentIndex++ {
		segment := ExtractSegment(pieces, segmentIndex)
		// Reconstruct the segment.
		if n < decodedSegmentSize {
			decodedSegmentSize = n
		}
		if err := rs.RSCode.Recover(segment, decodedSegmentSize, w); err != nil {
			return err
		}
		n -= decodedSegmentSize
	}
	return nil
}

// Type returns the erasure coders type identifier.
func (rs *RSSubCode) Type() modules.ErasureCoderType {
	return ecReedSolomonSubShards
}

// ExtractSegment is a convenience method that extracts the data of the segment
// at segmentIndex from pieces.
func ExtractSegment(pieces [][]byte, segmentIndex int) [][]byte {
	segment := make([][]byte, len(pieces))
	off := segmentIndex * crypto.SegmentSize
	segmentSize := crypto.SegmentSize
	for i, piece := range pieces {
		if len(piece) >= off+segmentSize {
			segment[i] = piece[off : off+segmentSize]
		} else {
			segment[i] = nil
		}
	}
	return segment
}

// NewRSSubCode creates a new Reed-Solomon encoder/decoder using the supplied
// parameters.
func NewRSSubCode(nData, nParity int) (modules.ErasureCoder, error) {
	rs, err := newRSCode(nData, nParity)
	if err != nil {
		return nil, err
	}
	return &RSSubCode{
		*rs,
	}, nil
}
