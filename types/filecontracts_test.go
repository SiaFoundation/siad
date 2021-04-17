package types

import (
	"reflect"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/crypto"
)

// TestFileContractTax probes the Tax function.
func TestTax(t *testing.T) {
	// Test explicit values for post-hardfork tax values.
	if Tax(1e9, NewCurrency64(125e9)).Cmp(NewCurrency64(4875e6)) != 0 {
		t.Error("tax is being calculated incorrectly")
	}
	if PostTax(1e9, NewCurrency64(125e9)).Cmp(NewCurrency64(120125e6)) != 0 {
		t.Error("tax is being calculated incorrectly")
	}

	// Test equivalency for a series of values.
	if testing.Short() {
		t.SkipNow()
	}
	// COMPATv0.4.0 - check at height 0.
	for i := uint64(0); i < 10e3; i++ {
		val := NewCurrency64((1e3 * i) + i)
		tax := Tax(0, val)
		postTax := PostTax(0, val)
		if val.Cmp(tax.Add(postTax)) != 0 {
			t.Error("tax calculation inconsistent for", i)
		}
	}
	// Check at height 1e9
	for i := uint64(0); i < 10e3; i++ {
		val := NewCurrency64((1e3 * i) + i)
		tax := Tax(1e9, val)
		postTax := PostTax(1e9, val)
		if val.Cmp(tax.Add(postTax)) != 0 {
			t.Error("tax calculation inconsistent for", i)
		}
	}
}

// TestEAFundRevision probes the EAFundRevision function
func TestEAFundRevision(t *testing.T) {
	mock := func(renterFunds, hostCollateral uint64) FileContractRevision {
		return FileContractRevision{
			NewValidProofOutputs: []SiacoinOutput{
				{Value: NewCurrency64(renterFunds)},
				{Value: ZeroCurrency},
			},
			NewMissedProofOutputs: []SiacoinOutput{
				{Value: NewCurrency64(renterFunds)},
				{Value: NewCurrency64(hostCollateral)},
				{Value: ZeroCurrency},
			},
		}
	}

	// expect no error if amount is less than or equal to the renter funds
	rev := mock(100, 0)
	_, err := rev.EAFundRevision(NewCurrency64(99))
	if err != nil {
		t.Fatalf("Unexpected error '%v'", err)
	}
	_, err = rev.EAFundRevision(NewCurrency64(100))
	if err != nil {
		t.Fatalf("Unexpected error '%v'", err)
	}

	// expect ErrRevisionCostTooHigh
	rev = mock(100, 0)
	_, err = rev.EAFundRevision(NewCurrency64(100 + 1))
	if !errors.Contains(err, ErrRevisionCostTooHigh) {
		t.Fatalf("Expected error '%v' but received '%v'  ", ErrRevisionCostTooHigh, err)
	}

	// expect ErrRevisionCostTooHigh
	rev = mock(100, 0)
	rev.SetMissedRenterPayout(NewCurrency64(99))
	_, err = rev.EAFundRevision(NewCurrency64(100))
	if !errors.Contains(err, ErrRevisionCostTooHigh) {
		t.Fatalf("Expected error '%v' but received '%v'  ", ErrRevisionCostTooHigh, err)
	}

	// expect no error if amount is less than or equal to the host collateral
	rev = mock(100, 100)
	_, err = rev.EAFundRevision(NewCurrency64(99))
	if err != nil {
		t.Fatalf("Unexpected error '%v'", err)
	}
	_, err = rev.EAFundRevision(NewCurrency64(100))
	if err != nil {
		t.Fatalf("Unexpected error '%v'", err)
	}

	// verify funds moved to the appropriate outputs
	existing := mock(100, 0)
	amount := NewCurrency64(99)
	payment, err := existing.EAFundRevision(amount)
	if err != nil {
		t.Fatalf("Unexpected error '%v'", err)
	}
	if !existing.ValidRenterPayout().Sub(payment.ValidRenterPayout()).Equals(amount) {
		t.Fatal("Unexpected payout moved from renter to host")
	}
	if !payment.ValidHostPayout().Sub(existing.ValidHostPayout()).Equals(amount) {
		t.Fatal("Unexpected payout moved from renter to host")
	}
	if !payment.MissedHostPayout().Sub(existing.MissedHostPayout()).Equals(amount) {
		t.Fatal("Unexpected payout moved from renter to host")
	}
	if !payment.MissedRenterOutput().Value.Equals(existing.MissedRenterOutput().Value.Sub(amount)) {
		t.Fatal("Unexpected payout moved from renter to void")
	}
	pmvo, err1 := payment.MissedVoidOutput()
	emvo, err2 := existing.MissedVoidOutput()
	if err := errors.Compose(err1, err2); err != nil {
		t.Fatal(err)
	}
	if !pmvo.Value.Equals(emvo.Value) {
		t.Fatal("Unexpected payout moved from renter to void")
	}
}

