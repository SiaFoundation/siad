package modules

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"

	"github.com/aead/chacha20/chacha"
	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/errors"
)

var (
	// ErrInvalidDefaultPath is returned when the specified default path is not
	// valid, e.g. the file it points to does not exist.
	ErrInvalidDefaultPath = errors.New("invalid default path provided")
)

// AddMultipartFile is a helper function to add a file to multipart form-data.
// Note that the given data will be treated as binary data and the multipart
// ContentType header will be set accordingly.
func AddMultipartFile(w *multipart.Writer, filedata []byte, filekey, filename string, filemode uint64, offset *uint64) (SkyfileSubfileMetadata, error) {
	filemodeStr := fmt.Sprintf("%o", filemode)
	contentType, err := fileContentType(filename, bytes.NewReader(filedata))
	if err != nil {
		return SkyfileSubfileMetadata{}, err
	}
	partHeader := createFormFileHeaders(filekey, filename, filemodeStr, contentType)
	part, err := w.CreatePart(partHeader)
	if err != nil {
		return SkyfileSubfileMetadata{}, err
	}
	_, err = part.Write(filedata)
	if err != nil {
		return SkyfileSubfileMetadata{}, err
	}
	metadata := SkyfileSubfileMetadata{
		Filename:    filename,
		ContentType: contentType,
		FileMode:    os.FileMode(filemode),
		Len:         uint64(len(filedata)),
	}
	if offset != nil {
		metadata.Offset = *offset
		*offset += metadata.Len
	}
	return metadata, nil
}

