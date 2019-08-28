package siafile

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/klauspost/reedsolomon"

	"gitlab.com/NebulousLabs/Sia/modules"
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
	return rs.EncodeShards(pieces)
}

// EncodeShards creates the parity shards for an already sharded input.
func (rs *RSCode) EncodeShards(pieces [][]byte) ([][]byte, error) {
	// Check that the caller provided the minimum amount of pieces.
	if len(pieces) < rs.MinPieces() {
		return nil, fmt.Errorf("invalid number of pieces given %v < %v", len(pieces), rs.MinPieces())
	}
	// Since all the pieces should have the same length, get the pieceSize from
	// the first one.
	pieceSize := len(pieces[0])
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

// Identifier returns an identifier for an erasure coder which can be used to
// identify erasure coders of the same type, dataPieces and parityPieces.
func (rs *RSCode) Identifier() modules.ErasureCoderIdentifier {
	t := rs.Type()
	dataPieces := rs.MinPieces()
	parityPieces := rs.NumPieces() - dataPieces
	id := fmt.Sprintf("%v+%v+%v", binary.BigEndian.Uint32(t[:]), dataPieces, parityPieces)
	return modules.ErasureCoderIdentifier(id)
}

// Reconstruct recovers the full set of encoded shards from the provided pieces,
// of which at least MinPieces must be non-nil.
func (rs *RSCode) Reconstruct(pieces [][]byte) error {
	return rs.enc.Reconstruct(pieces)
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

// SupportsPartialEncoding returns false for the basic reed-solomon encoder.
func (rs *RSCode) SupportsPartialEncoding() bool {
	return false
}

// Type returns the erasure coders type identifier.
func (rs *RSCode) Type() modules.ErasureCoderType {
	return ecReedSolomon
}

// NewRSCode creates a new Reed-Solomon encoder/decoder using the supplied
// parameters.
func NewRSCode(nData, nParity int) (modules.ErasureCoder, error) {
	return newRSCode(nData, nParity)
}

// newRSCode creates a new Reed-Solomon encoder/decoder using the supplied
// parameters.
func newRSCode(nData, nParity int) (*RSCode, error) {
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
