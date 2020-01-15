package modules

// sialink.go creates links that can be used to reference specific sector data
// in a siafile. The links are base64 encoded structs prepended with 'sia://'

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"math/bits"
	"strings"

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

// Sialink defines a link that can be used to fetch data from the Sia network.
// Context clues provided by the blockchain combined with the information in the
// Sialink is all that is needed to uniquely identify and retrieve a file from
// the Sia network.
type Sialink string

// The LinkData contains all of the information that can be encoded into a
// sialink. This information consists of a 32 byte MerkleRoot and a 2 byte
// bitfield.
//
// The first two bits of the bitfield (values 1 and 2 in decimal) determine the
// version of the sialink. The sialink version determines how the remaining bits
// are used. Not all values of the bitfield are legal.
type LinkData struct {
	bitfield   uint16
	merkleRoot crypto.Hash
}

// NewLinkDataV1 will return a v1 LinkData object with the version set to 1 and
// the remaining fields set appropriately. Note that the offset needs to be
// aligned correctly. Check OffsetAndFetchSize for a full list of rules on legal
// offsets - the value of a legal offset depends on the provided length.
//
// The input length will automatically be converted to the nearest fetch size.
func NewLinkDataV1(merkleRoot crypto.Hash, offset, length uint64) (LinkData, error) {
	var ld LinkData
	err := ld.setOffsetAndFetchSize(offset, length)
	if err != nil {
		return LinkData{}, errors.AddContext(err, "Invalid LinkData")
	}
	ld.merkleRoot = merkleRoot
	return ld, nil
}

// validateLinkDataV1Bitfield checks that the
func validateLinkDataV1Bitfield(bitfield uint16) error {
	// Ensure that the version is set to v1.
	version := (bitfield & 3) + 1
	if version != 1 {
		return errors.New("bitfield is not set to v1")
	}

	// The v1 bitfield has a requirement that there is at least one '0' in the
	// bitrange 3-10. Each consecutive '1' bit starting from the 3rd index
	// indicates that the next version of the data structure should be used.
	// After 8 consecutive 1's, there are no more versions of the data structure
	// available, and the bitfield is invalid.
	if bitfield>>2&255 == 255 {
		return errors.New("sialink is not valid, length and offset are illegal")
	}

	// TODO: Check for any of the invalid mode states. Nontrivial, easiest thing
	// might actually be to just parse it like normal and see if the parser ends
	// up with an illegal offset+len total size.
	return nil
}

// LoadSialink returns the linkdata associated with an input sialink.
func (ld *LinkData) LoadSialink(s Sialink) error {
	return ld.LoadString(string(s))
}

// LoadString converts from a string and loads the result into ld.
func (ld *LinkData) LoadString(s string) error {
	// Trim any 'sia://' that has tagged along.
	noPrefix := strings.TrimPrefix(s, "sia://")
	// Trim any parameters that may exist after an ampersand. Eventually, it
	// will be possible to parse these separately as additional/optional
	// arguments, for now anything after an ampersand is just ignored.
	splits := strings.SplitN(noPrefix, "&", 2)
	// No need to check if there is an element returned by strings.SplitN, so
	// long as the second arg is not-nil (in this case, '&'), SplitN cannot
	// return an empty slice.
	base := []byte(splits[0])
	// Input check, ensure that this string is the expected size.
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

	// Load and check the bitfield. The bitfield is checked before modifying the
	// LinkData so that the LinkData remains unchanged if there is any error
	// parsing the string.
	bitfield := binary.LittleEndian.Uint16(raw)
	err = validateLinkDataV1Bitfield(bitfield)
	if err != nil {
		return errors.AddContext(err, "sialink failed verification")
	}

	// Load the raw data.
	ld.bitfield = bitfield
	copy(ld.merkleRoot[:], raw[2:])
	return nil
}

// MerkleRoot returns the merkle root of the LinkData.
func (ld LinkData) MerkleRoot() crypto.Hash {
	return ld.merkleRoot
}

