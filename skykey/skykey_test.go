package skykey

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aead/chacha20/chacha"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/encoding"
)

// TestSkykeyManager tests the basic functionality of the skykeyManager.
func TestSkykeyManager(t *testing.T) {
	// Create a key manager.
	persistDir := build.TempDir("skykey", t.Name())
	keyMan, err := NewSkykeyManager(persistDir)
	if err != nil {
		t.Fatal(err)
	}

	// Check that the header values are set.
	if keyMan.staticVersion != skykeyVersion {
		t.Fatal("Expected version to be set")
	}
	if int(keyMan.fileLen) < headerLen {
		t.Fatal("Expected at file to be at least headerLen bytes")
	}

	// Creating a key with name longer than the max allowed should fail.
	var longName [MaxKeyNameLen + 1]byte
	for i := 0; i < len(longName); i++ {
		longName[i] = 0x41 // "A"
	}
	_, err = keyMan.CreateKey(string(longName[:]), TypePublicID)
	if !errors.Contains(err, errSkykeyNameToolong) {
		t.Fatal(err)
	}

	// Creating a key with name less than or equal to max len should be ok.
	_, err = keyMan.CreateKey(string(longName[:len(longName)-1]), TypePublicID)
	if err != nil {
		t.Fatal(err)
	}

	// Unsupported cipher types should cause an error.
	_, err = keyMan.CreateKey("test_key1", SkykeyType(0x00))
	if !errors.Contains(err, errUnsupportedSkykeyType) {
		t.Fatal(err)
	}
	_, err = keyMan.CreateKey("test_key1", SkykeyType(0xFF))
	if !errors.Contains(err, errUnsupportedSkykeyType) {
		t.Fatal(err)
	}

	skykey, err := keyMan.CreateKey("test_key1", TypePublicID)
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
		t.Log(skykey)
		t.Log(decodedSkykey)
		t.Fatal("Expected decoded skykey to be the same")
	}

	// Check duplicate name errors.
	_, err = keyMan.CreateKey("test_key1", TypePublicID)
	if !errors.Contains(err, ErrSkykeyWithNameAlreadyExists) {
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
	if !errors.Contains(err, ErrNoSkykeysWithThatName) {
		t.Fatal(err)
	}

	// Check that the correct error for a random unknown key is given.
	var randomID SkykeyID
	fastrand.Read(randomID[:])
	_, err = keyMan.KeyByID(randomID)
	if !errors.Contains(err, ErrNoSkykeysWithThatID) {
		t.Fatal(err)
	}

	// Create a second test key and check that it's different than the first.
	skykey2, err := keyMan.CreateKey("test_key2", TypePublicID)
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
	if keyMan2.staticVersion != skykeyVersion {
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
	newPersistDir := build.TempDir(t.Name(), "add-only-keyman")
	addKeyMan, err := NewSkykeyManager(newPersistDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range keyMan.keysByID {
		err := addKeyMan.AddKey(key)
		if err != nil {
			t.Fatal(err)
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
		err := addKeyMan.AddKey(key)
		if !errors.Contains(err, ErrSkykeyWithIDAlreadyExists) {
			t.Fatal(err)
		}
	}
}

// TestSkykeyDerivation tests skykey derivation methods used in skyfile
// encryption.
func TestSkykeyDerivations(t *testing.T) {
	// Create a key manager.
	persistDir := build.TempDir("skykey", t.Name())
	keyMan, err := NewSkykeyManager(persistDir)
	if err != nil {
		t.Fatal(err)
	}

	// Hard-code some expected values.
	skykey := Skykey{"derivation_test_key", TypePublicID, []byte{51, 90, 115, 73, 121, 179, 94, 117, 153, 74, 70, 80, 127, 55, 231, 196, 104, 244, 83, 157, 198, 159, 118, 79, 213, 32, 112, 255, 8, 84, 83, 183, 125, 30, 213, 34, 252, 152, 144, 42, 231, 151, 254, 145, 149, 205, 135, 169, 44, 185, 223, 52, 250, 126, 119, 249}}
	err = keyMan.AddKey(skykey)
	if err != nil {
		t.Fatal(err)
	}

	masterNonce := skykey.Nonce()

	derivationPath1 := []byte("derivationtest1")
	derivationPath2 := []byte("path2")

	// Derive a subkey and check that it matches saved values.
	dk1, err := skykey.DeriveSubkey(derivationPath1)
	if err != nil {
		t.Fatal(err)
	}
	expected := []byte{51, 90, 115, 73, 121, 179, 94, 117, 153, 74, 70, 80, 127, 55, 231, 196, 104, 244, 83, 157, 198, 159, 118, 79, 213, 32, 112, 255, 8, 84, 83, 183, 121, 171, 176, 232, 96, 47, 177, 154, 180, 144, 145, 29, 220, 178, 39, 220, 182, 53, 153, 191, 167, 116, 108, 221}
	if !bytes.Equal(dk1.Entropy, expected) {
		t.Fatal("unexpected subkey entropy")
	}
	if !bytes.Equal(dk1.Entropy[:chacha.KeySize], skykey.Entropy[:chacha.KeySize]) {
		t.Fatal("did not preserve key part of master key's entropy")
	}

	// Create file-specific keys.
	numDerivedSkykeys := 5
	derivedSkykeys := make([]Skykey, 0)
	for i := 0; i < numDerivedSkykeys; i++ {
		fsKey, err := skykey.GenerateFileSpecificSubkey()
		if err != nil {
			t.Fatal(err)
		}
		derivedSkykeys = append(derivedSkykeys, fsKey)

		// Further derive subkeys along the 2 test paths.
		dk1, err := fsKey.DeriveSubkey(derivationPath1)
		if err != nil {
			t.Fatal(err)
		}
		dk2, err := fsKey.DeriveSubkey(derivationPath2)
		if err != nil {
			t.Fatal(err)
		}
		derivedSkykeys = append(derivedSkykeys, dk1)
		derivedSkykeys = append(derivedSkykeys, dk2)
	}

	// Include all keys.
	numDerivedSkykeys *= 3

	// Check that all keys have the same Key data.
	for i := 0; i < numDerivedSkykeys; i++ {
		if !bytes.Equal(skykey.Entropy[:chacha.KeySize], derivedSkykeys[i].Entropy[:chacha.KeySize]) {
			t.Fatal("Expected each derived skykey to have the same key as the master skykey")
		}
		// Sanity check by checking ID equality also.
		if skykey.ID() != derivedSkykeys[i].ID() {
			t.Fatal("Expected each derived skykey to have the same ID as the master skykey")
		}
	}

	// Check that all nonces have a different nonce, and are not considered equal.
	for i := 0; i < numDerivedSkykeys; i++ {
		ithNonce := derivedSkykeys[i].Nonce()
		if bytes.Equal(ithNonce[:], masterNonce[:]) {
			t.Fatal("Expected nonce different from master nonce", i)
		}
		for j := i + 1; j < numDerivedSkykeys; j++ {
			jthNonce := derivedSkykeys[j].Nonce()
			if bytes.Equal(ithNonce[:], jthNonce[:]) {
				t.Fatal("Expected different nonces", ithNonce, jthNonce)
			}
			// Sanity check our definition of equals.
			if derivedSkykeys[i].equals(derivedSkykeys[j]) {
				t.Fatal("Expected skykey to be different", i, j)
			}
		}
	}
}

// TestSkykeyFormatCompat tests compatibility code for the old skykey format.
func TestSkykeyFormatCompat(t *testing.T) {
	badOldKeyString := "BAAAAAAAAABrZXkxAAAAAAAAAAQgAAAAAAAAADiObVg49-0juJ8udAx4qMW-TEHgDxfjA0fjJSNBuJ4a"
	oldKeyString := "CAAAAAAAAAB0ZXN0a2V5MQAAAAAAAAAEOAAAAAAAAADJfmSVAo2HGDfBpPrDr1CoqiqXAMYG9FaaHBwxKL6lNVEysSVY65et5zdFmwCMb7HibTE8LlRR5Q=="

	var oldSkykey compatSkykeyV148
	err := oldSkykey.fromString(badOldKeyString)
	if err == nil {
		t.Fatal("Expected error decoding incorrectly formatted old key")
	}

	err = oldSkykey.fromString(oldKeyString)
	if err != nil {
		t.Fatal(err)
	}
	if oldSkykey.name != "testkey1" {
		t.Fatal("Incorrect skykey name", oldSkykey.name)
	}
	if oldSkykey.ciphertype != crypto.TypeXChaCha20 {
		t.Fatal("Incorrect skykey name", oldSkykey.name)
	}

	// Sanity check: the skykey can be used to create a cipherkey still
	_, err = crypto.NewSiaKey(oldSkykey.ciphertype, oldSkykey.entropy)
	if err != nil {
		t.Log(len(oldSkykey.entropy))
		t.Fatal(err)
	}

	// Test a marshal and unmarshal of a new key.
	oldSkykey2 := compatSkykeyV148{
		name:       "oldkey2",
		ciphertype: crypto.TypeXChaCha20,
		entropy:    make([]byte, 56),
	}
	fastrand.Read(oldSkykey2.entropy)

	var buf bytes.Buffer
	err = oldSkykey2.marshalSia(&buf)
	if err != nil {
		t.Fatal(err)
	}

	var decodedOK2 compatSkykeyV148
	err = decodedOK2.unmarshalSia(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if decodedOK2.name != oldSkykey2.name {
		t.Fatal("Expected key names to match", decodedOK2.name)
	}
	if decodedOK2.ciphertype != oldSkykey2.ciphertype {
		t.Fatal("Expected key ciphertypes to match", decodedOK2.ciphertype)
	}
	if !bytes.Equal(decodedOK2.entropy, oldSkykey2.entropy) {
		t.Log(decodedOK2)
		t.Log(oldSkykey2)
		t.Fatal("Expected entropy to match")
	}

	// Write an old key to the buffer again.
	err = oldSkykey2.marshalSia(&buf)
	if err != nil {
		t.Fatal(err)
	}

	// Test conversion to updated key format.
	var sk Skykey
	err = sk.unmarshalAndConvertFromOldFormat(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if sk.Name != oldSkykey2.name {
		t.Fatal("Incorrect skykey name", sk.Name)
	}
	if sk.Type != TypePublicID {
		t.Fatal("Incorrect skykey name", sk.Type)
	}
	if sk.CipherType() != crypto.TypeXChaCha20 {
		t.Fatal("Incorrect skykey ciphertype", sk.CipherType())
	}
	if !bytes.Equal(sk.Entropy, oldSkykey2.entropy) {
		t.Log(sk)
		t.Log(oldSkykey)
		t.Fatal("Expected entropy to match")
	}
}

// TestSkykeyURIFormatting checks the ToString and FromString skykey methods
// that use URI formatting.
func TestSkykeyURIFormatting(t *testing.T) {
	testKeyName := "FormattingTestKey"
	keyDataString := "AT7-P751d_SEBhXvbOQTfswB62n2mqMe0Q89cQ911KGeuTIV2ci6GjG3Aj5CuVZUDS6hkG7pHXXZ"
	nameParam := "?name=" + testKeyName

	testStrings := []string{
		SkykeyScheme + ":" + keyDataString + nameParam, // skykey with scheme and name
		keyDataString + nameParam,                      // skykey with name and no scheme
		SkykeyScheme + ":" + keyDataString,             // skykey with scheme and no name
		keyDataString,                                  // skykey with no scheme and no name
	}
	skykeys := make([]Skykey, len(testStrings))

	// Check that we can load from string and recreate the input string from the
	// skykey.
	for i, testString := range testStrings {
		err := skykeys[i].FromString(testString)
		if err != nil {
			t.Fatal(err)
		}
		s, err := skykeys[i].ToString()
		if err != nil {
			t.Fatal(err)
		}

		// ToString should always output the "skykey:" scheme even if the input did
		// not.
		withScheme := strings.Contains(testString, SkykeyScheme)
		if withScheme && s != testString {
			t.Fatal("Expected string to match test string", i, s, testString)
		} else if !withScheme && s != SkykeyScheme+":"+testString {
			t.Fatal("Expected string to match test string", i, s, testString)
		}
	}

	// The first 2 keys should have names and the rest should not.
	for i, sk := range skykeys {
		if i <= 1 && sk.Name != testKeyName {
			t.Log(sk)
			t.Log("Expected testKeyName in skykey")
		}
		if i > 1 && sk.Name != "" {
			t.Log(sk)
			t.Log("Expected testKeyName in skykey")
		}
	}

	// All skykeys should have the same ID for each skykey.
	for i := 1; i < len(skykeys); i++ {
		if skykeys[i].ID() != skykeys[i-1].ID() {
			t.Fatal("Expected same ID", i)
		}
	}
}

// TestSkyeyMarshalling tests edges cases in marshalling and unmarshalling.
func TestSkykeyMarshalling(t *testing.T) {
	skykeyType := TypePublicID
	cipherKey := crypto.GenerateSiaKey(skykeyType.CipherType())
	skykey := Skykey{
		Type:    skykeyType,
		Entropy: cipherKey.Key(),
	}

	// marshal/unmarshal a good key.
	var buf bytes.Buffer
	err := skykey.marshalSia(&buf)
	if err != nil {
		t.Fatal(err)
	}
	sk := Skykey{}
	err = sk.unmarshalSia(&buf)
	if err != nil {
		t.Fatal(err)
	}

	// Add a name that is too long.
	for i := 0; i < MaxKeyNameLen+1; i++ {
		skykey.Name += "L"
	}
	buf.Reset()
	err = skykey.marshalSia(&buf)
	if err != nil {
		t.Fatal(err)
	}

	// Unmarshaling a Skykey with a long name should throw an error.
	sk = Skykey{}
	err = sk.unmarshalSia(&buf)
	if !errors.Contains(err, errSkykeyNameToolong) {
		t.Fatal("Expected error for long name", err)
	}
	// Forcefully marshal the skykey
	e := encoding.NewEncoder(&buf)
	e.WriteByte(byte(skykey.Type))
	e.Write(sk.Entropy[:])
	e.Encode(sk.Name)
	if err = e.Err(); err != nil {
		t.Fatal(err)
	}
	// Check for the unmarshal error.
	err = sk.unmarshalSia(&buf)
	if !errors.Contains(err, errSkykeyNameToolong) {
		t.Fatal("Expected error for trying to unmarshal skykey with a name that is too long", err)
	}

	// Fix the name length and use the (default) invalid type.
	skykey = Skykey{
		Name:    "a-reasonably-sized-name",
		Entropy: skykey.Entropy,
	}
	buf.Reset()
	err = skykey.marshalSia(&buf)
	err = sk.unmarshalSia(&buf)
	if !errors.Contains(err, errCannotMarshalTypeInvalidSkykey) {
		t.Fatal("Expected error for trying to marshal an invalid skykey type", err)
	}

	// Forcefully marshal a skykey with type invalid.
	e = encoding.NewEncoder(&buf)
	e.WriteByte(byte(TypeInvalid))
	e.Write(sk.Entropy[:])
	e.Encode(sk.Name)
	if err = e.Err(); err != nil {
		t.Fatal(err)
	}
	// Check for the unmarshal error.
	err = sk.unmarshalSia(&buf)
	if !errors.Contains(err, errCannotMarshalTypeInvalidSkykey) {
		t.Fatal("Expected error for trying to unmarshal an invalid skykey type", err)
	}

	// Use an unknown type and check for the marshal error.
	skykey.Type = SkykeyType(0xF0)
	buf.Reset()
	err = skykey.marshalSia(&buf)
	if !errors.Contains(err, errUnsupportedSkykeyType) {
		t.Fatal("Expected error for trying to marshal an unknown skykey type", err)
	}

	// Forcefully marshal a bad skykey.
	buf.Reset()
	e = encoding.NewEncoder(&buf)
	e.WriteByte(0xF0)
	e.Write(sk.Entropy[:])
	e.Encode(sk.Name)
	if err = e.Err(); err != nil {
		t.Fatal(err)
	}
	// Check for the unmarshal error.
	err = sk.unmarshalSia(&buf)
	if !errors.Contains(err, errUnsupportedSkykeyType) {
		t.Fatal("Expected error for trying to unmarshal an unknown skykey type", err)
	}

	// Create a skykey with small Entropy slice.
	skykey = Skykey{
		Name:    "aname",
		Type:    TypePublicID,
		Entropy: make([]byte, 5),
	}
	buf.Reset()
	err = skykey.marshalSia(&buf)
	if !errors.Contains(err, errInvalidEntropyLength) {
		t.Fatal(err)
	}

	// Forcefully marshal a bad skykey.
	buf.Reset()
	e = encoding.NewEncoder(&buf)
	e.WriteByte(byte(skykey.Type))
	e.Write(skykey.Entropy[:])
	e.Encode(skykey.Name)
	if err = e.Err(); err != nil {
		t.Fatal(err)
	}
	// Check for the unmarshal error.
	err = sk.unmarshalSia(&buf)
	if !errors.Contains(err, errUnmarshalDataErr) {
		t.Fatal("Expected error for trying to unmarshal skykey with small Entropy slice", err)
	}

	// Create a skykey with too large of an Entropy slice.
	skykey = Skykey{
		Name:    "aname",
		Type:    TypePublicID,
		Entropy: make([]byte, 500),
	}
	buf.Reset()
	err = skykey.marshalSia(&buf)
	if !errors.Contains(err, errInvalidEntropyLength) {
		t.Fatal(err)
	}

	// Forcefully marshal a bad skykey.
	buf.Reset()
	e = encoding.NewEncoder(&buf)
	e.WriteByte(byte(skykey.Type))
	e.Write(skykey.Entropy[:])
	e.Encode(skykey.Name)
	if err = e.Err(); err != nil {
		t.Fatal(err)
	}
	// There should be no error, because we only try to unmarshal the correct
	// (smaller) number of bytes.
	err = sk.unmarshalSia(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if sk.Type != TypePublicID {
		t.Fatal("Expected correct skykey type")
	}
	if sk.Name != "" {
		t.Fatal("Expected no skykey name")
	}
	if len(sk.Entropy) != chacha.XNonceSize+chacha.KeySize {
		t.Fatal("Expected entropy with correct size.")
	}

	// Unmarshaling a Skykey with a long name should throw an error.
	sk = Skykey{}
	err = sk.unmarshalSia(&buf)
	// Try unmarshalling small random byte slices.
	for i := 0; i < 10; i++ {
		buf.Reset()
		buf.Write(fastrand.Bytes(fastrand.Intn(20)))
		sk = Skykey{}

		err = sk.unmarshalSia(&buf)
		if err == nil {
			t.Log(buf)
			t.Log(sk)
			t.Fatal("Expected random byte unmarshaling to fail")
		}
	}

	// Try unmarshalling larger random byte slices.
	for i := 0; i < 10; i++ {
		buf.Reset()
		buf.Write(fastrand.Bytes(100 * fastrand.Intn(20)))
		sk = Skykey{}

		err = sk.unmarshalSia(&buf)
		if err == nil {
			t.Log(buf)
			t.Log(sk)
			t.Fatal("Expected random byte unmarshaling to fail")
		}
	}
}

// TestSkykeyTypeStrings tests FromString and ToString methods for SkykeyTypes
func TestSkykeyTypeStrings(t *testing.T) {
	publicIDString := TypePublicID.ToString()
	if publicIDString != "public-id" {
		t.Fatal("Incorrect skykeytype name", publicIDString)
	}

	var st SkykeyType
	err := st.FromString(publicIDString)
	if err != nil {
		t.Fatal(err)
	}
	if st != TypePublicID {
		t.Fatal("Wrong SkykeyType", st)
	}

	invalidTypeString := TypeInvalid.ToString()
	if invalidTypeString != "invalid" {
		t.Fatal("Incorrect skykeytype name", invalidTypeString)
	}

	var invalidSt SkykeyType
	err = invalidSt.FromString(invalidTypeString)
	if !errors.Contains(err, ErrInvalidSkykeyType) {
		t.Fatal(err)
	}

	privateIDString := TypePrivateID.ToString()
	if privateIDString != "private-id" {
		t.Fatal("Incorrect skykeytype name", privateIDString)
	}

	err = st.FromString(privateIDString)
	if err != nil {
		t.Fatal(err)
	}
	if st != TypePrivateID {
		t.Fatal("Wrong SkykeyType", st)
	}
}

// TestSkyfileEncryptionIDs tests the generation and verification of skyfile
// encryption IDs.
func TestSkyfileEncryptionIDs(t *testing.T) {
	// Create a key manager.
	persistDir := build.TempDir("skykey", t.Name())
	keyMan, err := NewSkykeyManager(persistDir)
	if err != nil {
		t.Fatal(err)
	}

	pubSkykey, err := keyMan.CreateKey("public_id"+t.Name(), TypePublicID)
	if err != nil {
		t.Fatal(err)
	}
	pubFsKey, err := pubSkykey.GenerateFileSpecificSubkey()
	if err != nil {
		t.Fatal(err)
	}
	// We should not be able to generate encryption IDs with a TypePublicID key.
	_, err = pubFsKey.GenerateSkyfileEncryptionID()
	if !errors.Contains(err, errSkykeyTypeDoesNotSupportFunction) {
		t.Fatal(err)
	}

	// Create a private-id skykey.
	privSkykey, err := keyMan.CreateKey("private_id"+t.Name(), TypePrivateID)
	if err != nil {
		t.Fatal(err)
	}

	// Check that different file-specific keys make different encryption IDs.
	nEncIDs := 10
	encIDSet := make(map[[SkykeyIDLen]byte]struct{})
	encIDs := make([][SkykeyIDLen]byte, nEncIDs)
	nonces := make([][]byte, nEncIDs)
	for i := 0; i < nEncIDs; i++ {
		nextFsKey, err := privSkykey.GenerateFileSpecificSubkey()
		if err != nil {
			t.Fatal(err)
		}
		encID, err := nextFsKey.GenerateSkyfileEncryptionID()
		if _, ok := encIDSet[encID]; ok {
			t.Log(i, encID)
			t.Fatal("Found encID in set of existing encIDs!")
		}
		encIDSet[encID] = struct{}{}

		// Save the nonce and encID in slice for next part of test.
		nonces[i] = nextFsKey.Nonce()
		encIDs[i] = encID
	}

	// Create more private-id skykey to make sure that they don't match any
	// encID/nonce pair.
	nPrivIDKeys := 10
	privIDKeys := make([]Skykey, nPrivIDKeys)
	privIDKeys[0] = privSkykey
	for i := 0; i < nPrivIDKeys-1; i++ {
		privIDKeys[i+1], err = keyMan.CreateKey("private_id"+t.Name()+fmt.Sprint(i+1), TypePrivateID)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Test MatchesSkyfileEncryptionID. Unrelated encID/nonce pairs should never
	// match. Unrelated keys should also not show a match.
	for i, nonce := range nonces {
		for j, encID := range encIDs {
			// TypePublicID keys should never produce a match.
			matches, err := pubSkykey.MatchesSkyfileEncryptionID(encID[:], nonce)
			if matches {
				t.Fatal("public-id Skykey matches encryption ID")
			}
			if err != nil {
				t.Fatal(err)
			}

			// Unrelated private-id keys should never match.
			for i := 1; i < nPrivIDKeys; i++ {
				matches, err := privIDKeys[i].MatchesSkyfileEncryptionID(encID[:], nonce)
				if err != nil {
					t.Fatal(err)
				}
				if matches {
					t.Fatal("wrong  Skykey matches encryption ID")
				}
			}

			// The original private-id skykey should match only when i == j.
			matches, err = privIDKeys[0].MatchesSkyfileEncryptionID(encID[:], nonce)
			if err != nil {
				t.Fatal(err)
			}
			if matches != (i == j) {
				t.Fatalf("Bad encID, nonce pair matched, or correct pair did not match, i: %d, j: %d", i, j)
			}
		}
	}

	// Invalid id/nonce lengths should fail.
	_, err = privIDKeys[0].MatchesSkyfileEncryptionID(encIDs[0][:SkykeyIDLen-1], nonces[0])
	if !errors.Contains(err, errInvalidIDorNonceLength) {
		t.Fatal(err)
	}
	_, err = privIDKeys[0].MatchesSkyfileEncryptionID(encIDs[0][:], nonces[0][:chacha.XNonceSize-1])
	if !errors.Contains(err, errInvalidIDorNonceLength) {
		t.Fatal(err)
	}
	_, err = privIDKeys[0].MatchesSkyfileEncryptionID(encIDs[0][:SkykeyIDLen-1], nonces[0][:chacha.XNonceSize-1])
	if !errors.Contains(err, errInvalidIDorNonceLength) {
		t.Fatal(err)
	}
}

// TestSkykeyDelete tests the Delete methods for the skykey manager.
func TestSkykeyDelete(t *testing.T) {
	// Create a key manager.
	persistDir := build.TempDir("skykey", t.Name())
	keyMan, err := NewSkykeyManager(persistDir)
	if err != nil {
		t.Fatal(err)
	}

	// Add several keys and delete them all.
	keys := make([]Skykey, 0)
	for i := 0; i < 5; i++ {
		sk, err := keyMan.CreateKey("keys-to-delete"+fmt.Sprint(i), TypePrivateID)
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, sk)
	}
	for _, key := range keys {
		err = keyMan.DeleteKeyByID(key.ID())
		if err != nil {
			t.Fatal(err)
		}
	}

	// Check that keyMan doesn't recognize them anymore.
	for _, key := range keys {
		_, err = keyMan.KeyByID(key.ID())
		if !errors.Contains(err, ErrNoSkykeysWithThatID) {
			t.Fatal(err)
		}
	}

	// checkForExpectedKeys checks that the keys in expectedKeySet are the only
	// ones stored by keyMan, and also checks that a new keyManager loaded from
	// the same persist also stores only this exact set of skykeys.
	checkForExpectedKeys := func(expectedKeySet map[SkykeyID]struct{}) {
		allSkykeys := keyMan.Skykeys()
		if len(allSkykeys) != len(expectedKeySet) {
			t.Fatalf("Expected %d keys, got %d", len(expectedKeySet), len(allSkykeys))
		}
		for _, sk := range allSkykeys {
			_, ok := expectedKeySet[sk.ID()]
			if !ok {
				t.Fatal("Did not find key in expected key set")
			}
		}

		freshKeyMan, err := NewSkykeyManager(persistDir)
		if err != nil {
			t.Fatal(err)
		}

		loadedSkykeys := freshKeyMan.Skykeys()
		if len(loadedSkykeys) != len(expectedKeySet) {
			t.Fatalf("Fresh load: Expected %d keys, got %d", len(expectedKeySet), len(loadedSkykeys))
		}

		for _, sk := range loadedSkykeys {
			_, ok := expectedKeySet[sk.ID()]
			if !ok {
				t.Fatal("Fresh load: Did not find key in expected key set")
			}
		}
	}

	// There should be no keys remaining.
	checkForExpectedKeys(make(map[SkykeyID]struct{}))

	// Add a bunch of keys again.
	expectedKeySet := make(map[SkykeyID]struct{})
	nKeys := 10
	keys = make([]Skykey, 0)
	for i := 0; i < nKeys; i++ {
		sk, err := keyMan.CreateKey("key"+fmt.Sprint(i), TypePrivateID)
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, sk)
		expectedKeySet[sk.ID()] = struct{}{}
	}
	checkForExpectedKeys(expectedKeySet)

	// Delete the first key.
	err = keyMan.DeleteKeyByID(keys[0].ID())
	if err != nil {
		t.Fatal(err)
	}
	delete(expectedKeySet, keys[0].ID())

	// Check keyManager deletion.
	checkForExpectedKeys(expectedKeySet)

	// Delete a middle key and do the same checks.
	midIdx := nKeys / 2
	err = keyMan.DeleteKeyByID(keys[midIdx].ID())
	if err != nil {
		t.Fatal(err)
	}
	delete(expectedKeySet, keys[midIdx].ID())
	checkForExpectedKeys(expectedKeySet)

	// Delete the last key and do the same checks.
	endIdx := nKeys - 1
	err = keyMan.DeleteKeyByID(keys[endIdx].ID())
	if err != nil {
		t.Fatal(err)
	}
	delete(expectedKeySet, keys[endIdx].ID())
	checkForExpectedKeys(expectedKeySet)

	// Add a few more keys.
	for i := 0; i < nKeys; i++ {
		sk, err := keyMan.CreateKey("extra-key"+fmt.Sprint(i), TypePrivateID)
		if err != nil {
			t.Fatal(err)
		}
		expectedKeySet[sk.ID()] = struct{}{}
	}
	checkForExpectedKeys(expectedKeySet)

	// Sanity check on DeleteKeyByName by deleting some of the new keys.
	for i := 0; i < len(expectedKeySet)/2; i += 2 {
		sk, err := keyMan.KeyByName("extra-key" + fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
		err = keyMan.DeleteKeyByName(sk.Name)
		if err != nil {
			t.Fatal(err)
		}

		delete(expectedKeySet, sk.ID())
	}
	checkForExpectedKeys(expectedKeySet)
}

// TestSkykeyDelete tests the Delete methods for the skykey manager, starting
// with a file containing skykeys created using the older format.
func TestSkykeyDeleteCompat(t *testing.T) {
	// Create a persist dir.
	persistDir := build.TempDir("skykey", t.Name())
	err := os.MkdirAll(persistDir, defaultDirPerm)
	if err != nil {
		t.Fatal(err)
	}

	// copy the testdata file over to it.
	persistFileName := filepath.Join(persistDir, SkykeyPersistFilename)
	persistFile, err := os.Create(persistFileName)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := persistFile.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	testDataFileName := filepath.Join("testdata", "v144_and_v149_skykeys.dat")
	testDataFile, err := os.Open(testDataFileName)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := testDataFile.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	_, err = io.Copy(persistFile, testDataFile)

	if err != nil {
		t.Fatal(err)
	}

	// Create a key manager.
	keyMan, err := NewSkykeyManager(persistDir)
	if err != nil {
		t.Fatal(err)
	}

	nKeys := 8
	keys := keyMan.Skykeys()
	if len(keys) != nKeys {
		t.Fatalf("Expected %d keys got %d", nKeys, len(keys))
	}

	// Delete all the keys.
	for i, sk := range keys {
		err = keyMan.DeleteKeyByName(sk.Name)
		if err != nil {
			t.Fatal(err)
		}

		if len(keyMan.Skykeys()) != nKeys-(i+1) {
			t.Fatalf("Expected %d keys got %d", len(keyMan.Skykeys()), nKeys-(i+1))
		}
	}

	// Sanity check: create a new key and check for it.
	sk, err := keyMan.CreateKey("sanity-check", TypePrivateID)
	if err != nil {
		t.Fatal(err)
	}
	keys = keyMan.Skykeys()
	if len(keys) != 1 {
		t.Fatal("Expected 1 key", keys)
	}
	if !keys[0].equals(sk) {
		t.Fatal("keys don't match")
	}

	// Sanity check: check that the new key is loaded from a fresh persist.
	freshKeyMan, err := NewSkykeyManager(persistDir)
	if err != nil {
		t.Fatal(err)
	}
	loadedSkykeys := freshKeyMan.Skykeys()
	if len(loadedSkykeys) != 1 {
		t.Fatal("Expected 1 key", keys)
	}
	if !loadedSkykeys[0].equals(sk) {
		t.Fatal("keys don't match")
	}
}
