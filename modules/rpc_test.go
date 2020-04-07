package modules

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"
)

// TestUniqueIDMarshaling verifies a UniqueID can be properly marshaled and
// unmarsheled
func TestUniqueIDMarshaling(t *testing.T) {
	var uid UniqueID
	fastrand.Read(uid[:])

	uidBytes, err := json.Marshal(uid)
	if err != nil {
		t.Fatal(err)
	}
	var uidUmar UniqueID
	err = json.Unmarshal(uidBytes, &uidUmar)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(uid[:], uidUmar[:]) {
		t.Fatal("UniqueID not equal after marshaling")
	}
}

// TestUniqueID_LoadString verifies the functionality of LoadString
func TestUniqueID_LoadString(t *testing.T) {
	var uid UniqueID
	fastrand.Read(uid[:])

	uidStr := uid.String()

	var uidLoaded UniqueID
	err := uidLoaded.LoadString(uidStr)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(uid[:], uidLoaded[:]) {
		t.Fatal("UniqueID not equal after doing LoadString")
	}

	err = uidLoaded.LoadString(uidStr + "a")
	if err == nil || !strings.Contains(err.Error(), "incorrect length") {
		t.Fatalf("Expected 'incorrect length' error, instead error was '%v'", err)
	}
}