// OffsetAndFetchSize returns the offset and fetch length of a file that sits
// within a sialink sector. All sialinks point to one sector of data. If the
// file is large enough that more data is necessary, a "fanout" is used to point
// to more sectors.
//
// NOTE: To fully understand the bitfield of the v1 Sialink, it is recommended
// that the following documentation is read alongside the code.
//
// Sectors are 4 MiB large. To enable the support of efficiently storing and
// downloading smaller files, the sialink allows an offset and a length to be
// specified for a file, which means many files can be stored within a single
// sector root, and each file can get a unique 46 byte sialink.
//
// IPFS set an industry standard of 46 bytes in base64, which we have chosen to
// adhere to. That only gives us 34 bytes to work with for storing extra
// information such as the version, offset, and length of a file. The tight data
// constraints resulted in this compact format.
//
// Sialinks are given 2 bits for a version. These bits are always the first 2
// bits of the bitfield, which correspond to the values '1' and '2' when the
// bitfield is interpreted as a uint16. The version must be set to 1 to retrieve
// an offset and a length.
//
// That leaves 14 bits to determine the actual offset and length of the file.
// The first 8 of those 14 bits are conditional bits, operating somewhat like
// varints. There are 8 total "modes" that can be triggered by these 8 bits.
// The first mode is triggered if the first of the 8 bits is a "0". That mode
// indicates that the 13 remaining bits should be used to compute the offset and
// fetch size using mode 1. If the first of the 8 bits is a "1", it means check
// the next bit. If that next bit is a "0", the second mode is triggered,
// meaning that the remaining 12 bits should be used to compute the offset and
// fetch size using mode 2.
//
// Out of the 8 modes total, each mode has 1 fewer bit than the previous mode
// for computing the offset and fetch size. The first mode has 13 bits total,
// and the final mode has 6 bits total. The first three of these bits always
// indicates the fetch size. More on that later.
//
// The modes themselves are fairly simple. The first mode indicates that the
// file is stored on an offset that is aligned to 4096 (1 << 12) bytes. With
// that alignment, there are 1024 possible offsets for the file to start at
// within a 4 MiB sector. That takes 10 bits to represent with perfect
// precision, and is conveniently the number of remaining bits to determine the
// offset after the fetch size has been parsed.
//
// The second mode indicates that the file is stored on an offset that is
// aligned to 8192 (1 << 13) bytes, which means there are 512 possible offsets.
// Because a bit was consumed to switch modes, only 9 bits are available to
// indicate what the offset is. But as there are only 512 possible offsets, only
// 9 bits are needed.
//
// This continues until the final mode, which indicates that the file is stored
// on an offset that is alinged to 512 kib (1 << 19). This is where it stops,
// larger offsets are unnecessary. Having 8 consecutive 1's in a v1 Sialink is
// invalid, which means means there are 64 total unused states (all states where
// the first 8 of 14 non-version bits are set to '1').
//
// The fetch size is an upper bound that says 'the file is no more than this
// many bytes', and tells the client to download that many bytes to get the
// whole file. The actual length of the file is in the metadata that gets
// downloaded along with the file.
//
// For every mode, there are 8 possible fetch sizes. For the first mode, the
// first possible fetch size is 4 kib, and each additional possible fetch size
// is another 4 kib. That means files in the first mode can be placed on any
// 4096 byte aligned offset within the Merkle root and can be up to 32 kib
// large.
//
// For the second mode, the lengths also increase by 4 kib at a time, starting
// where the first mode left off. The smallest fetch size that a file in the
// second mode can have is 36 kib, and the largest fetch size that a file in the
// second mode can have is 64 kib.
//
// Each mode after that, the increment of the fetch size doubles. So the third
// mode starts at a fetch size of 72 kib, and goes up to a fetch size of 128
// kib. And the fourth mode starts at a fetch size of 144 kib, and goes up to a
// fetch size of 256 kib. The eighth and final mode extends up to a fetch size
// of 4 MiB, which is the full size of the sector.
//
// A full table of fetch sizes is presented here:
//
//	   4,    8,   12,   16,   20,   24,   28,   32,
//	  36,   40,   44,   48,   52,   56,   60,   64,
//	  72,   80,   88,   96,  104,  112,  120,  128,
//	 144,  160,  176,  192,  208,  224,  240,  256,
//	 288,  320,  352,  384,  416,  448,  480,  512,
//	 576,  640,  704,  768,  832,  896,  960, 1024,
//	1152, 1280, 1408, 1536, 1664, 1792, 1920, 2048,
//	2304, 2560, 2816, 3072, 3328, 3584, 3840, 4096,
//
// Certain combinations of offset + fetch size are illegal. Specifically, it is
// illegal to indicate a fetch size that goes beyond the boundary of the file.
// Every single mode therefore has 28 illegal states (7+6+...+1), for a total of
// 224 illegal states. Combined with the 64 illegal states created by the mode
// bits, there are a total of 288 illegal states for v1 of the Sialink.
//
// It's possible that these illegal states get repurposed in the future, giving
// a little bit of extra functionality to v1 Sialinks. No plans at this time,
// more likely we'd just switch to v2, which has a full 14 bits available.
//
// NOTE: If there is an error, OffsetAndLen will return a signal to download the
// entire sector. This means that any code which is ignoring the error will
// still have mostly sane behavior.
func (ld LinkData) OffsetAndFetchSize() (offset uint64, fetchSize uint64, err error) {
	// Validate the bitfield.
	err = validateLinkDataV1Bitfield(ld.bitfield)
	if err != nil {
		return 0, SialinkMaxFetchSize, errors.AddContext(err, "invalid bitfield, cannot parse offset and len")
	}

	// Grab the current window. As we parse through it, we will be sliding bits
	// out of the window.
	bitfield := ld.bitfield
	if bitfield&3 != 0 {
		return 0, SialinkMaxFetchSize, errors.New("invalid bitfield, version is not set to v1")
	}
	// Shift out the version bits.
	bitfield >>= 2

	// Determine how many mode bits are set.
	modeBits := uint16(bits.TrailingZeros16(^bitfield))
	// A number of 8 or larger is illegal.
	if modeBits > 7 {
		return 0, SialinkMaxFetchSize, errors.New("bitfield has invalid mode bits")
	}
	// Shift the mode bits out. The mode bits is the number of trailing '1's
	// plus an additional '0' which is necessary to signal the end of the mode
	// bits.
	bitfield >>= modeBits + 1

	// Determine the offset alignment and the step size. The offset alignment
	// starts at 4kb and doubles once per modeBit.
	offsetAlign := uint64(4096)
	fetchSizeAlign := uint64(4096)
	offsetAlign <<= modeBits
	if modeBits > 0 {
		fetchSizeAlign <<= modeBits - 1
	}

	// The next three bits decide the fetch size.
	fetchSize = uint64(bitfield & 7)
	fetchSize++ // semantic upstep, cover the range [1, 8] instead of [0, 8).
	fetchSize *= fetchSizeAlign
	if modeBits > 0 {
		fetchSize += fetchSizeAlign << 3
	}
	bitfield >>= 3

	// The remaining bits decide the offset.
	offset = uint64(bitfield) * offsetAlign
	if offset+fetchSize > SialinkMaxFetchSize {
		return 0, SialinkMaxFetchSize, errors.New("invalid bitfield, fetching beyond the limits of the sector")
	}

	return offset, fetchSize, nil
}

