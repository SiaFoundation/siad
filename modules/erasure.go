package modules

// erasure.go defines an interface for an erasure coder, as well as an erasure
// type for data that is not erasure coded.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/reedsolomon"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
)

var (
	// RenterDefaultDataPieces is the number of data pieces per erasure-coded
	// chunk used in the renter.
	RenterDefaultDataPieces = build.Select(build.Var{
		Dev:      1,
		Standard: 10,
		Testnet:  10,
		Testing:  1,
	}).(int)

	// RenterDefaultParityPieces is the number of parity pieces per
	// erasure-coded chunk used in the renter.
	RenterDefaultParityPieces = build.Select(build.Var{
		Dev:      1,
		Standard: 20,
		Testnet:  20,
		Testing:  4,
	}).(int)

	// ECReedSolomon is the marshaled type of the reed solomon coder.
	ECReedSolomon = ErasureCoderType{0, 0, 0, 1}

	// ECReedSolomonSubShards64 is the marshaled type of the reed solomon coder
	// for files where every 64 bytes of an encoded piece can be decoded
	// separately.
	ECReedSolomonSubShards64 = ErasureCoderType{0, 0, 0, 2}

	// ECPassthrough defines the erasure coder type for an erasure coder that
	// does nothing.
	ECPassthrough = ErasureCoderType{0, 0, 0, 3}
)

type (
	// ErasureCoderType is an identifier for the individual types of erasure
	// coders.
	ErasureCoderType [4]byte

	// ErasureCoderIdentifier is an identifier that only matches another
	// ErasureCoder's identifier if they both are of the same type and settings.
	ErasureCoderIdentifier string

	// An ErasureCoder is an error-correcting encoder and decoder.
	ErasureCoder interface {
		// NumPieces is the number of pieces returned by Encode.
		NumPieces() int

		// MinPieces is the minimum number of pieces that must be present to
		// recover the original data.
		MinPieces() int

		// Encode splits data into equal-length pieces, with some pieces
		// containing parity data.
		Encode(data []byte) ([][]byte, error)

		// Identifier returns the ErasureCoderIdentifier of the ErasureCoder.
		Identifier() ErasureCoderIdentifier

		// EncodeShards encodes the input data like Encode but accepts an already
		// sharded input.
		EncodeShards(data [][]byte) ([][]byte, error)

		// Reconstruct recovers the full set of encoded shards from the provided
		// pieces, of which at least MinPieces must be non-nil.
		Reconstruct(pieces [][]byte) error

		// Recover recovers the original data from pieces and writes it to w.
		// pieces should be identical to the slice returned by Encode (length
		// and order must be preserved), but with missing elements set to nil. n
		// is the number of bytes to be written to w; this is necessary because
		// pieces may have been padded with zeros during encoding.
		Recover(pieces [][]byte, n uint64, w io.Writer) error

		// SupportsPartialEncoding returns true if partial encoding is
		// supported. The piece segment size will be returned. Otherwise the
		// numerical return value is set to zero.
		SupportsPartialEncoding() (uint64, bool)

		// Type returns the type identifier of the ErasureCoder.
		Type() ErasureCoderType
	}

	// RSCode is a Reed-Solomon encoder/decoder. It implements the
	// ErasureCoder interface.
	RSCode struct {
		enc reedsolomon.Encoder

		numPieces  int
		dataPieces int
	}

	// RSSubCode is a Reed-Solomon encoder/decoder. It implements the
	// ErasureCoder interface in a way that every crypto.SegmentSize bytes of
	// encoded data can be recovered separately.
	RSSubCode struct {
		RSCode
		staticSegmentSize uint64
		staticType        ErasureCoderType
	}

	// PassthroughErasureCoder is a blank type that signifies no erasure coding.
	PassthroughErasureCoder struct{}
)

// NewRSCode creates a new Reed-Solomon encoder/decoder using the supplied
// parameters.
func NewRSCode(nData, nParity int) (ErasureCoder, error) {
	return newRSCode(nData, nParity)
}

// NewRSCodeDefault creates a new Reed-Solomon encoder/decoder using the
// default parameters.
func NewRSCodeDefault() ErasureCoder {
	ec, err := newRSCode(RenterDefaultDataPieces, RenterDefaultParityPieces)
	if err != nil {
		build.Critical("defaults are not accepted")
	}
	return ec
}

