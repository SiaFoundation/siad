package renter

// skyfile_encryption.go provides utilities for encrypting and decrypting
// skyfiles.

import (
	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/Sia/types"
)

// baseSectorNonceDerivation is the specifier used to derive a nonce for base
// sector encryption
var baseSectorNonceDerivation = types.NewSpecifier("BaseSectorNonce")

// fanoutNonceDerivation is the specifier used to derive a nonce for
// fanout encryption.
var fanoutNonceDerivation = types.NewSpecifier("FanoutNonce")

// deriveFileSpecificKey returns the file-specific Skykey used to encrypt this
// layout.
func (r *Renter) deriveFileSpecificKey(sl *skyfileLayout) (skykey.Skykey, error) {
	// Grab the key ID from the layout.
	var keyID skykey.SkykeyID
	copy(keyID[:], sl.keyData[:skykey.SkykeyIDLen])

	// Try to get the skykey associated with that ID.
	masterSkykey, err := r.staticSkykeyManager.KeyByID(keyID)
	if err != nil {
		return skykey.Skykey{}, errors.AddContext(err, "Error getting skykey")
	}

	// Grab the nonce
	nonce := make([]byte, len(masterSkykey.Nonce()))
	copy(nonce[:], sl.keyData[skykey.SkykeyIDLen:])

	// Make a file-specific subkey.
	return masterSkykey.SubkeyWithNonce(nonce)
}

// deriveFanoutKey returns the crypto.CipherKey that should be used for
// decrypting the fanout stream from the skyfile stored using this layout.
func (r *Renter) deriveFanoutKey(sl *skyfileLayout) (crypto.CipherKey, error) {
	if sl.cipherType != crypto.TypeXChaCha20 {
		return crypto.NewSiaKey(sl.cipherType, sl.keyData[:])
	}

	fileSkykey, err := r.deriveFileSpecificKey(sl)
	if err != nil {
		return nil, errors.AddContext(err, "Error getting file specifc key")
	}

	// Derive the fanout key.
	sk, err := fileSkykey.DeriveSubkey(fanoutNonceDerivation[:])
	if err != nil {
		return nil, errors.AddContext(err, "Error deriving skykey subkey")
	}
	return sk.CipherKey()
}

// decryptBaseSector attempts to decrypt the baseSector. If it has the
// necessary Skykey, it will decrypt the baseSector in-place.
func (r *Renter) decryptBaseSector(baseSector []byte) error {
	// Sanity check - baseSector should not be more than modules.SectorSize.
	// Note that the base sector may be smaller in the event of a packed
	// skyfile.
	if uint64(len(baseSector)) > modules.SectorSize {
		build.Critical("decryptBaseSector given a baseSector that is too large")
		return errors.New("baseSector too large")
	}

	var sl skyfileLayout
	sl.decode(baseSector)

	// Grab the key ID from the layout.
	var keyID skykey.SkykeyID
	copy(keyID[:], sl.keyData[:skykey.SkykeyIDLen])

	// Try to get the skykey associated with that ID.
	masterSkykey, err := r.staticSkykeyManager.KeyByID(keyID)
	if err != nil {
		return errors.AddContext(err, "Error getting skykey")
	}

	// Get the nonce and use it to derive the file-specific key.
	nonce := make([]byte, len(masterSkykey.Nonce()))
	copy(nonce[:], sl.keyData[skykey.SkykeyIDLen:skykey.SkykeyIDLen+len(masterSkykey.Nonce())])
	fileSkykey, err := masterSkykey.SubkeyWithNonce(nonce)
	if err != nil {
		return errors.AddContext(err, "Unable to derive file-specific subkey")
	}

	// Derive the base sector subkey and use it to decrypt the base sector.
	baseSectorKey, err := fileSkykey.DeriveSubkey(baseSectorNonceDerivation[:])
	if err != nil {
		return errors.AddContext(err, "Unable to derive baseSector subkey")
	}

	// Get the cipherkey.
	ck, err := baseSectorKey.CipherKey()
	if err != nil {
		return errors.AddContext(err, "Unable to get baseSector cipherkey")
	}

	_, err = ck.DecryptBytesInPlace(baseSector, 0)
	if err != nil {
		return errors.New("Error decrypting baseSector for download")
	}

	// Save the visible-by-default fields of the baseSector's layout.
	version := sl.version
	cipherType := sl.cipherType
	var keyData [64]byte
	copy(keyData[:], sl.keyData[:])
	keyData[63] ^= 1

	// Decode the now decrypted layout.
	sl.decode(baseSector)

	// Reset the visible-by-default fields.
	// (They were turned into random values by the decryption)
	sl.version = version
	sl.cipherType = cipherType
	copy(sl.keyData[:], keyData[:])

	// Now re-copy the decrypted layout into the decrypted baseSector.
	copy(baseSector[:SkyfileLayoutSize], sl.encode())

	return nil
}

// encryptBaseSectorWithSkykey encrypts the baseSector in place using the given
// Skykey. Certain fields of the layout are restored in plaintext into the
// encrypted baseSector to indicate to downloaders what Skykey was used.
func encryptBaseSectorWithSkykey(baseSector []byte, plaintextLayout skyfileLayout, sk skykey.Skykey) error {
	baseSectorKey, err := sk.DeriveSubkey(baseSectorNonceDerivation[:])
	if err != nil {
		return errors.AddContext(err, "Unable to derive baseSector subkey")
	}

	// Get the cipherkey.
	ck, err := baseSectorKey.CipherKey()
	if err != nil {
		return errors.AddContext(err, "Unable to get baseSector cipherkey")
	}

	_, err = ck.DecryptBytesInPlace(baseSector, 0)
	if err != nil {
		return errors.New("Error decrypting baseSector for download")
	}

	// Re-add the visible-by-default fields of the baseSector.
	var encryptedLayout skyfileLayout
	encryptedLayout.decode(baseSector)
	encryptedLayout.version = plaintextLayout.version
	encryptedLayout.cipherType = baseSectorKey.CipherType()

	// Finally: add the key ID and nonce to the base sector in plaintext.
	keyID := sk.ID()
	nonce := sk.Nonce()
	copy(encryptedLayout.keyData[:len(keyID)], keyID[:])
	copy(encryptedLayout.keyData[len(keyID):len(keyID)+len(nonce)], nonce[:])

	// Now re-copy the encrypted layout into the baseSector.
	copy(baseSector[:SkyfileLayoutSize], encryptedLayout.encode())
	return nil
}

// isEncryptedBaseSector returns true if and only if the the baseSector is
// encrypted.
func isEncryptedBaseSector(baseSector []byte) bool {
	var sl skyfileLayout
	sl.decode(baseSector)
	return isEncryptedLayout(sl)
}

// isEncryptedLayout returns true if and only if the the layout indicates that
// it is from an encrypted base sector.
func isEncryptedLayout(sl skyfileLayout) bool {
	return sl.version == 1 && sl.cipherType == crypto.TypeXChaCha20
}

func encryptionEnabled(sup modules.SkyfileUploadParameters) bool {
	return sup.SkykeyName != "" || sup.SkykeyID != skykey.SkykeyID{}
}
