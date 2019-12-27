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
type LinkData struct {
	Version      uint8
	MerkleRoot   crypto.Hash
	HeaderSize   uint32
	FileSize     uint64
	DataPieces   uint8
	ParityPieces uint8
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
	raw := make([]byte, 47)
	_, err := base64.RawURLEncoding.Decode(raw, []byte(base))
	if err != nil {
		return errors.New("unable to decode input as base64")
	}

	// Decode the raw bytes into a LinkData.
	ld.Version = raw[0]
	copy(ld.MerkleRoot[:], raw[1:])
	ld.HeaderSize = binary.LittleEndian.Uint32(raw[33:])
	ld.FileSize = binary.LittleEndian.Uint64(raw[37:])
	ld.DataPieces = raw[45]
	ld.ParityPieces = raw[46]

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
	raw := make([]byte, 47)
	raw[0] = ld.Version
	copy(raw[1:], ld.MerkleRoot[:])
	binary.LittleEndian.PutUint32(raw[33:], ld.HeaderSize)
	binary.LittleEndian.PutUint64(raw[37:], ld.FileSize)
	raw[45] = ld.DataPieces
	raw[46] = ld.ParityPieces

	// Encode to base64.
	bufBytes := make([]byte, 0, 72)
	buf := bytes.NewBuffer(bufBytes)
	encoder := base64.NewEncoder(base64.RawURLEncoding, buf)
	encoder.Write(raw)
	encoder.Close()
	return "sia://" + buf.String()
}
