package renter

import (
	"encoding/base64"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
)

// TestSkyKeyManager tests the basic functionality of the skyKeyManager.
func TestSkyKeyManager(t *testing.T) {
	cipherType := crypto.TypeThreefish.String()
	keyMan, err := newSkyKeyManager(build.TempDir(t.Name()))
	if err != nil {
		t.Fatal(err)
	}

	skyKey, err := keyMan.CreateKeyGroup("test_group1", cipherType)
	if err != nil {
		t.Fatal(err)
	}

	_, err = keyMan.CreateKeyGroup("test_group1", cipherType)
	if !errors.Contains(err, errSkyKeyGroupAlreadyExists) {
		t.Fatal("Expected skykey group name to already exist", err)
	}

	// Check the correct Id is returned.
	ids, err := keyMan.GetIdsByName("test_group1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatal("Expected exactly 1 group key")
	}
	if ids[0] != skyKey.Id() {
		t.Fatal("Expected matching keyId")
	}

	// Check that getting a random, unused name returns the expected error.
	randomNameBytes := fastrand.Bytes(24)
	randomName := string(randomNameBytes)
	ids, err = keyMan.GetIdsByName(randomName)
	if err != errNoSkyKeysWithThatName {
		t.Fatal(err)
	}

	// Check that getting a random, unused id returns the expected error.
	randomIdBytes := fastrand.Bytes(24)
	randomId := string(randomIdBytes)
	_, err = keyMan.GetKeyById(randomId)
	if err != errNoSkyKeysWithThatId {
		t.Fatal(err)
	}

	// Check that calling AddKey on a non-existent group creates a new group.
	entropyBytes := fastrand.Bytes(64)
	skyKey2, err := keyMan.AddKey("test_group2", base64.URLEncoding.EncodeToString(entropyBytes), cipherType)
	if err != nil {
		t.Fatal(err)
	}
	if skyKey2.Id() == skyKey.Id() {
		t.Fatal("Expected different skykey to be created")
	}

	// Add more keys to the group.
	numKeysInGroup2 := 25
	for i := 1; i < numKeysInGroup2; i++ {
		newEntropyBytes := fastrand.Bytes(64)
		_, err := keyMan.AddKey("test_group2", base64.URLEncoding.EncodeToString(newEntropyBytes), cipherType)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Check uniqueness of key ids.
	secondIds, err := keyMan.GetIdsByName("test_group2")
	if err != nil {
		t.Fatal(err)
	}
	secondIdsMap := make(map[string]struct{})
	for _, id := range secondIds {
		secondIdsMap[id] = struct{}{}
	}
	if len(secondIdsMap) != numKeysInGroup2 || len(secondIds) != numKeysInGroup2 {
		t.Fatal("Expected numKeysInGroup2 keys unique key Ids")
	}

	// Getting keys by group name should return a matching result.
	secondGroupKeys, err := keyMan.GetKeysByName("test_group2")
	if err != nil {
		t.Fatal(err)
	}
	if len(secondGroupKeys) != numKeysInGroup2 {
		t.Fatal("Expected numKeysInGroup2 keys")
	}
	for _, key := range secondGroupKeys {
		_, foundId := secondIdsMap[key.Id()]
		if !foundId {
			t.Fatal("Expected key Id to be in secondIdsMap")
		}
	}
}
