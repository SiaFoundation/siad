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
	// helper to create random error
	randomError := func() error {
		if fastrand.Intn(2) == 0 {
			return nil
		}
		return errors.New(string(fastrand.Bytes(100)))
	}
	// helper to create random hash
	randomHash := func() (h crypto.Hash) {
		fastrand.Read(h[:])
		return
	}
	// helper to create random proof.
	randomProof := func() (proof []crypto.Hash) {
		switch fastrand.Intn(3) {
		case 0:
			// nil proof
			return nil
		case 1:
			// empty proof
			return []crypto.Hash{}
		default:
		}
		// random length proof
		for i := 0; i < fastrand.Intn(5)+1; i++ {
			proof = append(proof, randomHash())
		}
		return
	}
	epr := RPCExecuteProgramResponse{
		AdditionalCollateral: types.NewCurrency64(fastrand.Uint64n(100)),
		Output:               fastrand.Bytes(10),
		NewMerkleRoot:        randomHash(),
		NewSize:              fastrand.Uint64n(100),
		Proof:                randomProof(),
		Error:                randomError(),
		TotalCost:            types.NewCurrency64(fastrand.Uint64n(100)),
		PotentialRefund:      types.NewCurrency64(fastrand.Uint64n(100)),
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
		t.Log(epr)
		t.Log(epr2)
		t.Fatal("responses don't match")
	}
}