// TestPaymentRevision probes the PaymentRevision function
func TestPaymentRevision(t *testing.T) {
	mock := func(renterFunds, hostCollateral uint64) FileContractRevision {
		return FileContractRevision{
			NewValidProofOutputs: []SiacoinOutput{
				{Value: NewCurrency64(renterFunds)},
				{Value: ZeroCurrency},
			},
			NewMissedProofOutputs: []SiacoinOutput{
				{Value: NewCurrency64(renterFunds)},
				{Value: NewCurrency64(hostCollateral)},
				{Value: ZeroCurrency},
			},
		}
	}

	// expect no error if amount is less than or equal to the renter funds
	rev := mock(100, 0)
	_, err := rev.PaymentRevision(NewCurrency64(99))
	if err != nil {
		t.Fatalf("Unexpected error '%v'", err)
	}
	_, err = rev.PaymentRevision(NewCurrency64(100))
	if err != nil {
		t.Fatalf("Unexpected error '%v'", err)
	}

	// expect ErrRevisionCostTooHigh
	rev = mock(100, 0)
	_, err = rev.PaymentRevision(NewCurrency64(100 + 1))
	if !errors.Contains(err, ErrRevisionCostTooHigh) {
		t.Fatalf("Expected error '%v' but received '%v'  ", ErrRevisionCostTooHigh, err)
	}

	// expect ErrRevisionCostTooHigh
	rev = mock(100, 0)
	rev.SetMissedRenterPayout(NewCurrency64(99))
	_, err = rev.PaymentRevision(NewCurrency64(100))
	if !errors.Contains(err, ErrRevisionCostTooHigh) {
		t.Fatalf("Expected error '%v' but received '%v'  ", ErrRevisionCostTooHigh, err)
	}

	// expect no error if amount is less than or equal to the host collateral
	rev = mock(100, 100)
	_, err = rev.PaymentRevision(NewCurrency64(99))
	if err != nil {
		t.Fatalf("Unexpected error '%v'", err)
	}
	_, err = rev.PaymentRevision(NewCurrency64(100))
	if err != nil {
		t.Fatalf("Unexpected error '%v'", err)
	}

	// expect error if void output is missing
	rev = mock(100, 100)
	rev.NewMissedProofOutputs = append([]SiacoinOutput{}, rev.NewMissedProofOutputs[0], rev.NewMissedProofOutputs[1])
	_, err = rev.PaymentRevision(NewCurrency64(100))
	if err == nil || !strings.Contains(err.Error(), "void output is missing") {
		t.Fatalf("Unexpected error '%v'", err)
	}

	// verify funds moved to the appropriate outputs
	existing := mock(100, 0)
	amount := NewCurrency64(99)
	payment, err := existing.PaymentRevision(amount)
	if err != nil {
		t.Fatalf("Unexpected error '%v'", err)
	}
	if !existing.ValidRenterPayout().Sub(payment.ValidRenterPayout()).Equals(amount) {
		t.Fatal("Unexpected payout moved from renter to host")
	}
	if !payment.ValidHostPayout().Sub(existing.ValidHostPayout()).Equals(amount) {
		t.Fatal("Unexpected payout moved from renter to host")
	}
	if !payment.MissedRenterOutput().Value.Equals(existing.MissedRenterOutput().Value.Sub(amount)) {
		t.Fatal("Unexpected payout moved from renter to void")
	}
	pmvo, err1 := payment.MissedVoidOutput()
	emvo, err2 := existing.MissedVoidOutput()
	if err := errors.Compose(err1, err2); err != nil {
		t.Fatal(err)
	}
	if !pmvo.Value.Equals(emvo.Value.Add(amount)) {
		t.Fatal("Unexpected payout moved from renter to void")
	}
}