// Sialink returns the type safe 'sialink' of the link data, which is just a
// typecast string.
func (ld LinkData) Sialink() (Sialink, error) {
	sl, err := ld.String()
	return Sialink(sl), err
}

// String converts LinkData to a string.
func (ld LinkData) String() (string, error) {
	// Check for illegal values in the bitfield.
	err := validateLinkDataV1Bitfield(ld.bitfield)
	if err != nil {
		return "", errors.AddContext(err, "cannot marshal invalid sialink")
	}

	// Build the raw string.
	raw := make([]byte, rawLinkDataSize)
	binary.LittleEndian.PutUint16(raw, ld.bitfield)
	copy(raw[2:], ld.merkleRoot[:])

	// Encode the raw bytes to base64. TWe have to use a a buffer and a base64
	// encoder because the other functions that the stdlib provides will add
	// padding to the end unnecessarily.
	bufBytes := make([]byte, 0, encodedLinkDataSize)
	buf := bytes.NewBuffer(bufBytes)
	encoder := base64.NewEncoder(base64.RawURLEncoding, buf)
	encoder.Write(raw)
	encoder.Close()
	return "sia://" + buf.String(), nil
}

// Version will pull the version out of the bitfield and return it. The version
// is a 2 bit number, meaning there are 4 possible values. The bitwise values
// cover the range [0, 3], however we want to return a value in the range
// [1, 4], so we increment the bitwise result.
func (ld LinkData) Version() uint16 {
	return (ld.bitfield & 3) + 1
}

