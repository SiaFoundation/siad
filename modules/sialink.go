package modules

// linkformat.go creates links that can be used to reference specific sector
// data in a siafile. The links are base58 encoded structs prepended with
// 'sia://'

import (
	"bytes"
	"encoding/base64"
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
	// SialinkMaxFetchSize defines the maximum fetch size that is supported by
	// the sialink format. This is intentionally the same number as
	// modules.SectorSize on the release build. We could not use
	// modules.SectorSize directly because during testing that value is too
	// small to properly test the link format.
	SialinkMaxFetchSize = 1 << 22
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
	// If the fetch magnitude is maxed out, fetch everything.
	if ld.fetchMagnitude == 255 {
		return SialinkMaxFetchSize
	}
	// Increment the magnitude so the '0' value is fetching data.
	fm := uint64(ld.fetchMagnitude + 1)
	return SialinkMaxFetchSize / 256 * fm
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
// process that will round the value up to the nearest multiple of 16,384
// (2^14). Storing only a uint8 allows for shorter sialinks and better
// compatibility with the IPFS standard of 46 byte links.
//
// If the input is 2^22 or larger, a value of '255' will be set, indicating that
// the full initial root should be downloaded.
func (ld *LinkData) SetFetchSize(size uint64) {
	if size >= SialinkMaxFetchSize {
		ld.fetchMagnitude = 255
	}
	// Subtract one from the size so the operation to round-up will not round
	// sizes that divide perfectly evenly. The increment to round up is
	// performed in the FetchSize() function so that the '0' value of
	// ld.fetchMagnitude indicatese that 16,384 bytes should be fetched.
	//
	// Check for underflow.
	if size != 0 {
		size--
	}
	ld.fetchMagnitude = uint8(size / (SialinkMaxFetchSize / 256))
}

// SetMerkleRoot will set the merkle root of the LinkData. This is the one
// function that doesn't need to be wrapped for safety, however it is wrapped so
// that the field remains consistent with all other fields.
func (ld *LinkData) SetMerkleRoot(mr crypto.Hash) {
	ld.merkleRoot = mr
}

// SetParityPieces will set the parity pieces of the LinkData.
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