// TestExecuteProgramRevision probes the ExecuteProgramRevision function
func TestExecuteProgramRevision(t *testing.T) {
	mock := func(renterFunds, missedHostPayout uint64) FileContractRevision {
		return FileContractRevision{
			NewValidProofOutputs: []SiacoinOutput{
				{Value: NewCurrency64(renterFunds)},
				{Value: ZeroCurrency},
			},
			NewMissedProofOutputs: []SiacoinOutput{
				{Value: NewCurrency64(renterFunds)},
				{Value: NewCurrency64(missedHostPayout)},
				{Value: ZeroCurrency},
			},
			NewRevisionNumber: 1,
		}
	}

	// expect no error if amount is less than or equal to the renter funds
	rev := mock(100, 50)
	_, err := rev.ExecuteProgramRevision(2, NewCurrency64(49), crypto.Hash{}, 0)
	if err != nil {
		t.Fatalf("Unexpected error '%v'", err)
	}
	_, err = rev.ExecuteProgramRevision(2, NewCurrency64(50), crypto.Hash{}, 0)
	if err != nil {
		t.Fatalf("Unexpected error '%v'", err)
	}

	// expect an error if the revision number didn't increase.
	rev = mock(100, 50)
	_, err = rev.ExecuteProgramRevision(0, NewCurrency64(49), crypto.Hash{}, 0)
	if !errors.Contains(err, ErrRevisionNotIncremented) {
		t.Fatal("expected ErrRevisionNotIncremented but got", err)
	}
	_, err = rev.ExecuteProgramRevision(1, NewCurrency64(49), crypto.Hash{}, 0)
	if !errors.Contains(err, ErrRevisionNotIncremented) {
		t.Fatal("expected ErrRevisionNotIncremented but got", err)
	}

	// expect an error if we transfer more than the the host's missed output.
	rev = mock(100, 50)
	_, err = rev.ExecuteProgramRevision(2, rev.MissedHostPayout().Add64(1), crypto.Hash{}, 0)
	if !errors.Contains(err, ErrRevisionCostTooHigh) {
		t.Fatalf("Expected error '%v' but received '%v'  ", ErrRevisionCostTooHigh, err)
	}

	// expect an error if the void output is missing.
	rev = mock(100, 50)
	rev.NewMissedProofOutputs = rev.NewMissedProofOutputs[:2]
	_, err = rev.ExecuteProgramRevision(2, NewCurrency64(50), crypto.Hash{}, 0)
	if !strings.Contains(err.Error(), "failed to get void payout") {
		t.Fatalf("expected failure due to missing void payout but got %v", err)
	}

	// verify funds moved to the appropriate outputs
	rev = mock(100, 50)
	transfer := NewCurrency64(50)
	revision, err := rev.ExecuteProgramRevision(2, transfer, crypto.Hash{}, 0)
	if err != nil {
		t.Fatalf("Unexpected error '%v'", err)
	}
	// Valid outputs should stay the same.
	if !reflect.DeepEqual(rev.NewValidProofOutputs, revision.NewValidProofOutputs) {
		t.Fatal("valid outputs changed")
	}
	// Missed renter output should stay the same.
	if !rev.MissedRenterPayout().Equals(revision.MissedRenterPayout()) {
		t.Fatal("missed renter payout changed")
	}
	// Check money moved from host.
	fromHost := rev.MissedHostPayout().Sub(revision.MissedHostPayout())
	if !fromHost.Equals(transfer) {
		t.Fatal("money moved to void doesn't match transfer")
	}
	// Check money moved to void.
	newVoid, _ := revision.MissedVoidPayout()
	oldVoid, _ := rev.MissedVoidPayout()
	if !newVoid.Sub(oldVoid).Equals(transfer) {
		t.Fatal("money moved to void doesn't match transfer")
	}
}
