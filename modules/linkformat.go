package modules

// linkformat.go creates links that can be used to reference specific sector
// data in a siafile. The links are base58 encoded structs prepended with
// 'sia://'

import (
	"bytes"
	"encoding/base64"
	"math"
	"strings"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// rawLinkDataSize is the raw size of the data that gets put into a link.
	rawLinkDataSize = 34

	// encodedLinkDataSize is the size of the LinkData after it has been encoded
	// using base64. This size excludes the 'sia://' prefix.
	encodedLinkDataSize = 46
)

const (
	// SialinkPacketSize indicates the assumed packet size when fetching a
	// sialink. There is an assumption that any fetch of logical data has to be
	// a multiple of this size.
	SialinkPacketSize = 1420

	// FetchMagnitudeGrowthFactor indicates how fast the fast the FetchMagnitude
	// grows.
	FetchMagnitudeGrowthFactor = 1.04
)

// LinkData defines the data that appears in a linkfile. It is a highly
// compressed data structure that encodes into 48 or fewer base64 bytes. The
// goal of LinkData is not to provide a complete set of information, but rather
// to provide enough information to efficiently recover a file.
//
// Because the values are compressed, they are unexpectored and must be accessed
// using helper methods.
type LinkData struct {
	vdp            uint8
	fetchMagnitude uint8
	merkleRoot     crypto.Hash
}

// DataPieces will return the data pieces from the linkdata. At this time, the
// DataPieces should be considered undefined, will always return '1'.
func (ld LinkData) DataPieces() uint8 {
	return 1
}

// FetchSize determines how many logical bytes should be fetched based on the
// magnitude of the fetch request.
func (ld LinkData) FetchSize() uint64 {
	// Sentinal value of 0 indicates that the whole sector needs to be fetched.
	if ld.fetchMagnitude == 0 {
		return SectorSize
	}

	// Calculate the number of bytes that need to be fetched. Take the 1.04 to
	// the power of the magnitude and then multiply that by the packet size.
	// This number is an approximation, but covers the whole range from ~1500
	// bytes to the full 4 MiB, skipping only 4% at a time.
	numPackets := math.Pow(1.04, float64(ld.fetchMagnitude))
	return uint64(numPackets * SialinkPacketSize)
}

// LoadSialink returns the linkdata associated with an input sialink.
func (ld *LinkData) LoadSialink(s Sialink) error {
	return ld.LoadString(string(s))
}

// LoadString converts from a string and loads the result into ld.
func (ld *LinkData) LoadString(s string) error {
	// Trim any 'sia://' that has tagged along.
	noPrefix := strings.TrimPrefix(s, "sia://")
	// Trim any parameters that may exist after an amperstand. Eventually, it
	// will be possible to parse these separately as additional/optional
	// argumetns, for now anything after an amperstand is just ignored.
	splits := strings.SplitN(noPrefix, "&", 2)
	if len(splits) == 0 {
		return errors.New("not a sialnik, no base sialink provided")
	}
	base := []byte(splits[0])

	// Input check, ensure that this is an expected string.
	if len(base) != encodedLinkDataSize {
		return errors.New("not a sialink, sialinks are always 46 bytes")
	}

	// Decode the sialink from base64 into raw. I believe that only
	// 'rawLinkDataSize' bytes are necessary to decode successfully, however the
	// stdlib will panic if you run a decode operation on a slice that is too
	// small, so 4 extra bytes are added to cover any potential situation where
	// a sialink needs extra space to decode. 4 is chosen because that's the
	// size of a base64 word, meaning that there's an entire extra word of
	// cushion. Because we check the size upon receiving the sialink, we will
	// never need more than one extra word.
	raw := make([]byte, rawLinkDataSize+4)
	_, err := base64.RawURLEncoding.Decode(raw, base)
	if err != nil {
		return errors.New("unable to decode input as base64")
	}

	// Load the raw data.
	ld.vdp = raw[0]
	ld.fetchMagnitude = raw[1]
	copy(ld.merkleRoot[:], raw[2:])
	return nil
}

