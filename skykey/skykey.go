package skykey

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/url"

	"github.com/aead/chacha20/chacha"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/encoding"
)

const (
	// SkykeyScheme is the URI scheme for encoded skykeys.
	SkykeyScheme = "skykey"

	// SkykeyIDLen is the length of a SkykeyID
	SkykeyIDLen = 16

	// MaxKeyNameLen is the maximum length of a skykey's name.
	MaxKeyNameLen = 128

	// maxEntropyLen is used in unmarshalDataOnly as a cap for the entropy. It
	// should only ever go up between releases. The cap prevents over-allocating
	// when reading the length of a deleted skykey.
	// It must be at most MaxKeyNameLen plus the max entropy size for any
	// cipher-type.
	maxEntropyLen = 256
)

// Define SkykeyTypes. Constants stated explicitly (instead of
// `SkykeyType(iota)`) to avoid re-ordering mistakes in the future.
const (
	// TypeInvalid represents an invalid skykey type.
	TypeInvalid = SkykeyType(0x00)

	// TypePublicID is a Skykey that uses XChaCha20. It reveals its
	// skykey ID in *every* skyfile it encrypts.
	TypePublicID = SkykeyType(0x01)

	// TypePrivateID is a Skykey that uses XChaCha20 that does not
	// reveal its skykey ID when encrypting Skyfiles. Instead, it marks the skykey
	// used for encryption by storing an encrypted identifier that can only be
	// successfully decrypted with the correct skykey.
	TypePrivateID = SkykeyType(0x02)

	// typeDeletedSkykey is used internally to mark a key as deleted in the skykey
	// manager. It is different from TypeInvalid because TypeInvalid can be used
	// to catch other kinds of errors, i.e. accidentally using a Skykey{} with
	// unset fields will cause an invalid-related error.
	typeDeletedSkykey = SkykeyType(0xFF)
)

var (
	// SkykeySpecifier is used as a prefix when hashing Skykeys to compute their
	// ID.
	SkykeySpecifier               = types.NewSpecifier("Skykey")
	skyfileEncryptionIDSpecifier  = types.NewSpecifier("SkyfileEncID")
	skyfileEncryptionIDDerivation = types.NewSpecifier("SFEncIDDerivPath")

	errUnsupportedSkykeyType            = errors.New("Unsupported Skykey type")
	errUnmarshalDataErr                 = errors.New("Unable to unmarshal Skykey data")
	errCannotMarshalTypeInvalidSkykey   = errors.New("Cannot marshal or unmarshal Skykey of TypeInvalid type")
	errInvalidEntropyLength             = errors.New("Invalid skykey entropy length")
	errSkykeyTypeDoesNotSupportFunction = errors.New("Operation not supported by this SkykeyType")

	errInvalidIDorNonceLength = errors.New("Invalid length for encryptionID or nonce in MatchesSkyfileEncryptionID")

	// ErrInvalidSkykeyType is returned when an invalid SkykeyType is being used.
	ErrInvalidSkykeyType = errors.New("Invalid skykey type")
)

// SkykeyID is the identifier of a skykey.
type SkykeyID [SkykeyIDLen]byte

// SkykeyType encodes the encryption scheme and method used by the Skykey.
type SkykeyType byte

// Skykey is a key used to encrypt/decrypt skyfiles.
type Skykey struct {
	Name    string
	Type    SkykeyType
	Entropy []byte
}

// compatSkykeyV148 is the original skykey format. It is defined here for
// compatibility purposes. It should only be used to convert keys of the old
// format to the new format.
type compatSkykeyV148 struct {
	name       string
	ciphertype crypto.CipherType
	entropy    []byte
}

// ToString returns the string representation of the ciphertype.
func (t SkykeyType) ToString() string {
	switch t {
	case TypePublicID:
		return "public-id"
	case TypePrivateID:
		return "private-id"
	default:
		return "invalid"
	}
}

// FromString reads a SkykeyType from a string.
func (t *SkykeyType) FromString(s string) error {
	switch s {
	case "public-id":
		*t = TypePublicID
	case "private-id":
		*t = TypePrivateID
	default:
		return ErrInvalidSkykeyType
	}
	return nil
}

// unmarshalSia decodes the Skykey into the reader.
func (skOld *compatSkykeyV148) unmarshalSia(r io.Reader) error {
	d := encoding.NewDecoder(r, encoding.DefaultAllocLimit)
	d.Decode(&skOld.name)
	d.Decode(&skOld.ciphertype)
	d.Decode(&skOld.entropy)

	if err := d.Err(); err != nil {
		return err
	}
	if len(skOld.name) > MaxKeyNameLen {
		return errSkykeyNameToolong
	}
	if len(skOld.entropy) != chacha.KeySize+chacha.XNonceSize {
		return errInvalidEntropyLength
	}

	return nil
}

