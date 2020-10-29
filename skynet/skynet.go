package skynet

import (
	"encoding/binary"
	"encoding/json"

	"github.com/aead/chacha20/chacha"
	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// SkyfileLayoutSize describes the amount of space within the first sector
	// of a skyfile used to describe the rest of the skyfile.
	SkyfileLayoutSize = 99

	// SkyfileVersion establishes the current version for creating skyfiles.
	// The skyfile versions are different from the siafile versions.
	SkyfileVersion = 1

	// layoutKeyDataSize is the size of the key-data field in a skyfileLayout.
	layoutKeyDataSize = 64
)

// BaseSectorNonceDerivation is the specifier used to derive a nonce for base
// sector encryption
var BaseSectorNonceDerivation = types.NewSpecifier("BaseSectorNonce")

// FanoutNonceDerivation is the specifier used to derive a nonce for
// fanout encryption.
var FanoutNonceDerivation = types.NewSpecifier("FanoutNonce")

// SkyfileLayout explains the layout information that is used for storing data
// inside of the skyfile. The SkyfileLayout always appears as the first bytes
// of the leading chunk.
type SkyfileLayout struct {
	Version            uint8
	Filesize           uint64
	MetadataSize       uint64
	FanoutSize         uint64
	FanoutDataPieces   uint8
	FanoutParityPieces uint8
	CipherType         crypto.CipherType
	KeyData            [layoutKeyDataSize]byte // keyData is incompatible with ciphers that need keys larger than 64 bytes
}

// Encode will return a []byte that has compactly encoded all of the layout
// data.
func (sl *SkyfileLayout) Encode() []byte {
	b := make([]byte, SkyfileLayoutSize)
	offset := 0
	b[offset] = sl.Version
	offset += 1
	binary.LittleEndian.PutUint64(b[offset:], sl.Filesize)
	offset += 8
	binary.LittleEndian.PutUint64(b[offset:], sl.MetadataSize)
	offset += 8
	binary.LittleEndian.PutUint64(b[offset:], sl.FanoutSize)
	offset += 8
	b[offset] = sl.FanoutDataPieces
	offset += 1
	b[offset] = sl.FanoutParityPieces
	offset += 1
	copy(b[offset:], sl.CipherType[:])
	offset += len(sl.CipherType)
	copy(b[offset:], sl.KeyData[:])
	offset += len(sl.KeyData)

	// Sanity check. If this check fails, encode() does not match the
	// SkyfileLayoutSize.
	if offset != SkyfileLayoutSize {
		build.Critical("layout size does not match the amount of data encoded")
	}
	return b
}

// Decode will take a []byte and load the layout from that []byte.
func (sl *SkyfileLayout) Decode(b []byte) {
	offset := 0
	sl.Version = b[offset]
	offset += 1
	sl.Filesize = binary.LittleEndian.Uint64(b[offset:])
	offset += 8
	sl.MetadataSize = binary.LittleEndian.Uint64(b[offset:])
	offset += 8
	sl.FanoutSize = binary.LittleEndian.Uint64(b[offset:])
	offset += 8
	sl.FanoutDataPieces = b[offset]
	offset += 1
	sl.FanoutParityPieces = b[offset]
	offset += 1
	copy(sl.CipherType[:], b[offset:])
	offset += len(sl.CipherType)
	copy(sl.KeyData[:], b[offset:])
	offset += len(sl.KeyData)

	// Sanity check. If this check fails, decode() does not match the
	// SkyfileLayoutSize.
	if offset != SkyfileLayoutSize {
		build.Critical("layout size does not match the amount of data decoded")
	}
}

// DecodeFanout will take the fanout bytes from a baseSector and decode them.
func DecodeFanout(sl SkyfileLayout, fanoutBytes []byte) (piecesPerChunk, chunkRootsSize, numChunks uint64, err error) {
	// Special case: if the data of the file is using 1-of-N erasure coding,
	// each piece will be identical, so the fanout will only have encoded a
	// single piece for each chunk.
	if sl.FanoutDataPieces == 1 && sl.CipherType == crypto.TypePlain {
		piecesPerChunk = 1
		chunkRootsSize = crypto.HashSize
	} else {
		// This is the case where the file data is not 1-of-N. Every piece is
		// different, so every piece must get enumerated.
		piecesPerChunk = uint64(sl.FanoutDataPieces) + uint64(sl.FanoutParityPieces)
		chunkRootsSize = crypto.HashSize * piecesPerChunk
	}
	// Sanity check - the fanout bytes should be an even number of chunks.
	if uint64(len(fanoutBytes))%chunkRootsSize != 0 {
		err = errors.New("the fanout bytes do not contain an even number of chunks")
		return
	}
	numChunks = uint64(len(fanoutBytes)) / chunkRootsSize
	return
}