// MerkleRoot returns the merkle root of the LinkData.
func (ld LinkData) MerkleRoot() crypto.Hash {
	return ld.merkleRoot
}

// ParityPieces returns the number of ParityPieces from the linkdata. At this
// time, ParityPieces should be considered undefined, will always return '0'.
func (ld LinkData) ParityPieces() uint8 {
	return 0
}

// SetDataPieces will set the data pieces for the LinkData.
func (ld *LinkData) SetDataPieces(dp uint8) {
	if dp != 1 {
		build.Critical("SetDataPieces can only be 1 right now")
	}
	// Do nothing - value is already '0', which implies a value of '1'.
}

// SetFetchSize will compress the fetch size into a uint8. This is a lossy
// process, however so long as the value is below SectorSize, the result of
// calling 'FetchSize()' on the ld will be greater than or equal to the input
// value and no more than 4% larger than the input value.
func (ld *LinkData) SetFetchSize(size uint64) {
	if size > SectorSize {
		build.Critical("size needs to be less than SectorSize")
	}
	// The number of packets is the size divided by the packet size. One is
	// added at the end because it is necessary to round up.
	packets := math.Ceil(float64(size) / SialinkPacketSize)

	// TODO: This is taking the log base FetchMagnitudeGrowthFactor of the
	// value. Surely there is a faster way.
	val := 1.0
	magnitude := uint8(0)
	for val < packets {
		magnitude++
		val *= 1.04
	}
	ld.fetchMagnitude = magnitude
}

// SetMerkleRoot will set the merkle root of the LinkData. This is the one
// function that doesn't need to be wrapped for safety, however it is wrapped so
// that the field remains consistent with all other fields.
func (ld *LinkData) SetMerkleRoot(mr crypto.Hash) {
	ld.merkleRoot = mr
}

func (ld *LinkData) SetParityPieces(pp uint8) {
	if pp != 0 {
		build.Critical("SetParityPieces can only be 0 right now")
	}
	// Do nothing - value is already '0', which implies a value of '0'.
}

// SetVersion sets the version of the LinkData. Value must be in the range 
// [1, 4].
func (ld *LinkData) SetVersion(version uint8) error {
	// Check that the version is in the valid range for versions.
	if version < 1 || version > 4 {
		return errors.New("version must be in the range [1, 4]")
	}
	// Convert the version to its bitwise value.
	version--

	// Zero out the version bits of the vdp.
	ld.vdp &= 252
	// Set the version bits of the vdp.
	ld.vdp += version
	return nil
}

// Sialink returns the type safe 'sialink' of the link data, which is just a
// typecast string.
func (ld LinkData) Sialink() Sialink {
	return Sialink(ld.String())
}

// String converts LinkData to a string.
func (ld LinkData) String() string {
	// Build the raw string.
	raw := make([]byte, rawLinkDataSize)
	raw[0] = ld.vdp
	raw[1] = ld.fetchMagnitude
	copy(raw[2:], ld.merkleRoot[:])

	// Encode the raw bytes to base64. TWe have to use a a buffer and a base64
	// encoder because the other functions that the stdlib provides will add
	// padding to the end unnecessarily.
	bufBytes := make([]byte, 0, encodedLinkDataSize)
	buf := bytes.NewBuffer(bufBytes)
	encoder := base64.NewEncoder(base64.RawURLEncoding, buf)
	encoder.Write(raw)
	encoder.Close()
	return "sia://" + buf.String()
}

// Version will pull the version out of the vdp and return it. Version is a 2
// bit number, meaning there are 4 possible values. The bitwise values cover the
// range [0, 3], however we want to return a value in the range [1, 4], so we
// increment the bitwise result.
func (ld LinkData) Version() uint8 {
	return (ld.vdp & 3) + 1
}
