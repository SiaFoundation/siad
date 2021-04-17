package host

import (
	"fmt"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/types"
)

// TestVerifyPaymentRevision is a unit test covering verifyPaymentRevision
func TestVerifyPaymentRevision(t *testing.T) {
	t.Parallel()

	// create a current revision and a payment revision
	height := types.BlockHeight(0)
	amount := types.NewCurrency64(1)
	curr := types.FileContractRevision{
		NewValidProofOutputs: []types.SiacoinOutput{
			{Value: types.NewCurrency64(10)},
			{Value: types.NewCurrency64(1)},
		},
		NewMissedProofOutputs: []types.SiacoinOutput{
			{Value: types.NewCurrency64(10)},
			{Value: types.NewCurrency64(1)},
			{Value: types.ZeroCurrency},
		},
		NewWindowStart: types.BlockHeight(revisionSubmissionBuffer) + 1,
	}
	payment, err := curr.PaymentRevision(amount)
	if err != nil {
		t.Fatal(err)
	}

	// verify a properly created payment revision is accepted
	err = verifyPaymentRevision(curr, payment, height, amount)
	if err != nil {
		t.Fatal("Unexpected error when verifying revision, ", err)
	}

	// deepCopy is a helper function that makes a deep copy of a revision
	deepCopy := func(rev types.FileContractRevision) (revCopy types.FileContractRevision) {
		rBytes := encoding.Marshal(rev)
		err := encoding.Unmarshal(rBytes, &revCopy)
		if err != nil {
			panic(err)
		}
		return
	}

	// make sure verification fails if the amount of money moved to the void
	// doesn't match the amount of money moved to the host.
	badPayment := deepCopy(payment)
	badPayment.SetMissedVoidPayout(types.SiacoinPrecision)
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrLowVoidOutput) {
		t.Fatalf("expected %v but got %v", ErrLowVoidOutput, err)
	}

	// expect ErrBadContractOutputCounts
	badOutputs := []types.SiacoinOutput{payment.NewMissedProofOutputs[0]}
	badPayment = deepCopy(payment)
	badPayment.NewMissedProofOutputs = badOutputs
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadContractOutputCounts) {
		t.Fatalf("Expected ErrBadContractOutputCounts but received '%v'", err)
	}

	// expect ErrLateRevision
	badCurr := deepCopy(curr)
	badCurr.NewWindowStart = curr.NewWindowStart - 1
	err = verifyPaymentRevision(badCurr, payment, height, amount)
	if !errors.Contains(err, ErrLateRevision) {
		t.Fatalf("Expected ErrLateRevision but received '%v'", err)
	}

	// expect host payout address changed
	hash := crypto.HashBytes([]byte("random"))
	badCurr = deepCopy(curr)
	badCurr.NewValidProofOutputs[1].UnlockHash = types.UnlockHash(hash)
	err = verifyPaymentRevision(badCurr, payment, height, amount)
	if err == nil || !strings.Contains(err.Error(), "host payout address changed") {
		t.Fatalf("Expected host payout error but received '%v'", err)
	}

	// expect host payout address changed
	badCurr = deepCopy(curr)
	badCurr.NewMissedProofOutputs[1].UnlockHash = types.UnlockHash(hash)
	err = verifyPaymentRevision(badCurr, payment, height, amount)
	if err == nil || !strings.Contains(err.Error(), "host payout address changed") {
		t.Fatalf("Expected host payout error but received '%v'", err)
	}

	// expect missed void output
	badCurr = deepCopy(curr)
	badCurr.NewMissedProofOutputs = append([]types.SiacoinOutput{}, curr.NewMissedProofOutputs[:2]...)
	err = verifyPaymentRevision(badCurr, payment, height, amount)
	if !errors.Contains(err, types.ErrMissingVoidOutput) {
		t.Fatalf("Expected '%v' but received '%v'", types.ErrMissingVoidOutput, err)
	}

	// expect lost collateral address changed
	badPayment = deepCopy(payment)
	badPayment.NewMissedProofOutputs[2].UnlockHash = types.UnlockHash(hash)
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err == nil || !strings.Contains(err.Error(), "lost collateral address was changed") {
		t.Fatalf("Expected lost collaterall error but received '%v'", err)
	}

	// expect renter increased its proof output
	badPayment = deepCopy(payment)
	badPayment.SetValidRenterPayout(curr.ValidRenterPayout().Add64(1))
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err == nil || !strings.Contains(err.Error(), string(ErrHighRenterValidOutput)) {
		t.Fatalf("Expected '%v' but received '%v'", string(ErrHighRenterValidOutput), err)
	}

	// expect an error saying not enough money was transferred
	err = verifyPaymentRevision(curr, payment, height, amount.Add64(1))
	if err == nil || !strings.Contains(err.Error(), string(ErrHighRenterValidOutput)) {
		t.Fatalf("Expected '%v' but received '%v'", string(ErrHighRenterValidOutput), err)
	}
	expectedErrorMsg := fmt.Sprintf("expected at least %v to be exchanged, but %v was exchanged: ", amount.Add64(1), curr.ValidRenterPayout().Sub(payment.ValidRenterPayout()))
	if err == nil || !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Fatalf("Expected '%v' but received '%v'", expectedErrorMsg, err)
	}

	// expect ErrLowHostValidOutput
	badPayment = deepCopy(payment)
	badPayment.SetValidHostPayout(curr.ValidHostPayout().Sub64(1))
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err == nil || !strings.Contains(err.Error(), string(ErrLowHostValidOutput)) {
		t.Fatalf("Expected '%v' but received '%v'", string(ErrLowHostValidOutput), err)
	}

	// expect ErrLowHostValidOutput
	badCurr = deepCopy(curr)
	badCurr.SetValidHostPayout(curr.ValidHostPayout().Sub64(1))
	err = verifyPaymentRevision(badCurr, payment, height, amount)
	if err == nil || !strings.Contains(err.Error(), string(ErrLowHostValidOutput)) {
		t.Fatalf("Expected '%v' but received '%v'", string(ErrLowHostValidOutput), err)
	}

	// expect ErrHighRenterMissedOutput
	badPayment = deepCopy(payment)
	badPayment.SetMissedRenterPayout(payment.MissedRenterOutput().Value.Sub64(1))
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err == nil || !strings.Contains(err.Error(), string(ErrHighRenterMissedOutput)) {
		t.Fatalf("Expected '%v' but received '%v'", string(ErrHighRenterMissedOutput), err)
	}

	// expect ErrLowHostMissedOutput
	badCurr = deepCopy(curr)
	currOut := curr.MissedHostOutput()
	currOut.Value = currOut.Value.Add64(1)
	badCurr.NewMissedProofOutputs[1] = currOut
	err = verifyPaymentRevision(badCurr, payment, height, amount)
	if err == nil || !strings.Contains(err.Error(), string(ErrLowHostMissedOutput)) {
		t.Fatalf("Expected '%v' but received '%v'", string(ErrLowHostMissedOutput), err)
	}

	// expect ErrBadRevisionNumber even if outputs don't match.
	badOutputs = []types.SiacoinOutput{payment.NewMissedProofOutputs[0]}
	badPayment = deepCopy(payment)
	badPayment.NewMissedProofOutputs = badOutputs
	badPayment.NewRevisionNumber--
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadRevisionNumber) {
		t.Fatalf("Expected ErrBadRevisionNumber but received '%v'", err)
	}

	// expect ErrBadParentID
	badPayment = deepCopy(payment)
	badPayment.ParentID = types.FileContractID(hash)
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadParentID) {
		t.Fatalf("Expected ErrBadParentID but received '%v'", err)
	}

	// expect ErrBadUnlockConditions
	badPayment = deepCopy(payment)
	badPayment.UnlockConditions.Timelock = payment.UnlockConditions.Timelock + 1
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadUnlockConditions) {
		t.Fatalf("Expected ErrBadUnlockConditions but received '%v'", err)
	}

	// expect ErrBadFileSize
	badPayment = deepCopy(payment)
	badPayment.NewFileSize = payment.NewFileSize + 1
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadFileSize) {
		t.Fatalf("Expected ErrBadFileSize but received '%v'", err)
	}

	// expect ErrBadFileMerkleRoot
	badPayment = deepCopy(payment)
	badPayment.NewFileMerkleRoot = hash
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadFileMerkleRoot) {
		t.Fatalf("Expected ErrBadFileMerkleRoot but received '%v'", err)
	}

	// expect ErrBadWindowStart
	badPayment = deepCopy(payment)
	badPayment.NewWindowStart = curr.NewWindowStart + 1
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadWindowStart) {
		t.Fatalf("Expected ErrBadWindowStart but received '%v'", err)
	}

	// expect ErrBadWindowEnd
	badPayment = deepCopy(payment)
	badPayment.NewWindowEnd = curr.NewWindowEnd - 1
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadWindowEnd) {
		t.Fatalf("Expected ErrBadWindowEnd but received '%v'", err)
	}

	// expect ErrBadUnlockHash
	badPayment = deepCopy(payment)
	badPayment.NewUnlockHash = types.UnlockHash(hash)
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadUnlockHash) {
		t.Fatalf("Expected ErrBadUnlockHash but received '%v'", err)
	}

	// expect ErrLowHostMissedOutput
	badCurr = deepCopy(curr)
	badCurr.SetMissedHostPayout(payment.MissedHostOutput().Value.Sub64(1))
	err = verifyPaymentRevision(badCurr, payment, height, amount)
	if !errors.Contains(err, ErrLowHostMissedOutput) {
		t.Fatalf("Expected ErrLowHostMissedOutput but received '%v'", err)
	}

	// NOTE: we don't trigger the last check in verifyPaymentRevision which
	// makes sure that the payouts between the revisions match. This is due to
	// the fact that the existing checks around the outputs are so tight, that
	// they will trigger before the payout check does. This essentially makes
	// the payout check redundant, but it's skill kept to be 100% sure.
}
