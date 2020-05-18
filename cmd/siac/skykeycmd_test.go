package main

import (
	"fmt"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/skykey"
)

// TestSkykeyCommands tests the basic functionality of the siac skykey commands
// interface. More detailed testing of the skykey manager is done in the skykey
// package.
func TestSkykeyCommands(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create a node for the test
	n, err := siatest.NewNode(node.AllModules(build.TempDir(t.Name())))
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()

	// Set the (global) cipher type to the only allowed type.
	// This is normally done by the flag parser.
	skykeyCipherType = "XChaCha20"

	testSkykeyString := "BAAAAAAAAABrZXkxAAAAAAAAAAQgAAAAAAAAADiObVg49-0juJ8udAx4qMW-TEHgDxfjA0fjJSNBuJ4a"
	err = skykeyAdd(n.Client, testSkykeyString)
	if err != nil {
		t.Fatal(err)
	}

	err = skykeyAdd(n.Client, testSkykeyString)
	if !strings.Contains(err.Error(), skykey.ErrSkykeyWithIDAlreadyExists.Error()) {
		t.Fatal("Unexpected duplicate name error", err)
	}

	// Change the key entropy, but keep the same name.
	var sk skykey.Skykey
	err = sk.FromString(testSkykeyString)
	if err != nil {
		t.Fatal(err)
	}
	sk.Entropy[0] ^= 1 // flip the first byte.

	skString, err := sk.ToString()
	if err != nil {
		t.Fatal(err)
	}

	// This should return a duplicate name error.
	err = skykeyAdd(n.Client, skString)
	if !strings.Contains(err.Error(), skykey.ErrSkykeyWithNameAlreadyExists.Error()) {
		t.Fatal("Expected duplicate name error", err)
	}

	// Check that adding same key twice returns an error.
	keyName := "createkeyTest!"
	newSkykey, err := skykeyCreate(n.Client, keyName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = skykeyCreate(n.Client, keyName)
	if !strings.Contains(err.Error(), skykey.ErrSkykeyWithNameAlreadyExists.Error()) {
		t.Fatal("Expected error when creating key with same name")
	}

	// Check that invalid cipher types are caught.
	skykeyCipherType = "InvalidCipherType"
	_, err = skykeyCreate(n.Client, "createkey2")
	if !errors.Contains(err, crypto.ErrInvalidCipherType) {
		t.Fatal("Expected error when creating key with invalid ciphertype")
	}
	skykeyCipherType = "XChaCha20" //reset the ciphertype

	// Test skykeyGet
	// known key should have no errors.
	getKeyStr, err := skykeyGet(n.Client, keyName, "")
	if err != nil {
		t.Fatal(err)
	}

	if getKeyStr != newSkykey {
		t.Fatal("Expected keys to match")
	}

	// Using both name and id params should return an error
	_, err = skykeyGet(n.Client, "name", "id")
	if err == nil {
		t.Fatal("Expected error when using both name and id")
	}
	// Using neither name or id param should return an error
	_, err = skykeyGet(n.Client, "", "")
	if err == nil {
		t.Fatal("Expected error when using neither name or id params")
	}

	// Do some basic sanity checks on skykeyListKeys.
	nKeys := 2
	nExtraLines := 2
	keyStrings := make([]string, nKeys)
	keyNames := make([]string, nKeys)

	keyNames[0] = "key1"
	keyNames[1] = "createkeyTest!"
	keyStrings[0] = testSkykeyString
	keyStrings[1] = getKeyStr

	keyListString, err := skykeyListKeys(n.Client, true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < nKeys; i++ {
		if !strings.Contains(keyListString, keyNames[i]) {
			t.Log(keyListString)
			t.Fatal("Missing key name!", i)
		}
		if !strings.Contains(keyListString, keyStrings[i]) {
			t.Log(keyListString)
			t.Fatal("Missing key!", i)
		}
	}
	keyList := strings.Split(keyListString, "\n")
	if len(keyList) != nKeys+nExtraLines {
		t.Fatal("Unpected number of lines/keys", len(keyList), nKeys+nExtraLines)
	}

	// Make sure key data isn't shown but otherwise the same checks pass.
	keyListString, err = skykeyListKeys(n.Client, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < nKeys; i++ {
		if !strings.Contains(keyListString, keyNames[i]) {
			t.Log(keyListString)
			t.Fatal("Missing key name!", i)
		}
		if strings.Contains(keyListString, keyStrings[i]) {
			t.Log(keyListString)
			t.Fatal("Found key!", i)
		}
	}
	keyList = strings.Split(keyListString, "\n")
	if len(keyList) != nKeys+nExtraLines {
		t.Fatal("Unpected number of lines/keys", len(keyList))
	}

	nExtraKeys := 10
	nKeys += nExtraKeys
	for i := 0; i < nExtraKeys; i++ {
		nextName := fmt.Sprintf("extrakey-%d", i)
		keyNames = append(keyNames, nextName)
		nextSkStr, err := skykeyCreate(n.Client, nextName)
		if err != nil {
			t.Fatal(err)
		}
		keyStrings = append(keyStrings, nextSkStr)
	}

	// Check that all the key names and key data is there.
	keyListString, err = skykeyListKeys(n.Client, true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < nKeys; i++ {
		if !strings.Contains(keyListString, keyNames[i]) {
			t.Log(keyListString)
			t.Fatal("Missing key name!", i)
		}
		if !strings.Contains(keyListString, keyStrings[i]) {
			t.Log(keyListString)
			t.Fatal("Missing key!", i)
		}
	}
	keyList = strings.Split(keyListString, "\n")
	if len(keyList) != nKeys+nExtraLines {
		t.Fatal("Unpected number of lines/keys", len(keyList))
	}

	// Make sure key data isn't shown but otherwise the same checks pass.
	keyListString, err = skykeyListKeys(n.Client, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < nKeys; i++ {
		if !strings.Contains(keyListString, keyNames[i]) {
			t.Log(keyListString)
			t.Fatal("Missing key name!", i)
		}
		if strings.Contains(keyListString, keyStrings[i]) {
			t.Log(keyListString)
			t.Fatal("Found key!", i)
		}
	}
	keyList = strings.Split(keyListString, "\n")
	if len(keyList) != nKeys+nExtraLines {
		t.Fatal("Unpected number of lines/keys", len(keyList))
	}
}
