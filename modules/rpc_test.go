package modules

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/types"
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
func TestRPCExecuteProgramResponseMarshalSia(t *testing.T) {
	// helper to create random error
	randomError := func() error {
		if fastrand.Intn(2) == 0 {
			return nil
		}
		return errors.New(hex.EncodeToString(fastrand.Bytes(100)))
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
		OutputLength:         fastrand.Uint64n(10),
		NewMerkleRoot:        randomHash(),
		NewSize:              fastrand.Uint64n(100),
		Proof:                randomProof(),
		Error:                randomError(),
		TotalCost:            types.NewCurrency64(fastrand.Uint64n(100)),
		FailureRefund:        types.NewCurrency64(fastrand.Uint64n(100)),
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
	if !epr.AdditionalCollateral.Equals(epr2.AdditionalCollateral) {
		t.Log(epr.AdditionalCollateral)
		t.Log(epr2.AdditionalCollateral)
		t.Fatal("field doesn't match")
	}
	if epr.OutputLength != epr2.OutputLength {
		t.Log(epr.OutputLength)
		t.Log(epr2.OutputLength)
		t.Fatal("field doesn't match")
	}
	if !bytes.Equal(epr.NewMerkleRoot[:], epr2.NewMerkleRoot[:]) {
		t.Log(epr.NewMerkleRoot)
		t.Log(epr2.NewMerkleRoot)
		t.Fatal("field doesn't match")
	}
	if epr.NewSize != epr2.NewSize {
		t.Log(epr.NewSize)
		t.Log(epr2.NewSize)
		t.Fatal("field doesn't match")
	}
	if !(len(epr.Proof) == 0 && len(epr2.Proof) == 0) && !reflect.DeepEqual(epr.Proof, epr2.Proof) {
		t.Log(epr.Proof)
		t.Log(epr2.Proof)
		println("ep", epr.Proof, epr2.Proof)
		t.Fatal("field doesn't match")
	}
	bothNil := epr.Error == nil && epr2.Error == nil
	strMatch := epr.Error != nil && epr2.Error != nil && epr.Error.Error() == epr2.Error.Error()
	if !bothNil && !strMatch {
		t.Log(epr.Error)
		t.Log(epr2.Error)
		t.Fatal("field doesn't match")
	}
	if !epr.TotalCost.Equals(epr2.TotalCost) {
		t.Log(epr.TotalCost)
		t.Log(epr2.TotalCost)
		t.Fatal("field doesn't match")
	}
	if !epr.FailureRefund.Equals(epr2.FailureRefund) {
		t.Log(epr.FailureRefund)
		t.Log(epr2.FailureRefund)
		t.Fatal("field doesn't match")
	}
}

// TestIsPriceTableInvalidErr is a small unit test that verifies the
// functionality of the `IsPriceTableInvalidErr` helper.
func TestIsPriceTableInvalidErr(t *testing.T) {
	t.Parallel()

	var tests = []struct {
		err      error
		expected bool
	}{
		{nil, false},
		{errors.New("err"), false},
		{ErrPriceTableExpired, true},
		{ErrPriceTableNotFound, true},
		{errors.Compose(ErrPriceTableNotFound, ErrPriceTableExpired), true},
		{errors.Compose(ErrPriceTableNotFound, errors.New("err")), true},
		{errors.Compose(ErrPriceTableExpired, errors.New("err")), true},
		{errors.AddContext(ErrPriceTableNotFound, "err"), true},
	}
	for _, test := range tests {
		actual := IsPriceTableInvalidErr(test.err)
		if actual != test.expected {
			t.Fatal("unexpected", test.err)
		}
	}
}
