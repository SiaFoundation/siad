package renter

import (
	"bytes"
	"os"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/fastrand"
)

// TestSkyfileBaseSectorEncryption
func TestSkyfileBaseSectorEncryption(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	r := rt.renter
	defer rt.Close()

	// Create the 2 test skykeys.
	keyName1 := t.Name() + "1"
	sk1, err := r.CreateSkykey(keyName1, crypto.TypeXChaCha20)
	if err != nil {
		t.Fatal(err)
	}

	// Create a file that fits in one base sector and set it up for encryption.
	fileBytes := fastrand.Bytes(1000)
	metadata := modules.SkyfileMetadata{
		Mode:     os.FileMode(0777),
		Filename: "encryption_test_file",
	}
	// Grab the metadata bytes.
	metadataBytes, err := skyfileMetadataBytes(metadata)
	if err != nil {
		t.Fatal(err)
	}
	ll := skyfileLayout{
		version:      SkyfileVersion,
		filesize:     uint64(len(fileBytes)),
		metadataSize: uint64(len(metadataBytes)),
		cipherType:   crypto.TypePlain,
	}
	baseSector, _ := skyfileBuildBaseSector(ll.encode(), nil, metadataBytes, fileBytes) // 'nil' because there is no fanout

	// Make a helper function for producing copies of the basesector
	// because encryption is done in-place.
	baseSectorCopy := func() []byte {
		bsCopy := make([]byte, len(baseSector))
		copy(bsCopy[:], baseSector[:])
		return bsCopy
	}

	fsKey1, err := sk1.GenerateFileSpecificSubkey()
	if err != nil {
		t.Fatal(err)
	}

	// Encryption of the same base sector with the same key should yield the same
	// result, and it should be different from the plaintext.
	bsCopy1 := baseSectorCopy()
	bsCopy2 := baseSectorCopy()
	err = encryptBaseSectorWithSkykey(bsCopy1, ll, fsKey1)
	if err != nil {
		t.Fatal(err)
	}
	err = encryptBaseSectorWithSkykey(bsCopy2, ll, fsKey1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bsCopy1, bsCopy2) {
		t.Fatal("Expected encrypted basesector copies to be equal")
	}
	if bytes.Equal(baseSector, bsCopy2) {
		t.Fatal("Expected encrypted basesector copy to be different from original base sector")
	}

	// Create a different file-specific key. The encrypted basesector should be
	// different.
	fsKey2, err := sk1.GenerateFileSpecificSubkey()
	if err != nil {
		t.Fatal(err)
	}
	bsCopy3 := baseSectorCopy()
	err = encryptBaseSectorWithSkykey(bsCopy3, ll, fsKey2)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(baseSector, bsCopy3) {
		t.Fatal("Expected encrypted basesector copy to be different from original base sector")
	}
	if bytes.Equal(bsCopy2, bsCopy3) {
		t.Fatal("Basesectors encrypted with different file-specific keys should be different.")
	}

	// Create a entirely different skykey and sanity check that it produces
	// different ciphertexts.
	keyName2 := t.Name() + "2"
	sk2, err := r.CreateSkykey(keyName2, crypto.TypeXChaCha20)
	if err != nil {
		t.Fatal(err)
	}
	otherFSKey, err := sk2.GenerateFileSpecificSubkey()
	if err != nil {
		t.Fatal(err)
	}
	otherBSCopy := baseSectorCopy()
	err = encryptBaseSectorWithSkykey(otherBSCopy, ll, otherFSKey)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(otherBSCopy, baseSector) {
		t.Fatal("Expected base sector encrypted with different skykey to be different from original base sector.")
	}
	if bytes.Equal(otherBSCopy, bsCopy1) {
		t.Fatal("Expected base sector encrypted with different skykey to be differen from original base sector.")
	}
	if bytes.Equal(otherBSCopy, bsCopy3) {
		t.Fatal("Expected base sector encrypted with different skykey to be different from original base sector.")
	}

	// Now decrypt all the base sectors. They should all be equal to the original
	// now.
	err = r.decryptBaseSector(bsCopy1)
	if err != nil {
		t.Fatal(err)
	}
	err = r.decryptBaseSector(bsCopy2)
	if err != nil {
		t.Fatal(err)
	}
	err = r.decryptBaseSector(bsCopy3)
	if err != nil {
		t.Fatal(err)
	}
	err = r.decryptBaseSector(otherBSCopy)
	if err != nil {
		t.Fatal(err)
	}

	// All baseSectors should be equal in everything except their keydata.
	equalExceptKeyData := func(x, y []byte) error {
		xLayout, xFanoutBytes, xSM, xPayload, err := parseSkyfileMetadata(x)
		if err != nil {
			return err
		}
		yLayout, yFanoutBytes, ySM, yPayload, err := parseSkyfileMetadata(y)
		if err != nil {
			return err
		}

		// Check layout equality.
		if xLayout.version != yLayout.version {
			return errors.New("Expected version to match")
		}
		if xLayout.filesize != yLayout.filesize {
			return errors.New("Expected filesizes to match")
		}
		if xLayout.metadataSize != yLayout.metadataSize {
			return errors.New("Expected metadatasizes to match")
		}
		if xLayout.fanoutSize != yLayout.fanoutSize {
			return errors.New("Expected fanoutsize to match")
		}
		if xLayout.fanoutDataPieces != yLayout.fanoutDataPieces {
			return errors.New("Expected fanoutDataPieces to match")
		}
		if xLayout.fanoutParityPieces != yLayout.fanoutParityPieces {
			return errors.New("Expected fanoutParityPieces to match")
		}
		// (Key data and cipher type won't match because the unencrypted baseSector won't have any key
		// data)

		if !bytes.Equal(xFanoutBytes, yFanoutBytes) {
			return errors.New("Expected fanoutBytes to match")
		}

		// Check that xSM and ySM both have the original Mode/Filename.
		if xSM.Mode != metadata.Mode {
			return errors.New("x Mode doesn't match original")
		}
		if ySM.Mode != metadata.Mode {
			return errors.New("y Mode doesn't match original")
		}
		if xSM.Filename != metadata.Filename {
			return errors.New("x filename doesn't match original")
		}
		if ySM.Filename != metadata.Filename {
			return errors.New("y filename doesn't match original")
		}

		if !bytes.Equal(xPayload, yPayload) {
			return errors.New("Expected x and y payload to match")
		}
		return nil
	}

	// Base sector 1 and 2 should be *exactly* equal.
	// They used the exact same key throughout.
	if !bytes.Equal(bsCopy1, bsCopy2) {
		t.Fatal("Expected decrypted basesector copies to be equal")
	}

	// Check (almost) equality.
	err = equalExceptKeyData(baseSector, bsCopy1)
	if err != nil {
		t.Fatal(err)
	}
	err = equalExceptKeyData(bsCopy1, bsCopy3)
	if err != nil {
		t.Fatal(err)
	}
	err = equalExceptKeyData(bsCopy1, otherBSCopy)
	if err != nil {
		t.Fatal(err)
	}

	// bsCopy3 should not be exactly equal to bsCopy2 because of its different keyData.
	if bytes.Equal(bsCopy3, bsCopy2) {
		t.Fatal("Expected copies with different file-specific keys to be different")
	}
	// the original will also be different because it has no keydata.
	if bytes.Equal(baseSector, bsCopy2) {
		t.Fatal("Expected copies with different file-specific keys to be different")
	}
	// the original will also be different because it has no keydata.
	if bytes.Equal(baseSector, bsCopy3) {
		t.Fatal("Expected copies with different file-specific keys to be different")
	}
	// the original will also be different because it has no keydata.
	if bytes.Equal(baseSector, otherBSCopy) {
		t.Fatal("Expected copies with different file-specific keys to be different")
	}

	// Testing fanout key derivation.
	layoutForFanout, _, _, _, err := parseSkyfileMetadata(bsCopy1)
	if err != nil {
		t.Fatal(err)
	}
	fanoutKey, err := r.deriveFanoutKey(&layoutForFanout)
	if err != nil {
		t.Fatal(err)
	}
	fanoutKeyEntropy := fanoutKey.Key()

	// Check that deriveFanoutKey produces the same derived key as a manual
	// derivation from the original.The fact that it is different fsKey1 is
	// guaranteed by skykey module tests.
	fanoutKey2, err := fsKey1.DeriveSubkey(fanoutNonceDerivation[:])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fanoutKey2.Entropy[:], fanoutKeyEntropy[:]) {
		t.Fatal("Expected fanout key returned from deriveFanoutKey to be same as manual derivation")
	}
}
