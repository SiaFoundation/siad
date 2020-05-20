package main

import (
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/errors"
)

// TestSkykeyCommands tests the basic functionality of the siac skykey commands
// interface. More detailed testing of the skykey manager is done in the skykey
// package.
func TestSkykeyCommands(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	groupDir := skykeycmdTestDir(t.Name())

	// Define subtests
	subTests := []SubTest{
		{Name: "TestDuplicateSkykeyAdd", Test: testDuplicateSkykeyAdd},
		{Name: "TestChangeKeyEntropyKeepName", Test: testChangeKeyEntropyKeepName},
		{Name: "TestAddKeyTwice", Test: testAddKeyTwice},
		{Name: "TestInvalidCipherType", Test: testInvalidCipherType},
		{Name: "TestSkykeyGet", Test: testSkykeyGet},
		{Name: "TestSkykeyGetUsingNameAndID", Test: testSkykeyGetUsingNameAndID},
		{Name: "TestSkykeyGetUsingNoNameAndNoID", Test: testSkykeyGetUsingNoNameAndNoID},
	}

	// Run tests
	if err := RunSubTests(t, groupDir, subTests); err != nil {
		t.Fatal(err)
	}
}

// testDuplicateSkykeyAdd tests that adding with duplicate Skykey will return
// duplicate name error.
func testDuplicateSkykeyAdd(t *testing.T, c client.Client) {
	skykeyString := "BAAAAAAAAABrZXkxAAAAAAAAAAQgAAAAAAAAADiObVg49-0juJ8udAx4qMW-TEHgDxfjA0fjJSNBuJ4a"
	err := skykeyAdd(c, skykeyString)
	if err != nil {
		t.Fatal(err)
	}

	err = skykeyAdd(c, skykeyString)
	if !strings.Contains(err.Error(), skykey.ErrSkykeyWithIDAlreadyExists.Error()) {
		t.Fatal("Expected duplicate name error but got", err)
	}
}

// testChangeKeyEntropyKeepName tests that adding with changed entropy but the
// same Skykey will return duplicate name error.
func testChangeKeyEntropyKeepName(t *testing.T, c client.Client) {
	// Change the key entropy, but keep the same name.
	var sk skykey.Skykey
	skykeyString := "BAAAAAAAAABrZXkxAAAAAAAAAAQgAAAAAAAAADiObVg49-0juJ8udAx4qMW-TEHgDxfjA0fjJSNBuJ4a"
	err := sk.FromString(skykeyString)
	if err != nil {
		t.Fatal(err)
	}
	sk.Entropy[0] ^= 1 // flip the first byte.

	skString, err := sk.ToString()
	if err != nil {
		t.Fatal(err)
	}

	// This should return a duplicate name error.
	err = skykeyAdd(c, skString)
	if !strings.Contains(err.Error(), skykey.ErrSkykeyWithNameAlreadyExists.Error()) {
		t.Fatal("Expected duplicate name error but got", err)
	}
}

// testAddKeyTwice tests that creating a Skykey with the same key name twice
// returns duplicate name error.
func testAddKeyTwice(t *testing.T, c client.Client) {
	// Check that adding same key twice returns an error.
	keyName := "createkey1"
	skykeyCipherType := "XChaCha20"
	_, err := skykeyCreate(c, keyName, skykeyCipherType)
	if err != nil {
		t.Fatal(err)
	}
	_, err = skykeyCreate(c, keyName, skykeyCipherType)
	if !strings.Contains(err.Error(), skykey.ErrSkykeyWithNameAlreadyExists.Error()) {
		t.Fatal("Expected error when creating key with same name")
	}
}

// testInvalidCipherType tests that invalid cipher types are caught.
func testInvalidCipherType(t *testing.T, c client.Client) {
	invalidSkykeyCipherType := "InvalidCipherType"
	_, err := skykeyCreate(c, "createkey2", invalidSkykeyCipherType)
	if !errors.Contains(err, crypto.ErrInvalidCipherType) {
		t.Fatal("Expected error when creating key with invalid ciphertype")
	}
}

// testSkykeyGet tests skykeyGet with known key should not return any errors.
func testSkykeyGet(t *testing.T, c client.Client) {
	keyName := "createkey testSkykeyGet"
	skykeyCipherType := "XChaCha20"
	newSkykey, err := skykeyCreate(c, keyName, skykeyCipherType)
	if err != nil {
		t.Fatal(err)
	}
	getKeyStr, err := skykeyGet(c, keyName, "")
	if err != nil {
		t.Fatal(err)
	}
	if getKeyStr != newSkykey {
		t.Fatal("Expected keys to match")
	}
}

// testSkykeyGetUsingNameAndID tests using both name and id params should return an
// error.
func testSkykeyGetUsingNameAndID(t *testing.T, c client.Client) {
	_, err := skykeyGet(c, "name", "id")
	if err == nil {
		t.Fatal("Expected error when using both name and id")
	}
}

// testSkykeyGetUsingNoNameAndNoID test using neither name or id param should return an
// error.
func testSkykeyGetUsingNoNameAndNoID(t *testing.T, c client.Client) {
	_, err := skykeyGet(c, "", "")
	if err == nil {
		t.Fatal("Expected error when using neither name or id params")
	}
}