// marshalSia encodes the Skykey into the writer.
func (skOld compatSkykeyV148) marshalSia(w io.Writer) error {
	e := encoding.NewEncoder(w)
	e.Encode(skOld.name)
	e.Encode(skOld.ciphertype)
	e.Encode(skOld.entropy)
	return e.Err()
}

// fromString decodes a base64-encoded string, interpreting it as the old skykey
// format.
func (skOld *compatSkykeyV148) fromString(s string) error {
	keyBytes, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	return skOld.unmarshalSia(bytes.NewReader(keyBytes))
}

// convertToUpdatedFormat converts the skykey from the old format to the updated
// format.
func (skOld compatSkykeyV148) convertToUpdatedFormat() (Skykey, error) {
	sk := Skykey{
		Name:    skOld.name,
		Type:    TypePublicID,
		Entropy: skOld.entropy,
	}

	// Sanity check that we can actually make a CipherKey with this.
	_, err := crypto.NewSiaKey(sk.CipherType(), sk.Entropy)
	if err != nil {
		return Skykey{}, errors.AddContext(err, "Unable to convert skykey from old format correctly")
	}

	return sk, nil
}

// unmarshalAndConvertFromOldFormat unmarshals data from the reader as a skykey
// using the old format, and attempts to convert it to the new format.
func (sk *Skykey) unmarshalAndConvertFromOldFormat(r io.Reader) error {
	var oldFormatSkykey compatSkykeyV148
	err := oldFormatSkykey.unmarshalSia(r)
	if err != nil {
		return err
	}
	convertedSk, err := oldFormatSkykey.convertToUpdatedFormat()
	if err != nil {
		return err
	}
	sk.Name = convertedSk.Name
	sk.Type = convertedSk.Type
	sk.Entropy = convertedSk.Entropy
	return sk.IsValid()
}

// CipherType returns the crypto.CipherType used by this Skykey.
func (t SkykeyType) CipherType() crypto.CipherType {
	switch t {
	case TypePublicID, TypePrivateID:
		return crypto.TypeXChaCha20
	default:
		return crypto.TypeInvalid
	}
}

// CipherType returns the crypto.CipherType used by this Skykey.
func (sk *Skykey) CipherType() crypto.CipherType {
	return sk.Type.CipherType()
}

// unmarshalDataOnly decodes the Skykey data into the reader.
func (sk *Skykey) unmarshalDataOnly(r io.Reader) error {
	d := encoding.NewDecoder(r, encoding.DefaultAllocLimit)
	typeByte, _ := d.ReadByte()
	sk.Type = SkykeyType(typeByte)

	var entropyLen uint64
	switch sk.Type {
	case TypePublicID, TypePrivateID:
		entropyLen = chacha.KeySize + chacha.XNonceSize
	case TypeInvalid:
		return errCannotMarshalTypeInvalidSkykey
	case typeDeletedSkykey:
		entropyLen = d.NextUint64()
		// Avoid panicking due to overallocation.
		if entropyLen > maxEntropyLen {
			return errInvalidEntropyLength
		}
	default:
		return errUnsupportedSkykeyType
	}

	sk.Entropy = make([]byte, entropyLen)
	d.ReadFull(sk.Entropy)
	if err := d.Err(); err != nil {
		return err
	}
	return nil
}

// unmarshalSia decodes the Skykey data and name into the reader.
func (sk *Skykey) unmarshalSia(r io.Reader) error {
	err := sk.unmarshalDataOnly(r)
	if err != nil {
		return errors.Compose(errUnmarshalDataErr, err)
	}

	if sk.Type == typeDeletedSkykey {
		return nil
	}

	d := encoding.NewDecoder(r, encoding.DefaultAllocLimit)
	d.Decode(&sk.Name)
	if err := d.Err(); err != nil {
		return err
	}

	return sk.IsValid()
}

// marshalDataOnly encodes the Skykey data into the writer.
func (sk Skykey) marshalDataOnly(w io.Writer) error {
	e := encoding.NewEncoder(w)

	var entropyLen int
	switch sk.Type {
	case TypePublicID, TypePrivateID:
		entropyLen = chacha.KeySize + chacha.XNonceSize
	case TypeInvalid:
		return errCannotMarshalTypeInvalidSkykey
	default:
		return errUnsupportedSkykeyType
	}

	if len(sk.Entropy) != entropyLen {
		return errInvalidEntropyLength
	}

	e.WriteByte(byte(sk.Type))
	e.Write(sk.Entropy[:entropyLen])
	return e.Err()
}