// NewRSSubCode creates a new Reed-Solomon encoder/decoder using the supplied
// parameters.
func NewRSSubCode(nData, nParity int, segmentSize uint64) (ErasureCoder, error) {
	rs, err := newRSCode(nData, nParity)
	if err != nil {
		return nil, err
	}
	// Get the correct type from the segmentSize.
	var t ErasureCoderType
	switch segmentSize {
	case 64:
		t = ECReedSolomonSubShards64
	default:
		return nil, errors.New("unsupported segmentSize")
	}
	// Create the encoder.
	return &RSSubCode{
		*rs,
		segmentSize,
		t,
	}, nil
}

// NewRSSubCodeDefault creates a new Reed-Solomon encoder/decoder using the
// default parameters and the default segment size.
func NewRSSubCodeDefault() ErasureCoder {
	ec, err := NewRSSubCode(RenterDefaultDataPieces, RenterDefaultParityPieces, crypto.SegmentSize)
	if err != nil {
		build.Critical("defaults are not accepted")
	}
	return ec
}

// NewPassthroughErasureCoder will return an erasure coder that does not encode
// the data. It uses 1-of-1 redundancy and always returns itself or some subset
// of itself.
func NewPassthroughErasureCoder() ErasureCoder {
	return new(PassthroughErasureCoder)
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
func (rs *RSCode) Identifier() ErasureCoderIdentifier {
	t := rs.Type()
	dataPieces := rs.MinPieces()
	parityPieces := rs.NumPieces() - dataPieces
	id := fmt.Sprintf("%v+%v+%v", binary.BigEndian.Uint32(t[:]), dataPieces, parityPieces)
	return ErasureCoderIdentifier(id)
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

// SupportsPartialEncoding returns false for the basic reed-solomon encoder and
// a size of 0.
func (rs *RSCode) SupportsPartialEncoding() (uint64, bool) {
	return 0, false
}

// Type returns the erasure coders type identifier.
func (rs *RSCode) Type() ErasureCoderType {
	return ECReedSolomon
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

// Encode splits data into equal-length pieces, some containing the original
// data and some containing parity data.
func (rs *RSSubCode) Encode(data []byte) ([][]byte, error) {
	pieces, err := rs.enc.Split(data)
	if err != nil {
		return nil, err
	}
	return rs.EncodeShards(pieces[:rs.MinPieces()])
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
	// pieceSize must be divisible by segmentSize
	if pieceSize%rs.staticSegmentSize != 0 {
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
		s := buf.Next(int(rs.staticSegmentSize) * rs.MinPieces())

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
		segmentOffset += rs.staticSegmentSize
	}
	return pieces, nil
}

// Identifier returns an identifier for an erasure coder which can be used to
// identify erasure coders of the same type, dataPieces and parityPieces.
func (rs *RSSubCode) Identifier() ErasureCoderIdentifier {
	t := rs.Type()
	dataPieces := rs.MinPieces()
	parityPieces := rs.NumPieces() - dataPieces
	id := fmt.Sprintf("%v+%v+%v", binary.BigEndian.Uint32(t[:]), dataPieces, parityPieces)
	return ErasureCoderIdentifier(id)
}

// Reconstruct recovers the full set of encoded shards from the provided
// pieces, of which at least MinPieces must be non-nil.
func (rs *RSSubCode) Reconstruct(pieces [][]byte) error {
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

	// pieceSize must be divisible by segmentSize
	if pieceSize%rs.staticSegmentSize != 0 {
		return errors.New("pieceSize not divisible by segmentSize")
	}

	isNil := make([]bool, len(pieces))
	for i := range pieces {
		isNil[i] = len(pieces[i]) == 0
		pieces[i] = pieces[i][:0]
	}

	// Extract the segment from the pieces.
	segment := make([][]byte, len(pieces))
	for segmentIndex := 0; uint64(segmentIndex) < pieceSize/rs.staticSegmentSize; segmentIndex++ {
		off := uint64(segmentIndex) * rs.staticSegmentSize
		for i, piece := range pieces {
			if isNil[i] {
				segment[i] = piece[off:off]
			} else {
				segment[i] = piece[off:][:rs.staticSegmentSize]
			}
		}
		// Reconstruct the segment.
		if err := rs.RSCode.Reconstruct(segment); err != nil {
			return err
		}
		for i := range pieces {
			pieces[i] = append(pieces[i], segment[i]...)
		}
	}
	return nil
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

	// pieceSize must be divisible by segmentSize
	if pieceSize%rs.staticSegmentSize != 0 {
		return errors.New("pieceSize not divisible by segmentSize")
	}

	// Extract the segment from the pieces.
	decodedSegmentSize := rs.staticSegmentSize * uint64(rs.MinPieces())
	segment := make([][]byte, len(pieces))
	for i := range segment {
		segment[i] = make([]byte, 0, rs.staticSegmentSize)
	}
	for segmentIndex := 0; uint64(segmentIndex) < pieceSize/rs.staticSegmentSize && n > 0; segmentIndex++ {
		off := uint64(segmentIndex) * rs.staticSegmentSize
		for i, piece := range pieces {
			if uint64(len(piece)) >= off+rs.staticSegmentSize {
				segment[i] = append(segment[i][:0], piece[off:off+rs.staticSegmentSize]...)
			} else {
				segment[i] = segment[i][:0]
			}
		}
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

// SupportsPartialEncoding returns true for the custom reed-solomon encoder and
// returns the segment size.
func (rs *RSSubCode) SupportsPartialEncoding() (uint64, bool) {
	return crypto.SegmentSize, true
}

// Type returns the erasure coders type identifier.
func (rs *RSSubCode) Type() ErasureCoderType {
	return rs.staticType
}

// ExtractSegment is a convenience method that extracts the data of the segment
// at segmentIndex from pieces.
func ExtractSegment(pieces [][]byte, segmentIndex int, segmentSize uint64) [][]byte {
	segment := make([][]byte, len(pieces))
	off := uint64(segmentIndex) * segmentSize
	for i, piece := range pieces {
		if uint64(len(piece)) >= off+segmentSize {
			segment[i] = piece[off : off+segmentSize]
		} else {
			segment[i] = nil
		}
	}
	return segment
}

// NumPieces is the number of pieces returned by Encode. For the passthrough
// this is hardcoded to 1.
func (pec *PassthroughErasureCoder) NumPieces() int {
	return 1
}

// MinPieces is the minimum number of pieces that must be present to recover the
// original data. For the passthrough this is hardcoded to 1.
func (pec *PassthroughErasureCoder) MinPieces() int {
	return 1
}

// Encode splits data into equal-length pieces, with some pieces containing
// parity data. For the passthrough this is a no-op.
func (pec *PassthroughErasureCoder) Encode(data []byte) ([][]byte, error) {
	return [][]byte{data}, nil
}

// Identifier returns the ErasureCoderIdentifier of the ErasureCoder.
func (pec *PassthroughErasureCoder) Identifier() ErasureCoderIdentifier {
	return "ECPassthrough"
}

// EncodeShards encodes the input data like Encode but accepts an already
// sharded input. For the passthrough this is a no-op.
func (pec *PassthroughErasureCoder) EncodeShards(pieces [][]byte) ([][]byte, error) {
	return pieces, nil
}

// Reconstruct recovers the full set of encoded shards from the provided pieces,
// of which at least MinPieces must be non-nil. For the passthrough this is a
// no-op.
func (pec *PassthroughErasureCoder) Reconstruct(pieces [][]byte) error {
	return nil
}

// Recover recovers the original data from pieces and writes it to w. pieces
// should be identical to the slice returned by Encode (length and order must be
// preserved), but with missing elements set to nil. n is the number of bytes to
// be written to w; this is necessary because pieces may have been padded with
// zeros during encoding.
func (pec *PassthroughErasureCoder) Recover(pieces [][]byte, n uint64, w io.Writer) error {
	_, err := w.Write(pieces[0][:n])
	return err
}

// SupportsPartialEncoding returns true if partial encoding is supported. The
// piece segment size will be returned. Otherwise the numerical return value is
// set to zero.
func (pec *PassthroughErasureCoder) SupportsPartialEncoding() (uint64, bool) {
	// The actual protocol is in some places restricted to using an atomic
	// request size of crypto.SegmentSize, so that's what we use here.
	//
	// TODO: I'm not sure if the above comment is completely true, may be okay
	// to return a segment size of 1.
	return crypto.SegmentSize, true
}

// Type returns the type identifier of the ErasureCoder.
func (pec *PassthroughErasureCoder) Type() ErasureCoderType {
	return ECPassthrough
}
