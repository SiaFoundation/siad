package modules

import (
	"bytes"
	"encoding/json"
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
