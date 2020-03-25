package modules

import (
	"encoding/hex"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestAppFeeEncoding probes the encoding of the AppFees
func TestAppFeeEncoding(t *testing.T) {
	// Create fees
	fee1 := AppFee{
		Address:    types.UnlockHash{},
		Amount:     types.NewCurrency64(fastrand.Uint64n(100)),
		AppUID:     AppUID(hex.EncodeToString(fastrand.Bytes(20))),
		Cancelled:  fastrand.Intn(100)%2 == 0,
		Offset:     int64(fastrand.Intn(1000)),
		Reoccuring: fastrand.Intn(100)%2 == 0,
		UID:        FeeUID("fee1"),
	}
	fee2 := AppFee{
		Address:    types.UnlockHash{},
		Amount:     types.NewCurrency64(fastrand.Uint64n(100)),
		AppUID:     AppUID(hex.EncodeToString(fastrand.Bytes(20))),
		Cancelled:  fastrand.Intn(100)%2 == 0,
		Offset:     int64(fastrand.Intn(1000)),
		Reoccuring: fastrand.Intn(100)%2 == 0,
		UID:        FeeUID("fee2"),
	}

	// Marshal Fees
	data1, err := MarshalFee(fee1)
	if err != nil {
		t.Fatal(err)
	}
	data2, err := MarshalFee(fee2)
	if err != nil {
		t.Fatal(err)
	}

	// Unmarshal fees
	fees, err := UnmarshalFees(append(data1, data2...))
	if err != nil {
		t.Fatal(err)
	}

	// Check Fees
	if len(fees) != 2 {
		t.Fatalf("Expected 2 fees but found %v", len(fees))
	}
	if !reflect.DeepEqual(fees[0], fee1) {
		t.Log("Fees Before", fee1)
		t.Log("Fees After", fees[0])
		t.Fatal("Fees not equal after encoding")
	}
	if !reflect.DeepEqual(fees[1], fee2) {
		t.Log("Fees Before", fee2)
		t.Log("Fees After", fees[1])
		t.Fatal("Fees not equal after encoding")
	}
}