// BuildBaseSector will take all of the elements of the base sector and copy
// them into a freshly created base sector.
func BuildBaseSector(layoutBytes, fanoutBytes, metadataBytes, fileBytes []byte) ([]byte, uint64) {
	// Sanity Check
	totalSize := len(layoutBytes) + len(fanoutBytes) + len(metadataBytes) + len(fileBytes)
	if uint64(totalSize) > SectorSize {
		err := fmt.Errorf("inputs too large for baseSector: totalSize %v, layoutBytes %v, fanoutBytes %v, metadataBytes %v, fileBytes %v",
			totalSize, len(layoutBytes), len(fanoutBytes), len(metadataBytes), len(fileBytes))
		build.Critical(err)
		return nil, 0
	}

	// Build baseSector
	baseSector := make([]byte, SectorSize)
	offset := 0
	copy(baseSector[offset:], layoutBytes)
	offset += len(layoutBytes)
	copy(baseSector[offset:], fanoutBytes)
	offset += len(fanoutBytes)
	copy(baseSector[offset:], metadataBytes)
	offset += len(metadataBytes)
	copy(baseSector[offset:], fileBytes)
	offset += len(fileBytes)
	return baseSector, uint64(offset)
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
	// Sanity check - baseSector should not be more than SectorSize.
	// Note that the base sector may be smaller in the event of a packed
	// skyfile.
	if uint64(len(baseSector)) > SectorSize {
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

// EnsurePrefix checks if `str` starts with `prefix` and adds it if that's not
// the case.
func EnsurePrefix(str, prefix string) string {
	if strings.HasPrefix(str, prefix) {
		return str
	}
	return prefix + str
}

// EnsureSuffix checks if `str` ends with `suffix` and adds it if that's not
// the case.
func EnsureSuffix(str, suffix string) string {
	if strings.HasSuffix(str, suffix) {
		return str
	}
	return str + suffix
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
func ParseSkyfileMetadata(baseSector []byte) (sl SkyfileLayout, fanoutBytes []byte, sm SkyfileMetadata, baseSectorPayload []byte, err error) {
	// Sanity check - baseSector should not be more than SectorSize.
	// Note that the base sector may be smaller in the event of a packed
	// skyfile.
	if uint64(len(baseSector)) > SectorSize {
		build.Critical("parseSkyfileMetadata given a baseSector that is too large")
	}

	// Parse the layout.
	var offset uint64
	sl.Decode(baseSector)
	offset += SkyfileLayoutSize

	// Check the version.
	if sl.Version != 1 {
		return SkyfileLayout{}, nil, SkyfileMetadata{}, nil, errors.New("unsupported skyfile version")
	}

	// Currently there is no support for skyfiles with fanout + metadata that
	// exceeds the base sector.
	if offset+sl.FanoutSize+sl.MetadataSize > uint64(len(baseSector)) || sl.FanoutSize > SectorSize || sl.MetadataSize > SectorSize {
		return SkyfileLayout{}, nil, SkyfileMetadata{}, nil, errors.New("this version of siad does not support skyfiles with large fanouts and metadata")
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
		return SkyfileLayout{}, nil, SkyfileMetadata{}, nil, errors.AddContext(err, "unable to parse SkyfileMetadata from skyfile base sector")
	}
	offset += metadataSize

	// In version 1, the base sector payload is nil unless there is no fanout.
	if sl.FanoutSize == 0 {
		baseSectorPayload = baseSector[offset : offset+sl.Filesize]
	}

	return sl, fanoutBytes, sm, baseSectorPayload, nil
}

// SkyfileMetadataBytes will return the marshalled/encoded bytes for the
// skyfile metadata.
func SkyfileMetadataBytes(sm SkyfileMetadata) ([]byte, error) {
	// Compose the metadata into the leading chunk.
	metadataBytes, err := json.Marshal(sm)
	if err != nil {
		return nil, errors.AddContext(err, "unable to marshal the link file metadata")
	}
	return metadataBytes, nil
}

// ValidateSkyfileMetadata validates the given SkyfileMetadata
func ValidateSkyfileMetadata(metadata SkyfileMetadata) error {
	// check filename
	err := ValidatePathString(metadata.Filename, false)
	if err != nil {
		return errors.AddContext(err, fmt.Sprintf("invalid filename provided '%v'", metadata.Filename))
	}

	// check filename of every subfile
	if metadata.Subfiles != nil {
		for filename, md := range metadata.Subfiles {
			if filename != md.Filename {
				return errors.New("subfile name did not match metadata filename")
			}
			err := ValidatePathString(filename, false)
			if err != nil {
				return errors.AddContext(err, fmt.Sprintf("invalid filename provided for subfile '%v'", filename))
			}

			// note that we do not check the length property of a subfile as it
			// is possible a user might have uploaded an empty part
		}
	}

	// check length
	if metadata.Length == 0 {
		return errors.New("'Length' property not set on metadata")
	}

	// validate default path (only if default path was not explicitly disabled)
	if !metadata.DisableDefaultPath {
		metadata.DefaultPath, err = validateDefaultPath(metadata.DefaultPath, metadata.Subfiles)
		if err != nil {
			return errors.Compose(ErrInvalidDefaultPath, err)
		}
	}

	return nil
}

// createFormFileHeaders builds a header from the given params. These headers
// are used when creating the parts in a multi-part form upload.
func createFormFileHeaders(fieldname, filename, filemode, contentType string) textproto.MIMEHeader {
	quoteEscaper := strings.NewReplacer("\\", "\\\\", `"`, "\\\"")
	fieldname = quoteEscaper.Replace(fieldname)
	filename = quoteEscaper.Replace(filename)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Type", contentType)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldname, filename))
	h.Set("mode", filemode)
	return h
}

// fileContentType extracts the content type from a given file. If the content
// type cannot be determined by the file's extension, this function will read up
// to 512 bytes from the provided reader.
func fileContentType(filename string, file io.Reader) (string, error) {
	contentType := mime.TypeByExtension(filepath.Ext(filename))
	if contentType != "" {
		return contentType, nil
	}
	// Only the first 512 bytes are used to sniff the content type.
	buffer := make([]byte, 512)
	_, err := file.Read(buffer)
	if err != nil {
		return "", err
	}
	// Always returns a valid content-type by returning
	// "application/octet-stream" if no others seemed to match.
	return http.DetectContentType(buffer), nil
}

// validateDefaultPath ensures the given default path makes sense in relation to
// the subfiles being uploaded. It returns a potentially altered default path.
func validateDefaultPath(defaultPath string, subfiles SkyfileSubfiles) (string, error) {
	if defaultPath == "" {
		return defaultPath, nil
	}
	defaultPath = EnsurePrefix(defaultPath, "/")

	// check if we have a subfile at the given default path.
	subfile, found := subfiles[strings.TrimPrefix(defaultPath, "/")]
	if !found {
		return "", fmt.Errorf("no such path: %s", defaultPath)
	}

	// ensure it's an HTML file.
	if !subfile.IsHTML() {
		return "", fmt.Errorf("invalid default path '%s', the default path must point to an HTML file", defaultPath)
	}

	// ensure it's at the root of the Skyfile
	if strings.Count(defaultPath, "/") > 1 {
		return "", fmt.Errorf("invalid default path '%s', the default path must point to a file in the root directory of the skyfile", defaultPath)
	}

	return defaultPath, nil
}
