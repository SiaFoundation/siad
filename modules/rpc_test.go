package modules

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestRPCReadWriteError verifies the functionality of RPCRead, RPCWrite and
// RPCWriteError
func TestRPCReadWriteError(t *testing.T) {
	t.Parallel()

	// use a buffer as stream to avoid unnecessary setup
	stream := new(bytes.Buffer)

	expectedData := []byte("some data")
	err := RPCWrite(stream, expectedData)
	if err != nil {
		t.Fatal(err)
	}
	var data []byte
	err = RPCRead(stream, &data)
	if err != nil {
		t.Error(err)
	}
	if !bytes.Equal(data, expectedData) {
		t.Fatalf("Expected data to be '%v' but received '%v'", expectedData, data)
	}

	expectedErr := errors.New("some error")
	err = RPCWriteError(stream, expectedErr)
	if err != nil {
		t.Fatal(err)
	}
	resp := RPCRead(stream, &struct{}{})
	if resp.Error() != expectedErr.Error() {
		t.Fatalf("Expected '%v' but received '%v'", expectedErr, resp)
	}
}

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

// TestRPCExecuteProgramResponseMarshalSia tests the custom SiaMarshaler
// implementation of RPCExecuteProgramResponse.
func TestRPCExecuteProgramResponseMarshalsia(t *testing.T) {
	epr := RPCExecuteProgramResponse{
		AdditionalCollateral: types.SiacoinPrecision,
		Output:               fastrand.Bytes(10),
		NewMerkleRoot:        crypto.Hash{1},
		NewSize:              100,
		Proof:                []crypto.Hash{{1}, {2}, {3}},
		Error:                errors.New("some error"),
		TotalCost:            types.SiacoinPrecision.Mul64(10),
		PotentialRefund:      types.SiacoinPrecision.Mul64(100),
	}
	// Marshal
	b := encoding.Marshal(epr)
	// Unmarshal
	var epr2 RPCExecuteProgramResponse
	err := encoding.Unmarshal(b, &epr2)
	if err != nil {
		t.Fatal(err)
	}
	// Compare
	if !reflect.DeepEqual(epr, epr2) {
		t.Fatal("responses don't match")
	}
}
