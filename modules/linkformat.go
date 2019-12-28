package modules

// linkformat.go creates links that can be used to reference specific sector
// data in a siafile. The links are base58 encoded structs prepended with
// 'sia://'

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"strings"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/errors"
)

// LinkData defines the data that appears in a linkfile. The DataPieces and
// ParityPieces specify the intra-sector erasure coding parameters, not the
// linkfile erasure coding parameters.
//
// The maximum value for Version, DataPieces, and ParityPieces is 15, and the
// maximum value for HeaderSize is 2^51. This is because these values get
// bitpacked together to make the URL shorter.
type LinkData struct {
	MerkleRoot   crypto.Hash
	Version      uint64
	DataPieces   uint64
	ParityPieces uint64
	HeaderSize   uint64
	FileSize     uint64
}

// LoadSialink returns the linkdata associated with an input sialink.
func (ld *LinkData) LoadSialink(s Sialink) error {
	return ld.LoadString(string(s))
}

// LoadString converts from a string and loads the result into ld.
func (ld *LinkData) LoadString(s string) error {
	// Trim any 'sia://' that has tagged along.
	base := strings.TrimPrefix(s, "sia://")

	// Use the base64 package to decode the string.
	raw := make([]byte, 52)
	_, err := base64.RawURLEncoding.Decode(raw, []byte(base))
	if err != nil {
		return errors.New("unable to decode input as base64")
	}

	// Decode the raw bytes into a LinkData.
	copy(ld.MerkleRoot[:], raw)
	reader := bytes.NewReader(raw[32:])
	headerSize, err := binary.ReadUvarint(reader)
	if err != nil {
		return errors.AddContext(err, "unable to read sialink header size")
	}
	fileSize, err := binary.ReadUvarint(reader)
	if err != nil {
		return errors.AddContext(err, "unable to read sialink file size")
	}
	ld.FileSize = fileSize

	// Decode the bitpacked fields.
	ld.ParityPieces = headerSize
	ld.ParityPieces <<= 60
	ld.ParityPieces >>= 60
	headerSize >>= 4
	ld.DataPieces = headerSize
	ld.DataPieces <<= 60
	ld.DataPieces >>= 60
	headerSize >>= 4
	ld.Version = headerSize
	ld.Version <<= 60
	ld.Version >>= 60
	headerSize >>= 4
	ld.HeaderSize = headerSize

	// Do some sanity checks on the version, the data pieces, and the parity
	// pieces. Having these checks in place ensures that data was not lost at
	// the front or end of the sialink - the first byte is not allowed to be
	// zero, and the final two bytes are also not allowed to be zero. If for
	// some reason those were omitted, these errors should catch that the
	// sialink has not been copied correctly.
	if ld.Version == 0 {
		return errors.New("version of sialink is not allowed to be zero")
	}
	if ld.DataPieces == 0 {
		return errors.New("data pieces on sialink should not be set to zero")
	}
	return nil
}

// Sialink returns the type safe 'sialink' of the link data, which is just a
// typecast string.
func (ld LinkData) Sialink() Sialink {
	return Sialink(ld.String())
}

// String converts LinkData to a string.
func (ld LinkData) String() string {
	// Treat ld.Version, ld.HeaderSize, and ld.Parity size all as 4 bit
	// integers. That's 12 bits total. Bitpack those 12 bits into ld.HeaderSize
	// by ensuring that ld.HeaderSize fits into 52 bits, and then shifiting it
	// up a few bits to make room for the bitpacked values.
	if ld.DataPieces > 15 {
		panic("DataPieces can only be 4 bits")
	}
	if ld.ParityPieces > 15 {
		panic("ParityPieces can only be 4 bits")
	}
	if ld.Version > 15 {
		panic("Version can only be 4 bits")
	}
	if ld.HeaderSize > uint64(1<<51) {
		panic("HeaderSize can only be 52 bits")
	}
	ld.HeaderSize *= 16
	ld.HeaderSize += ld.Version
	ld.HeaderSize *= 16
	ld.HeaderSize += ld.DataPieces
	ld.HeaderSize *= 16
	ld.HeaderSize += ld.ParityPieces

	// Write out the raw bytes. Max size is 50 bytes - 32 for the Merkle root,
	// 10 for the first varint, 10 for the second varint.
	raw := make([]byte, 52)
	copy(raw, ld.MerkleRoot[:])
	size1 := binary.PutUvarint(raw[32:], ld.HeaderSize)
	size2 := binary.PutUvarint(raw[32+size1:], ld.FileSize)

	// Encode to base64. The maximum size of 52 bytes encoded to base64 is 72
	// bytes.
	bufBytes := make([]byte, 0, 70)
	buf := bytes.NewBuffer(bufBytes)
	encoder := base64.NewEncoder(base64.RawURLEncoding, buf)
	encoder.Write(raw[:32+size1+size2])
	encoder.Close()
	return "sia://" + buf.String()
}
