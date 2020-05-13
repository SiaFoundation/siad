package main

import (
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/skykey"
)

var (
	testSkykeyString = "BAAAAAAAAABrZXkxAAAAAAAAAAQgAAAAAAAAADiObVg49-0juJ8udAx4qMW-TEHgDxfjA0fjJSNBuJ4a"
	testClient       client.Client
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
	keyName := "createkey1"
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
}

//xxxqqq
func TestSkykeyCommandsXXX(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	//xxxccc move to local variable after !4387 is merged
	n := newTestNode(t)
	defer n.Close()
	testClient = n.Client

	// Set the (global) cipher type to the only allowed type.
	// This is normally done by the flag parser.
	skykeyCipherType = "XChaCha20"

	t.Run("TestDuplicateSkykeyAdd", testDuplicateSkykeyAdd)
	t.Run("TestChangeKeyEntropyKeepName", testChangeKeyEntropyKeepName)
	t.Run("TestAddKeyTwice", testAddKeyTwice)
	t.Run("TestInvalidCipherType", testInvalidCipherType)
	t.Run("TestSkykeyGet", testSkykeyGet)
	t.Run("TestUsingNameAndID", testUsingNameAndID)
}

//xxxqqq
func newTestNode(t *testing.T) *siatest.TestNode {
	// Create a node for the test
	n, err := siatest.NewNode(node.AllModules(build.TempDir(t.Name())))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func testDuplicateSkykeyAdd(t *testing.T) {
	err := skykeyAdd(testClient, testSkykeyString)
	if err != nil {
		t.Fatal(err)
	}

	err = skykeyAdd(testClient, testSkykeyString)
	if !strings.Contains(err.Error(), skykey.ErrSkykeyWithIDAlreadyExists.Error()) {
		t.Fatal("Expected duplicate name error but got", err)
	}
}

func testChangeKeyEntropyKeepName(t *testing.T) {
	// Change the key entropy, but keep the same name.
	var sk skykey.Skykey
	err := sk.FromString(testSkykeyString)
	if err != nil {
		t.Fatal(err)
	}
	sk.Entropy[0] ^= 1 // flip the first byte.

	skString, err := sk.ToString()
	if err != nil {
		t.Fatal(err)
	}

	// This should return a duplicate name error.
	err = skykeyAdd(testClient, skString)
	if !strings.Contains(err.Error(), skykey.ErrSkykeyWithNameAlreadyExists.Error()) {
		t.Fatal("Expected duplicate name error", err)
	}
}

func testAddKeyTwice(t *testing.T) {
	// Check that adding same key twice returns an error.
	keyName := "createkey1"
	_, err := skykeyCreate(testClient, keyName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = skykeyCreate(testClient, keyName)
	if !strings.Contains(err.Error(), skykey.ErrSkykeyWithNameAlreadyExists.Error()) {
		t.Fatal("Expected error when creating key with same name")
	}
}

func testInvalidCipherType(t *testing.T) {
	// Check that invalid cipher types are caught.
	//xxxqqq skykeyCipherType is global, should not be global
	skykeyCipherType = "InvalidCipherType"
	_, err := skykeyCreate(testClient, "createkey2")
	if !errors.Contains(err, crypto.ErrInvalidCipherType) {
		t.Fatal("Expected error when creating key with invalid ciphertype")
	}
	skykeyCipherType = "XChaCha20" //reset the ciphertype
}

func testSkykeyGet(t *testing.T) {
	keyName := "createkey testSkykeyGet"
	newSkykey, err := skykeyCreate(testClient, keyName)
	if err != nil {
		t.Fatal(err)
	}

	// Test skykeyGet
	// known key should have no errors.
	getKeyStr, err := skykeyGet(testClient, keyName, "")
	if err != nil {
		t.Fatal(err)
	}

	if getKeyStr != newSkykey {
		t.Fatal("Expected keys to match")
	}
}

func testUsingNameAndID(t *testing.T) {
	// Using both name and id params should return an error
	_, err := skykeyGet(testClient, "name", "id")
	if err == nil {
		t.Fatal("Expected error when using both name and id")
	}
	// Using neither name or id param should return an error
	_, err = skykeyGet(testClient, "", "")
	if err == nil {
		t.Fatal("Expected error when using neither name or id params")
	}
}