// DecryptBaseSector attempts to decrypt the baseSector. If it has the necessary
// Skykey, it will decrypt the baseSector in-place.It returns the file-specific
// skykey to be used for decrypting the rest of the associated skyfile.
func DecryptBaseSector(baseSector []byte, sk skykey.Skykey) (skykey.Skykey, error) {
	// Sanity check - baseSector should not be more than modules.SectorSize.
	// Note that the base sector may be smaller in the event of a packed
	// skyfile.
	if uint64(len(baseSector)) > modules.SectorSize {
		build.Critical("decryptBaseSector given a baseSector that is too large")
		return skykey.Skykey{}, errors.New("baseSector too large")
	}
	var sl SkyfileLayout
	sl.Decode(baseSector)

	if !IsEncryptedLayout(sl) {
		build.Critical("Expected layout to be marked as encrypted!")
	}

	// Get the nonce to be used for getting private-id skykeys, and for deriving the
	// file-specific skykey.
	nonce := make([]byte, chacha.XNonceSize)
	copy(nonce[:], sl.KeyData[skykey.SkykeyIDLen:skykey.SkykeyIDLen+chacha.XNonceSize])

	// Grab the key ID from the layout.
	var keyID skykey.SkykeyID
	copy(keyID[:], sl.KeyData[:skykey.SkykeyIDLen])

	// Derive the file-specific key.
	fileSkykey, err := sk.SubkeyWithNonce(nonce)
	if err != nil {
		return skykey.Skykey{}, errors.AddContext(err, "Unable to derive file-specific subkey")
	}

	// Derive the base sector subkey and use it to decrypt the base sector.
	baseSectorKey, err := fileSkykey.DeriveSubkey(BaseSectorNonceDerivation[:])
	if err != nil {
		return skykey.Skykey{}, errors.AddContext(err, "Unable to derive baseSector subkey")
	}

	// Get the cipherkey.
	ck, err := baseSectorKey.CipherKey()
	if err != nil {
		return skykey.Skykey{}, errors.AddContext(err, "Unable to get baseSector cipherkey")
	}

	_, err = ck.DecryptBytesInPlace(baseSector, 0)
	if err != nil {
		return skykey.Skykey{}, errors.New("Error decrypting baseSector for download")
	}

	// Save the visible-by-default fields of the baseSector's layout.
	version := sl.Version
	cipherType := sl.CipherType
	var keyData [64]byte
	copy(keyData[:], sl.KeyData[:])

	// Decode the now decrypted layout.
	sl.Decode(baseSector)

	// Reset the visible-by-default fields.
	// (They were turned into random values by the decryption)
	sl.Version = version
	sl.CipherType = cipherType
	copy(sl.KeyData[:], keyData[:])

	// Now re-copy the decrypted layout into the decrypted baseSector.
	copy(baseSector[:SkyfileLayoutSize], sl.Encode())

	return fileSkykey, nil
}

// DeriveFanoutKey returns the crypto.CipherKey that should be used for
// decrypting the fanout stream from the skyfile stored using this layout.
func DeriveFanoutKey(sl *SkyfileLayout, fileSkykey skykey.Skykey) (crypto.CipherKey, error) {
	if sl.CipherType != crypto.TypeXChaCha20 {
		return crypto.NewSiaKey(sl.CipherType, sl.KeyData[:])
	}

	// Derive the fanout key.
	fanoutSkykey, err := fileSkykey.DeriveSubkey(FanoutNonceDerivation[:])
	if err != nil {
		return nil, errors.AddContext(err, "Error deriving skykey subkey")
	}
	return fanoutSkykey.CipherKey()
}

// IsEncryptedBaseSector returns true if and only if the the baseSector is
// encrypted.
func IsEncryptedBaseSector(baseSector []byte) bool {
	var sl SkyfileLayout
	sl.Decode(baseSector)
	return IsEncryptedLayout(sl)
}

// IsEncryptedLayout returns true if and only if the the layout indicates that
// it is from an encrypted base sector.
func IsEncryptedLayout(sl SkyfileLayout) bool {
	return sl.Version == 1 && sl.CipherType == crypto.TypeXChaCha20
}

// ParseSkyfileMetadata will pull the metadata (including layout and fanout) out
// of a skyfile.
func ParseSkyfileMetadata(baseSector []byte) (sl SkyfileLayout, fanoutBytes []byte, sm modules.SkyfileMetadata, baseSectorPayload []byte, err error) {
	// Sanity check - baseSector should not be more than modules.SectorSize.
	// Note that the base sector may be smaller in the event of a packed
	// skyfile.
	if uint64(len(baseSector)) > modules.SectorSize {
		build.Critical("parseSkyfileMetadata given a baseSector that is too large")
	}

	// Parse the layout.
	var offset uint64
	sl.Decode(baseSector)
	offset += SkyfileLayoutSize

	// Check the version.
	if sl.Version != 1 {
		return SkyfileLayout{}, nil, modules.SkyfileMetadata{}, nil, errors.New("unsupported skyfile version")
	}

	// Currently there is no support for skyfiles with fanout + metadata that
	// exceeds the base sector.
	if offset+sl.FanoutSize+sl.MetadataSize > uint64(len(baseSector)) || sl.FanoutSize > modules.SectorSize || sl.MetadataSize > modules.SectorSize {
		return SkyfileLayout{}, nil, modules.SkyfileMetadata{}, nil, errors.New("this version of siad does not support skyfiles with large fanouts and metadata")
	}

	// Parse the fanout.
	// NOTE: we copy the fanoutBytes instead of returning a slice into baseSector
	// because in PinSkylink the baseSector may be re-encrypted.
	fanoutBytes = make([]byte, sl.FanoutSize)
	copy(fanoutBytes, baseSector[offset:offset+sl.FanoutSize])
	offset += sl.FanoutSize

	// Parse the metadata.
	metadataSize := sl.MetadataSize
	err = json.Unmarshal(baseSector[offset:offset+metadataSize], &sm)
	if err != nil {
		return SkyfileLayout{}, nil, modules.SkyfileMetadata{}, nil, errors.AddContext(err, "unable to parse SkyfileMetadata from skyfile base sector")
	}
	offset += metadataSize

	// In version 1, the base sector payload is nil unless there is no fanout.
	if sl.FanoutSize == 0 {
		baseSectorPayload = baseSector[offset : offset+sl.Filesize]
	}

	return sl, fanoutBytes, sm, baseSectorPayload, nil
}
