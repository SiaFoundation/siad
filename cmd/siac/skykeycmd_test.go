package main

import (
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

	// Set global HTTP client to the node's client.
	testingHttpClient := n.Client

	// Set the (global) cipher type to the only allowed type.
	// This is normally done by the flag parser.
	skykeyCipherType = "XChaCha20"

	testSkykeyString := "BAAAAAAAAABrZXkxAAAAAAAAAAQgAAAAAAAAADiObVg49-0juJ8udAx4qMW-TEHgDxfjA0fjJSNBuJ4a"
	err = skykeyAdd(&testingHttpClient, testSkykeyString)
	if err != nil {
		t.Fatal(err)
	}

	err = skykeyAdd(&testingHttpClient, testSkykeyString)
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
	err = skykeyAdd(&testingHttpClient, skString)
	if !strings.Contains(err.Error(), skykey.ErrSkykeyWithNameAlreadyExists.Error()) {
		t.Fatal("Expected duplicate name error", err)
	}

	// Check that adding same key twice returns an error.
	keyName := "createkey1"
	newSkykey, err := skykeyCreate(&testingHttpClient, keyName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = skykeyCreate(&testingHttpClient, keyName)
	if !strings.Contains(err.Error(), skykey.ErrSkykeyWithNameAlreadyExists.Error()) {
		t.Fatal("Expected error when creating key with same name")
	}

	// Check that invalid cipher types are caught.
	skykeyCipherType = "InvalidCipherType"
	_, err = skykeyCreate(&testingHttpClient, "createkey2")
	if !errors.Contains(err, crypto.ErrInvalidCipherType) {
		t.Fatal("Expected error when creating key with invalid ciphertype")
	}
	skykeyCipherType = "XChaCha20" //reset the ciphertype

	// Test skykeyGet
	// known key should have no errors.
	getKeyStr, err := skykeyGet(&testingHttpClient, keyName, "")
	if err != nil {
		t.Fatal(err)
	}

	if getKeyStr != newSkykey {
		t.Fatal("Expected keys to match")
	}

	// Using both name and id params should return an error
	_, err = skykeyGet(&testingHttpClient, "name", "id")
	if err == nil {
		t.Fatal("Expected error when using both name and id")
	}
	// Using neither name or id param should return an error
	_, err = skykeyGet(&testingHttpClient, "", "")
	if err == nil {
		t.Fatal("Expected error when using neither name or id params")
	}
}
