package skykey

import (
	"bytes"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
)

// TestSkykeyManager tests the basic functionality of the skykeyManager.
func TestSkykeyManager(t *testing.T) {
	// Create a key manager.
	persistDir := build.TempDir(t.Name())
	keyMan, err := NewSkykeyManager(persistDir)
	if err != nil {
		t.Fatal(err)
	}

	// Check that the header values are set.
	if keyMan.version != skykeyVersion {
		t.Fatal("Expected version to be set")
	}
	if int(keyMan.fileLen) < headerLen {
		t.Fatal("Expected at file to be at least headerLen bytes")
	}

	// Creating a key with name longer than the max allowed should fail.
	cipherType := crypto.TypeXChaCha20
	var longName [MaxKeyNameLen + 1]byte
	for i := 0; i < len(longName); i++ {
		longName[i] = 0x41 // "A"
	}
	_, err = keyMan.CreateKey(string(longName[:]), cipherType)
	if !errors.Contains(err, errSkykeyNameToolong) {
		t.Fatal(err)
	}

	// Creating a key with name less than or equal to max len should be ok.
	_, err = keyMan.CreateKey(string(longName[:len(longName)-1]), cipherType)
	if err != nil {
		t.Fatal(err)
	}

	// Unsupported cipher types should cause an error.
	_, err = keyMan.CreateKey("test_key1", crypto.TypeTwofish)
	if !errors.Contains(err, errUnsupportedSkykeyCipherType) {
		t.Fatal(err)
	}

	skykey, err := keyMan.CreateKey("test_key1", cipherType)
	if err != nil {
		t.Fatal(err)
	}

	// Simple encoding/decoding test.
	var buf bytes.Buffer
	err = skykey.marshalSia(&buf)
	if err != nil {
		t.Fatal(err)
	}

	var decodedSkykey Skykey
	err = decodedSkykey.unmarshalSia(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !decodedSkykey.equals(skykey) {
		t.Fatal("Expected decoded skykey to be the same")
	}

	// Check duplicate name errors.
	_, err = keyMan.CreateKey("test_key1", cipherType)
	if !errors.Contains(err, errSkykeyNameAlreadyExists) {
		t.Fatal("Expected skykey name to already exist", err)
	}

	// Check the correct ID is returned.
	id, err := keyMan.IDByName("test_key1")
	if err != nil {
		t.Fatal(err)
	}
	if id != skykey.ID() {
		t.Fatal("Expected matching keyID")
	}

	// Check that the correct error for a random unknown key is given.
	randomNameBytes := fastrand.Bytes(24)
	randomName := string(randomNameBytes)
	id, err = keyMan.IDByName(randomName)
	if err != errNoSkykeysWithThatName {
		t.Fatal(err)
	}

	// Check that the correct error for a random unknown key is given.
	var randomID SkykeyID
	fastrand.Read(randomID[:])
	_, err = keyMan.KeyByID(randomID)
	if err != errNoSkykeysWithThatID {
		t.Fatal(err)
	}

	// Create a second test key and check that it's different than the first.
	skykey2, err := keyMan.CreateKey("test_key2", cipherType)
	if err != nil {
		t.Fatal(err)
	}
	if skykey2.equals(skykey) {
		t.Fatal("Expected different skykey to be created")
	}
	if len(keyMan.keysByID) != 3 {
		t.Fatal("Wrong number of keys", len(keyMan.keysByID))
	}
	if len(keyMan.idsByName) != 3 {
		t.Fatal("Wrong number of keys", len(keyMan.idsByName))
	}

	// Check KeyByName returns the keys with the expected ID.
	key1Copy, err := keyMan.KeyByName("test_key1")
	if err != nil {
		t.Fatal(err)
	}
	if !key1Copy.equals(skykey) {
		t.Fatal("Expected key ID to match")
	}

	key2Copy, err := keyMan.KeyByName("test_key2")
	if err != nil {
		t.Fatal(err)
	}
	if !key2Copy.equals(skykey2) {
		t.Fatal("Expected key ID to match")
	}
	fileLen := keyMan.fileLen

	// Load a new keymanager from the same persistDir.
	keyMan2, err := NewSkykeyManager(persistDir)
	if err != nil {
		t.Fatal(err)
	}

	// Check that the header values are set.
	if keyMan2.version != skykeyVersion {
		t.Fatal("Expected version to be set")
	}
	if keyMan2.fileLen != fileLen {
		t.Fatal("Expected file len to match previous keyMan", fileLen, keyMan2.fileLen)
	}

	if len(keyMan.keysByID) != len(keyMan2.keysByID) {
		t.Fatal("Expected same number of keys")
	}
	for id, key := range keyMan.keysByID {
		if !key.equals(keyMan2.keysByID[id]) {
			t.Fatal("Expected same keys")
		}
	}

	// Check that AddKey works properly by re-adding all the keys from the first
	// 2 key managers into a new one.
	persistDir = build.TempDir(t.Name(), "add-only-keyman")
	addKeyMan, err := NewSkykeyManager(persistDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range keyMan.keysByID {
		addedKey, err := addKeyMan.AddKey(key.Name, key.CipherType, key.Entropy)
		if err != nil {
			t.Fatal(err)
		}
		if !addedKey.equals(key) {
			t.Fatal("Expected keys to be equal")
		}
	}

	// Check for the correct number of keys.
	if len(addKeyMan.keysByID) != 3 {
		t.Fatal("Wrong number of keys", len(addKeyMan.keysByID))
	}
	if len(addKeyMan.idsByName) != 3 {
		t.Fatal("Wrong number of keys", len(addKeyMan.idsByName))
	}

	// Try re-adding the same keys, and check that the duplicate name error is
	// shown.
	for _, key := range keyMan.keysByID {
		_, err := addKeyMan.AddKey(key.Name, key.CipherType, key.Entropy)
		if !errors.Contains(err, errSkykeyNameAlreadyExists) {
			t.Fatal(err)
		}
	}
}
