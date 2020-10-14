package renter

// skyfile_encryption.go provides utilities for encrypting and decrypting
// skyfiles.

import (
	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/Sia/skynet"
	"gitlab.com/NebulousLabs/Sia/types"

	"github.com/aead/chacha20/chacha"
)

// baseSectorNonceDerivation is the specifier used to derive a nonce for base
// sector encryption
var baseSectorNonceDerivation = types.NewSpecifier("BaseSectorNonce")

// fanoutNonceDerivation is the specifier used to derive a nonce for
// fanout encryption.
var fanoutNonceDerivation = types.NewSpecifier("FanoutNonce")

var errNoSkykeyMatchesSkyfileEncryptionID = errors.New("Unable to find matching skykey for public ID encryption")

// deriveFanoutKey returns the crypto.CipherKey that should be used for
// decrypting the fanout stream from the skyfile stored using this layout.
func (r *Renter) deriveFanoutKey(sl *skynet.SkyfileLayout, fileSkykey skykey.Skykey) (crypto.CipherKey, error) {
	if sl.CipherType != crypto.TypeXChaCha20 {
		return crypto.NewSiaKey(sl.CipherType, sl.KeyData[:])
	}

	// Derive the fanout key.
	fanoutSkykey, err := fileSkykey.DeriveSubkey(fanoutNonceDerivation[:])
	if err != nil {
		return nil, errors.AddContext(err, "Error deriving skykey subkey")
	}
	return fanoutSkykey.CipherKey()
}

// checkSkyfileEncryptionIDMatch tries to find a Skykey that can decrypt the
// identifier and be used for decrypting the associated skyfile. It returns an
// error if it is not found.
func (r *Renter) checkSkyfileEncryptionIDMatch(encryptionIdentifer []byte, nonce []byte) (skykey.Skykey, error) {
	allSkykeys := r.staticSkykeyManager.Skykeys()
	for _, sk := range allSkykeys {
		matches, err := sk.MatchesSkyfileEncryptionID(encryptionIdentifer, nonce)
		if err != nil {
			r.log.Debugln("SkykeyEncryptionID match err", err)
			continue
		}
		if matches {
			return sk, nil
		}
	}
	return skykey.Skykey{}, errNoSkykeyMatchesSkyfileEncryptionID
}

// decryptBaseSector attempts to decrypt the baseSector. If it has the necessary
// Skykey, it will decrypt the baseSector in-place. It returns the file-specific
// skykey to be used for decrypting the rest of the associated skyfile.
func (r *Renter) decryptBaseSector(baseSector []byte) (skykey.Skykey, error) {
	// Sanity check - baseSector should not be more than modules.SectorSize.
	// Note that the base sector may be smaller in the event of a packed
	// skyfile.
	if uint64(len(baseSector)) > modules.SectorSize {
		build.Critical("decryptBaseSector given a baseSector that is too large")
		return skykey.Skykey{}, errors.New("baseSector too large")
	}
	var sl skynet.SkyfileLayout
	sl.Decode(baseSector)

	if !isEncryptedLayout(sl) {
		build.Critical("Expected layout to be marked as encrypted!")
	}

	// Get the nonce to be used for getting private-id skykeys, and for deriving the
	// file-specific skykey.
	nonce := make([]byte, chacha.XNonceSize)
	copy(nonce[:], sl.KeyData[skykey.SkykeyIDLen:skykey.SkykeyIDLen+chacha.XNonceSize])

	// Grab the key ID from the layout.
	var keyID skykey.SkykeyID
	copy(keyID[:], sl.KeyData[:skykey.SkykeyIDLen])

	// Try to get the skykey associated with that ID.
	masterSkykey, err := r.staticSkykeyManager.KeyByID(keyID)
	// If the ID is unknown, use the key ID as an encryption identifier and try
	// finding the associated skykey.
	if errors.Contains(err, skykey.ErrNoSkykeysWithThatID) {
		masterSkykey, err = r.checkSkyfileEncryptionIDMatch(keyID[:], nonce)
	}
	if err != nil {
		return skykey.Skykey{}, errors.AddContext(err, "Unable to find associated skykey")
	}

	// Derive the file-specific key.
	fileSkykey, err := masterSkykey.SubkeyWithNonce(nonce)
	if err != nil {
		return skykey.Skykey{}, errors.AddContext(err, "Unable to derive file-specific subkey")
	}

	// Derive the base sector subkey and use it to decrypt the base sector.
	baseSectorKey, err := fileSkykey.DeriveSubkey(baseSectorNonceDerivation[:])
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
	copy(baseSector[:skynet.SkyfileLayoutSize], sl.Encode())

	return fileSkykey, nil
}

// encryptBaseSectorWithSkykey encrypts the baseSector in place using the given
// Skykey. Certain fields of the layout are restored in plaintext into the
// encrypted baseSector to indicate to downloaders what Skykey was used.
func encryptBaseSectorWithSkykey(baseSector []byte, plaintextLayout skynet.SkyfileLayout, sk skykey.Skykey) error {
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
	var encryptedLayout skynet.SkyfileLayout
	encryptedLayout.Decode(baseSector)
	encryptedLayout.Version = plaintextLayout.Version
	encryptedLayout.CipherType = baseSectorKey.CipherType()

	// Add the key ID or the encrypted skyfile identifier, depending on the key
	// type.
	switch sk.Type {
	case skykey.TypePublicID:
		keyID := sk.ID()
		copy(encryptedLayout.KeyData[:skykey.SkykeyIDLen], keyID[:])

	case skykey.TypePrivateID:
		encryptedIdentifier, err := sk.GenerateSkyfileEncryptionID()
		if err != nil {
			return errors.AddContext(err, "Unable to generate encrypted skyfile ID")
		}
		copy(encryptedLayout.KeyData[:skykey.SkykeyIDLen], encryptedIdentifier[:])

	default:
		build.Critical("No encryption implemented for this skykey type")
		return errors.AddContext(errors.New("No encryption implemented for skykey type"), string(sk.Type))
	}

	// Add the nonce to the base sector, in plaintext.
	nonce := sk.Nonce()
	copy(encryptedLayout.KeyData[skykey.SkykeyIDLen:skykey.SkykeyIDLen+len(nonce)], nonce[:])

	// Now re-copy the encrypted layout into the baseSector.
	copy(baseSector[:skynet.SkyfileLayoutSize], encryptedLayout.Encode())
	return nil
}

// isEncryptedBaseSector returns true if and only if the the baseSector is
// encrypted.
func isEncryptedBaseSector(baseSector []byte) bool {
	var sl skynet.SkyfileLayout
	sl.Decode(baseSector)
	return isEncryptedLayout(sl)
}

// isEncryptedLayout returns true if and only if the the layout indicates that
// it is from an encrypted base sector.
func isEncryptedLayout(sl skynet.SkyfileLayout) bool {
	return sl.Version == 1 && sl.CipherType == crypto.TypeXChaCha20
}

func encryptionEnabled(sup modules.SkyfileUploadParameters) bool {
	return sup.SkykeyName != "" || sup.SkykeyID != skykey.SkykeyID{}
}
