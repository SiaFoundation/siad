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

// TestSkykeyCommands tests the basic functionality of the siac skykey commands
// interface. More detailed testing of the skykey manager is done in the skykey
// package.
func TestSkykeyCommands(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create a test node/client for this test group
	n := newTestNode(t)
	defer func() {
		if err := n.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Set test parameters for this test group
	params := skykeycmdTestParams{
		skykeyString:     "BAAAAAAAAABrZXkxAAAAAAAAAAQgAAAAAAAAADiObVg49-0juJ8udAx4qMW-TEHgDxfjA0fjJSNBuJ4a",
		skykeyCipherType: "XChaCha20",
		client:           n.Client,
	}

	// Define subtests
	subTests := []skykeycmdSubTest{
		{Name: "TestDuplicateSkykeyAdd", Test: testDuplicateSkykeyAdd},
		{Name: "TestChangeKeyEntropyKeepName", Test: testChangeKeyEntropyKeepName},
		{Name: "TestAddKeyTwice", Test: testAddKeyTwice},
		{Name: "TestInvalidCipherType", Test: testInvalidCipherType},
		{Name: "TestSkykeyGet", Test: testSkykeyGet},
		{Name: "TestSkykeyGetUsingNameAndID", Test: testSkykeyGetUsingNameAndID},
		{Name: "TestSkykeyGetUsingNoNameAndNoID", Test: testSkykeyGetUsingNoNameAndNoID},
	}

	// Execute subtests
	for _, test := range subTests {
		t.Run(test.Name, func(t *testing.T) {
			test.Test(t, params)
		})
	}
}

// newTestNode creates a new Sia node for a test
func newTestNode(t *testing.T) *siatest.TestNode {
	n, err := siatest.NewNode(node.AllModules(build.TempDir(t.Name())))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// testDuplicateSkykeyAdd tests that adding with duplicate Skykey will return
// duplicate name error.
func testDuplicateSkykeyAdd(t *testing.T, p skykeycmdTestParams) {
	err := skykeyAdd(p.client, p.skykeyString)
	if err != nil {
		t.Fatal(err)
	}

	err = skykeyAdd(p.client, p.skykeyString)
	if !strings.Contains(err.Error(), skykey.ErrSkykeyWithIDAlreadyExists.Error()) {
		t.Fatal("Expected duplicate name error but got", err)
	}
}

// testChangeKeyEntropyKeepName tests that adding with changed entropy but the
// same Skykey will return duplicate name error.
func testChangeKeyEntropyKeepName(t *testing.T, p skykeycmdTestParams) {
	// Change the key entropy, but keep the same name.
	var sk skykey.Skykey
	err := sk.FromString(p.skykeyString)
	if err != nil {
		t.Fatal(err)
	}
	sk.Entropy[0] ^= 1 // flip the first byte.

	skString, err := sk.ToString()
	if err != nil {
		t.Fatal(err)
	}

	// This should return a duplicate name error.
	err = skykeyAdd(p.client, skString)
	if !strings.Contains(err.Error(), skykey.ErrSkykeyWithNameAlreadyExists.Error()) {
		t.Fatal("Expected duplicate name error but got", err)
	}
}

// testAddKeyTwice tests that creating a Skykey with the same key name twice
// returns duplicate name error.
func testAddKeyTwice(t *testing.T, p skykeycmdTestParams) {
	// Check that adding same key twice returns an error.
	keyName := "createkey1"
	_, err := skykeyCreate(p.client, keyName, p.skykeyCipherType)
	if err != nil {
		t.Fatal(err)
	}
	_, err = skykeyCreate(p.client, keyName, p.skykeyCipherType)
	if !strings.Contains(err.Error(), skykey.ErrSkykeyWithNameAlreadyExists.Error()) {
		t.Fatal("Expected error when creating key with same name")
	}
}

// testInvalidCipherType tests that invalid cipher types are caught.
func testInvalidCipherType(t *testing.T, p skykeycmdTestParams) {
	invalidSkykeyCipherType := "InvalidCipherType"
	_, err := skykeyCreate(p.client, "createkey2", invalidSkykeyCipherType)
	if !errors.Contains(err, crypto.ErrInvalidCipherType) {
		t.Fatal("Expected error when creating key with invalid ciphertype")
	}
}

// testSkykeyGet tests skykeyGet with known key should not return any errors.
func testSkykeyGet(t *testing.T, p skykeycmdTestParams) {
	keyName := "createkey testSkykeyGet"
	newSkykey, err := skykeyCreate(p.client, keyName, p.skykeyCipherType)
	if err != nil {
		t.Fatal(err)
	}
	getKeyStr, err := skykeyGet(p.client, keyName, "")
	if err != nil {
		t.Fatal(err)
	}
	if getKeyStr != newSkykey {
		t.Fatal("Expected keys to match")
	}
}

// testSkykeyGetUsingNameAndID tests using both name and id params should return an
// error.
func testSkykeyGetUsingNameAndID(t *testing.T, p skykeycmdTestParams) {
	_, err := skykeyGet(p.client, "name", "id")
	if err == nil {
		t.Fatal("Expected error when using both name and id")
	}
}

// testSkykeyGetUsingNoNameAndNoID test using neither name or id param should return an
// error.
func testSkykeyGetUsingNoNameAndNoID(t *testing.T, p skykeycmdTestParams) {
	_, err := skykeyGet(p.client, "", "")
	if err == nil {
		t.Fatal("Expected error when using neither name or id params")
	}
}

// skykeycmdSubTest is a local helper struct for running skykeycmd subtests
// so they can use the same skykeycmd test params
type skykeycmdSubTest struct {
	Name string
	Test func(*testing.T, skykeycmdTestParams)
}

// skykeycmdTestParams is a local helper struct to define common test
// parameters for skykeycmd sub tests
type skykeycmdTestParams struct {
	skykeyString     string
	skykeyCipherType string
	client           client.Client
}