// marshalSia encodes the Skykey data and name into the writer.
func (sk Skykey) marshalSia(w io.Writer) error {
	err := sk.marshalDataOnly(w)
	if err != nil {
		return err
	}
	e := encoding.NewEncoder(w)
	e.Encode(sk.Name)
	return e.Err()
}

// toURL encodes the skykey as a URL.
func (sk Skykey) toURL() (url.URL, error) {
	var b bytes.Buffer
	err := sk.marshalDataOnly(&b)
	if err != nil {
		return url.URL{}, err
	}
	skykeyString := base64.URLEncoding.EncodeToString(b.Bytes())

	skURL := url.URL{
		Scheme: SkykeyScheme,
		Opaque: skykeyString,
	}
	if sk.Name != "" {
		skURL.RawQuery = "name=" + sk.Name
	}
	return skURL, nil
}

// ToString encodes the Skykey as a base64 string.
func (sk Skykey) ToString() (string, error) {
	skURL, err := sk.toURL()
	if err != nil {
		return "", err
	}
	return skURL.String(), nil
}

// FromString decodes the base64 string into a Skykey.
func (sk *Skykey) FromString(s string) error {
	sURL, err := url.Parse(s)
	if err != nil {
		return err
	}

	// Get the skykey data from the path/opaque data.
	var skData string
	if sURL.Scheme == SkykeyScheme {
		skData = sURL.Opaque
	} else if sURL.Scheme == "" {
		skData = sURL.Path
	} else {
		return errors.New("Unknown URI scheme for skykey")
	}

	values := sURL.Query()
	sk.Name = values.Get("name") // defaults to ""
	if len(sk.Name) > MaxKeyNameLen {
		return errSkykeyNameToolong
	}

	keyBytes, err := base64.URLEncoding.DecodeString(skData)
	if err != nil {
		return err
	}
	return sk.unmarshalDataOnly(bytes.NewReader(keyBytes))
}

// ID returns the ID for the Skykey. A master Skykey and all file-specific
// skykeys derived from it share the same ID because they only differ in nonce
// values, not key values. This fact is used to identify the master Skykey
// with which a Skyfile was encrypted.
func (sk Skykey) ID() (keyID SkykeyID) {
	entropy := sk.Entropy

	switch sk.Type {
	// Ignore the nonce for this type because the nonce is different for each
	// file-specific subkey.
	case TypePublicID, TypePrivateID:
		entropy = sk.Entropy[:chacha.KeySize]

	default:
		build.Critical("Computing ID with skykey of unknown type: ", sk.Type)
	}

	h := crypto.HashAll(SkykeySpecifier, sk.Type, entropy)
	copy(keyID[:], h[:SkykeyIDLen])
	return keyID
}

// ToString encodes the SkykeyID as a base64 string.
func (id SkykeyID) ToString() string {
	return base64.URLEncoding.EncodeToString(id[:])
}

// FromString decodes the base64 string into a Skykey ID.
func (id *SkykeyID) FromString(s string) error {
	idBytes, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	if len(idBytes) != SkykeyIDLen {
		return errors.New("Skykey ID has invalid length")
	}
	copy(id[:], idBytes[:])
	return nil
}

// equals returns true if and only if the two Skykeys are equal.
func (sk *Skykey) equals(otherKey Skykey) bool {
	return sk.Name == otherKey.Name && sk.equalData(otherKey)
}

// equalData returns true if and only if the two Skykeys have the same
// underlying data. They can differ in name.
func (sk *Skykey) equalData(otherKey Skykey) bool {
	return sk.Type == otherKey.Type && bytes.Equal(sk.Entropy[:], otherKey.Entropy[:])
}

// GenerateFileSpecificSubkey creates a new subkey specific to a certain file
// being uploaded/downloaded. Skykeys can only be used once with a
// given nonce, so this method is used to generate keys with new nonces when a
// new file is uploaded.
func (sk *Skykey) GenerateFileSpecificSubkey() (Skykey, error) {
	// Generate a new random nonce.
	nonce := make([]byte, chacha.XNonceSize)
	fastrand.Read(nonce[:])
	return sk.SubkeyWithNonce(nonce)
}

// DeriveSubkey is used to create Skykeys with the same key, but with a
// different nonce. This is used to create file-specific keys, and separate keys
// for Skyfile baseSector uploads and fanout uploads.
func (sk *Skykey) DeriveSubkey(derivation []byte) (Skykey, error) {
	nonce := sk.Nonce()
	derivedNonceHash := crypto.HashAll(nonce, derivation)
	derivedNonce := derivedNonceHash[:chacha.XNonceSize]

	return sk.SubkeyWithNonce(derivedNonce)
}

