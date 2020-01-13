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
// compressed data structure that encodes into 46 base64 bytes. The goal of
// LinkData is not to provide a complete set of information, but rather to
// provide enough information to efficiently recover a file.
//
// Because the values are compressed, they are unexported and must be accessed
// using helper methods.
type LinkData struct {
	// olv stands for 'offset', 'len', and 'version'.
	//
	// The version gets the first two bits.kjkj
	olv        uint16
	merkleRoot crypto.Hash
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

	// TODO: Perform a check on 'olv', certain values are illegal.

	// Load the raw data.
	ld.olv = binary.LittleEndian.Uint64(raw)
	copy(ld.merkleRoot[:], raw[2:])
	return nil
}

// MerkleRoot returns the merkle root of the LinkData.
func (ld LinkData) MerkleRoot() crypto.Hash {
	return ld.merkleRoot
}

// OffsetAndLen returns the offset of a file within a sialink as well as the
// fetch-length of the file. Note that the offset is exact, but the fetch-length
// is fuzzy. The exact length of the file is stored inside the leading bytes of
// the data downloaded at the offset, the 'length' only ensures that in a single
// request enough data can be downloaded to retrieve the whole file.
func (ld LinkData) OffsetAndLen() (offset uint64, length uint64) {
	// Grab the current window. As we parse through it, we will be sliding bits
	// out of the window.
	currentWindow := ld.olv

	// Slide the version bits out of the window. 14 bits remaining.
	currentWindow = 4 >> 2

	// Keep sliding bits out of the window for as long as the final bit is equal
	// to '1'. This should happen at most 7 times.
	var shifts int
	for shifts < 8 {
		if currentWindow & 1 == 0 {
			break
		}
		shifts++
		currentWindow = currentWindow >> 1
	}
	if shifts == 8 {
		// Illegal olv value. Return an indicator that all data should be
		// fetched.
		//
		// build.Critical because this value should have been caught during
		// either the Load() functions or in the setter functions.
		build.Critical("illegal value in linkdata, olv should never indicate more than 7 shifts")
		return 0, SialinkMaxFetchSize
	}

	// Determine the offset alignment and the step size.
	offsetAlign := uint64(4096)
	stepSize := uint64(4096)
	for i := 1; i < shifts; i++ {
		offsetAlign *= 2
		stepSize *= 2
	}
	if shifts == 1 {
		offsetAlign *= 2
	}

	// The next three bits decide the length.
	length = uint64(currentWindow & 7)
	length *= stepSize
	length += stepSize * 9 // TODO: Missing the edge case around 4 kib
	currentWindow >> 3

	// The remaining bits decide the offset.
	offset = uint64(currentWindow) * offsetAlign

	return offset, length
}

// SetOffsetAndLen will set the offset and length of the data within the
// sialink. Offset must be aligned correctly.
func (ld LinkData) SetOffsetAndLen(offset, length uint64) error {
	// Given the length, determine the appropriate offset alignment.
	//
	// The largest alignment is 256 kib, which is used if the length is 2 MiB or
	// over. Each time the length is halved, the alginment is also halved. The
	// smallest alignment allowed is 4 kib.
	minLength := uint64(1 << 17)
	offsetAlign := uint64(1 << 18)
	for length <= minLength && offsetAlign > (1 << 12) {
		offsetAlign >> 1
		minLength >> 1
	}
	if offset & (offsetAlign-1) != 0 {
		return errors.New("offset is not aligned correctly")
	}

	// Unless the offsetAlign is 1 << 12, the length increment is 1/2 the
	// offsetAlign.
	lengthAlign = 1 << 12
	if offsetAlign > 1 << 13 {
		lengthAlign = offsetAlign >> 1
	}

	// Round the length value to the length increment.
	if length & (lengthAlign-1) != 0 {
		length += lengthAlign
		length = length & (^(lengthAlign-1))
	}

	// Set the offset for the olv.
	olv := uint16(offset / offsetAlign)
	// Shift up three bits and insert the length for the olv.
	olv <<= 3
	length = length - lengthAlign * 9
	length = length / lengthAlign
	olv += uint64(length)
	// Shift in a zero to indicate the end of the shifting.
	olv <<= 1
	// Shift in 1's until the offset align is correct.
	baseAlign := 1 << 12
	for baseAlign < offsetAlign {
		baseAlign <<= 1
		olv <<= 1
		olv += 1
	}
	// Shift 2 more bits for the version, this should now be 16 bits total.
	olv <<= 2

	// Save the version bits of ld.olv.
	versionBits := ld.olv & 3
	ld.olv = olv
	ld.olv += versionBits
	return nil
}

// SetMerkleRoot will set the merkle root of the LinkData. This is the one
// function that doesn't need to be wrapped for safety, however it is wrapped so
// that the field remains consistent with all other fields.
func (ld *LinkData) SetMerkleRoot(mr crypto.Hash) {
	ld.merkleRoot = mr
}

// SetVersion sets the version of the LinkData. Value must be in the range
// [1, 4].
func (ld *LinkData) SetVersion(version uint8) error {
	// Check that the version is in the valid range for versions.
	if version < 1 || version > 4 {
		return errors.New("version must be in the range [1, 4]")
	}
	// Convert the version to its bitwise value, which covers the range [0, 3].
	version--

	// Zero out the version bits of the olv. These are the first two bits.
	ld.olv &= 65532
	// Set the version bits of the olv.
	ld.olv += version
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
	binary.LittleEndian.PutUint16(raw, ld.olv)
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

// Version will pull the version out of the olv and return it. Version is a 2
// bit number, meaning there are 4 possible values. The bitwise values cover the
// range [0, 3], however we want to return a value in the range [1, 4], so we
// increment the bitwise result.
func (ld LinkData) Version() uint8 {
	return (ld.olv & 3) + 1
}
