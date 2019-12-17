package renter

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"strings"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/errors"
)

// linkformat.go creates links that can be used to reference specific sector
// data in a siafile. The links are base58 encoded structs prepended with
// 'sia://'

// LinkData defines the data that appears in a linkfile.
type LinkData struct {
	Version      uint8
	MerkleRoot   crypto.Hash
	Filesize     uint64
	DataPieces   uint8
	ParityPieces uint8
}

// String converts LinkData to a string.
func (ld LinkData) String() string {
	raw := make([]byte, 43, 43)
	raw[0] = byte(ld.Version)
	copy(raw[1:], ld.MerkleRoot[:])
	binary.LittleEndian.PutUint64(raw[33:], ld.Filesize)
	raw[41] = byte(ld.DataPieces)
	raw[42] = byte(ld.ParityPieces)

	// Encode to base64.
	bufBytes := make([]byte, 0, 60)
	buf := bytes.NewBuffer(bufBytes)
	encoder := base64.NewEncoder(base64.RawURLEncoding, buf)
	encoder.Write(raw)
	encoder.Close()
	return "sia://" + buf.String()
}

// LoadString converts from a string and loads the result into ld.
func (ld *LinkData) LoadString(s string) error {
	// Trim any 'sia://' that has tagged along.
	base := strings.TrimPrefix(s, "sia://")

	// Use the base64 package to decode the string.
	raw := make([]byte, 43, 43)
	_, err := base64.RawURLEncoding.Decode(raw, []byte(base))
	if err != nil {
		return errors.New("unable to decode input as base64")
	}

	// Decode the raw bytes into a LinkData.
	ld.Version = uint8(raw[0])
	copy(ld.MerkleRoot[:], raw[1:])
	ld.Filesize = binary.LittleEndian.Uint64(raw[33:])
	ld.DataPieces = uint8(raw[41])
	ld.ParityPieces = uint8(raw[42])
	return nil
}