// SubkeyWithNonce creates a new subkey with the same key data as this key, but
// with the given nonce.
func (sk *Skykey) SubkeyWithNonce(nonce []byte) (Skykey, error) {
	if len(nonce) != chacha.XNonceSize {
		return Skykey{}, errors.New("Incorrect nonce size")
	}

	entropy := make([]byte, chacha.KeySize+chacha.XNonceSize)
	copy(entropy[:chacha.KeySize], sk.Entropy[:chacha.KeySize])
	copy(entropy[chacha.KeySize:], nonce[:])

	// Sanity check that we can actually make a CipherKey with this.
	_, err := crypto.NewSiaKey(sk.CipherType(), entropy)
	if err != nil {
		return Skykey{}, errors.AddContext(err, "error creating new skykey subkey")
	}

	subkey := Skykey{sk.Name, sk.Type, entropy}
	return subkey, nil
}

// GenerateSkyfileEncryptionID creates an encrypted identifier that is used for
// PrivateID encrypted files.
// NOTE: This method MUST only be called using a FileSpecificSkykey.
func (sk *Skykey) GenerateSkyfileEncryptionID() ([SkykeyIDLen]byte, error) {
	if sk.Type != TypePrivateID {
		return [SkykeyIDLen]byte{}, errSkykeyTypeDoesNotSupportFunction
	}
	if SkykeyIDLen != types.SpecifierLen {
		build.Critical("SkykeyID and Specifier expected to have same size")
	}

	encIDSkykey, err := sk.DeriveSubkey(skyfileEncryptionIDDerivation[:])
	if err != nil {
		return [SkykeyIDLen]byte{}, err
	}

	// Get a CipherKey to encrypt the encryption specifer.
	ck, err := encIDSkykey.CipherKey()
	if err != nil {
		return [SkykeyIDLen]byte{}, err
	}

	// Encrypt the specifier.
	var skyfileID [SkykeyIDLen]byte
	copy(skyfileID[:], skyfileEncryptionIDSpecifier[:])
	_, err = ck.DecryptBytesInPlace(skyfileID[:], 0)
	if err != nil {
		return [SkykeyIDLen]byte{}, err
	}
	return skyfileID, nil
}

// MatchesSkyfileEncryptionID returns true if and only if the skykey was the one
// used with this nonce to create the encryptionID.
func (sk *Skykey) MatchesSkyfileEncryptionID(encryptionID, nonce []byte) (bool, error) {
	if len(encryptionID) != SkykeyIDLen || len(nonce) != chacha.XNonceSize {
		return false, errInvalidIDorNonceLength
	}
	// This only applies to TypePrivateID keys.
	if sk.Type != TypePrivateID {
		return false, nil
	}

	// Create the subkey for the encryption ID.
	fileSkykey, err := sk.SubkeyWithNonce(nonce)
	if err != nil {
		return false, err
	}
	encIDSkykey, err := fileSkykey.DeriveSubkey(skyfileEncryptionIDDerivation[:])
	if err != nil {
		return false, err
	}

	// Decrypt the identifier and check that it.
	ck, err := encIDSkykey.CipherKey()
	if err != nil {
		return false, err
	}
	plaintextBytes, err := ck.DecryptBytes(encryptionID[:])
	if err != nil {
		return false, err
	}
	if bytes.Equal(plaintextBytes, skyfileEncryptionIDSpecifier[:]) {
		return true, nil
	}
	return false, nil
}

// CipherKey returns the crypto.CipherKey equivalent of this Skykey.
func (sk *Skykey) CipherKey() (crypto.CipherKey, error) {
	return crypto.NewSiaKey(sk.CipherType(), sk.Entropy)
}

// Nonce returns the nonce of this Skykey.
func (sk *Skykey) Nonce() []byte {
	nonce := make([]byte, chacha.XNonceSize)
	copy(nonce[:], sk.Entropy[chacha.KeySize:])
	return nonce
}

// IsValid returns an nil if the skykey is valid and an error otherwise.
func (sk *Skykey) IsValid() error {
	if len(sk.Name) > MaxKeyNameLen {
		return errSkykeyNameToolong
	}

	switch sk.Type {
	case TypePublicID, TypePrivateID:
		if len(sk.Entropy) != chacha.KeySize+chacha.XNonceSize {
			return errInvalidEntropyLength
		}

	default:
		return errUnsupportedSkykeyType
	}

	_, err := crypto.NewSiaKey(sk.CipherType(), sk.Entropy)
	if err != nil {
		return err
	}
	return nil
}
