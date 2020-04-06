package main

import (
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/skykey"
)

func TestSkykeyCommands(t *testing.T) {
	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:   0,
		Miners:  1,
		Renters: 1,
	}
	groupDir := build.TempDir(t.Name())

	subTests := []siatest.SubTest{
		{Name: "TestAddSkykeyCmd", Test: testAddSkykeyCmd},
	}

	// Run tests
	if err := siatest.RunSubTests(t, groupParams, groupDir, subTests); err != nil {
		t.Fatal(err)
	}
}

func testAddSkykeyCmd(t *testing.T, tg *siatest.TestGroup) {
	// Set global HTTP client to the renter's client.
	httpClient = tg.Renters()[0].Client

	testSkykeyString := "BAAAAAAAAABrZXkxAAAAAAAAAAQgAAAAAAAAADiObVg49-0juJ8udAx4qMW-TEHgDxfjA0fjJSNBuJ4a"
	err := skykeyAdd(testSkykeyString)
	if err != nil {
		t.Fatal(err)
	}

	err = skykeyAdd(testSkykeyString)
	if err == nil || strings.Contains(err.Error(), skykey.ErrSkykeyNameAlreadyUsed.Error()) {
		t.Fatal("Expected non duplicate name error", err)
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
	err = skykeyAdd(skString)
	if !strings.Contains(err.Error(), skykey.ErrSkykeyNameAlreadyUsed.Error()) {
		t.Fatal("Expected duplicate name error", err)
	}
}