// setOffsetAndFetchSize will set the offset and length of the data within the
// sialink. Offset must be aligned correctly. setOffsetAndLen implies that the
// version is 1, so the version will also be set to 1.
func (ld *LinkData) setOffsetAndFetchSize(offset, fetchSize uint64) error {
	if offset+fetchSize > SialinkMaxFetchSize {
		return errors.New("offset plus length cannot exceed the size of one sector - 4 MiB")
	}

	// Given the fetch size, determine the appropriate offset alignment.
	//
	// The largest offset alignment is 512 kib, which is used if the fetch size
	// of 2 MiB or over. Each time the fetch size is halved, the offset
	// alignment is also halved. The smallest offset alignment is 4 kib.
	minFetchSize := uint64(1 << 21)
	offsetAlign := uint64(1 << 19)
	for fetchSize <= minFetchSize && offsetAlign > (1<<12) {
		offsetAlign >>= 1
		minFetchSize >>= 1
	}
	if offset&(offsetAlign-1) != 0 {
		return errors.New("offset is not aligned correctly")
	}
	// The bitwise representation of the offset is the actual offset divided by
	// the offset alignment.
	bitwiseOffset := uint16(offset / offsetAlign)

	// Unless the offsetAlign is 1 << 12, the fetch size alignment is 1/2 the
	// offsetAlign. If the offsetAlign is 1 << 12, the fetch size alignment is
	// also 1 << 12.
	fetchSizeAlign := uint64(1 << 12)
	if offsetAlign > 1<<13 {
		fetchSizeAlign = offsetAlign >> 1
	}
	// If the the mode is anything besides the first mode, there is a length
	// shift by 8 times the length size. We know that the mode is not the first
	// mode if the offsetAlign is not 1 << 12
	if offsetAlign > 1<<12 {
		fetchSize = fetchSize - fetchSizeAlign*8
	}
	// Round the fetch size to the fetchSizeAlign. Because the fetch size is
	// semantically shifted from the range [0, 8) to [1, 8], we round down. If
	// the fetch size is already evenly divisible by the , it's not going to be
	// rounded down so it needs to be decremented manually.
	if fetchSize != 0 && fetchSize == (fetchSize/fetchSizeAlign)*fetchSizeAlign {
		fetchSize--
	}
	fetchSize = fetchSize & (^(fetchSizeAlign - 1))
	bitwiseFetchSize := uint16(fetchSize / fetchSizeAlign)

	// Add the offset to the bitfield.
	bitfield := bitwiseOffset
	// Shift the bitfield up to add the fetch size.
	bitfield <<= 3
	bitfield += bitwiseFetchSize
	// Shift the bitfield up to add the 0 bit that terminates the mode bits.
	bitfield <<= 1
	// Shift in all of the mode bits.
	baseAlign := uint64(1 << 12)
	for baseAlign < offsetAlign {
		baseAlign <<= 1
		bitfield <<= 1
		bitfield += 1
	}
	// Shift 2 more bits for the version, this should now be 16 bits total. The
	// two final bits for the version are both kept at 0 to siganl version 1.
	bitfield <<= 2

	// Set the bitfield and return.
	ld.bitfield = bitfield
	return nil
}
