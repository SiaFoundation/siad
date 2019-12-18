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
	// PayloadSize has a unique definition. It is the size of the actual file,
	// plus the size of the metadata that is stored in the first sector, plus
	// the size of the fanout data that is stored in the first sector. The full
	// payload information is needed to understand how much data should be
	// downloaded from the first sector for files that exist entirely within the
	// first sector.
	//
	// For files that exist across multiple sectors (meaning they use a fanout),
	// extra data will be available to indicate the fanout size, so that is not
	// include in the PayloadSize. The full size of the actual file data as well
	// as all fanout information can be learned after fetching the first sector.
	Version      uint8
	MerkleRoot   crypto.Hash
	PayloadSize  uint64
	DataPieces   uint8
	ParityPieces uint8
}

// String converts LinkData to a string.
func (ld LinkData) String() string {
	raw := make([]byte, 43, 43)
	raw[0] = byte(ld.Version)
	copy(raw[1:], ld.MerkleRoot[:])
	binary.LittleEndian.PutUint64(raw[33:], ld.PayloadSize)
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
	ld.PayloadSize = binary.LittleEndian.Uint64(raw[33:])
	ld.DataPieces = uint8(raw[41])
	ld.ParityPieces = uint8(raw[42])
	return nil
}
