package proto

import (
	"net"
	"testing"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/encoding"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestNegotiateRevisionStopResponse tests that when the host sends
// StopResponse, the renter continues processing the revision instead of
// immediately terminating.
func TestNegotiateRevisionStopResponse(t *testing.T) {
	// simulate a renter-host connection
	rConn, hConn := net.Pipe()

	// handle the host's half of the pipe
	go func(c net.Conn) {
		defer func() {
			if err := c.Close(); err != nil {
				t.Error(err)
			}
		}()
		// read revision
		encoding.ReadObject(hConn, new(types.FileContractRevision), 1<<22)
		// write acceptance
		modules.WriteNegotiationAcceptance(hConn)
		// read txn signature
		encoding.ReadObject(hConn, new(types.TransactionSignature), 1<<22)
		// write StopResponse
		modules.WriteNegotiationStop(hConn)
		// write txn signature
		encoding.WriteObject(hConn, types.TransactionSignature{})
	}(hConn)

	// since the host wrote StopResponse, we should proceed to validating the
	// transaction. This will return a known error because we are supplying an
	// empty revision.
	_, err := negotiateRevision(rConn, types.FileContractRevision{}, crypto.SecretKey{}, 0)
	if !errors.Contains(err, types.ErrFileContractWindowStartViolation) {
		t.Fatalf("expected %q, got \"%v\"", types.ErrFileContractWindowStartViolation, err)
	}
	rConn.Close()

	// same as above, but send an error instead of StopResponse. The error
	// should be returned by negotiateRevision immediately (if it is not, we
	// should expect to see a transaction validation error instead).
	rConn, hConn = net.Pipe()
	go func(c net.Conn) {
		defer func() {
			if err := c.Close(); err != nil {
				t.Error(err)
			}
		}()
		encoding.ReadObject(hConn, new(types.FileContractRevision), 1<<22)
		modules.WriteNegotiationAcceptance(hConn)
		encoding.ReadObject(hConn, new(types.TransactionSignature), 1<<22)
		// write a sentinel error
		modules.WriteNegotiationRejection(hConn, errors.New("sentinel"))
		encoding.WriteObject(hConn, types.TransactionSignature{})
	}(hConn)
	expectedErr := "host did not accept transaction signature: sentinel"
	_, err = negotiateRevision(rConn, types.FileContractRevision{}, crypto.SecretKey{}, 0)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("expected %q, got \"%v\"", expectedErr, err)
	}
	rConn.Close()
}

// TestNewRevisionFundChecks checks that underflow errors1
func TestNewRevisionFundChecks(t *testing.T) {
	// helper func for revisions
	revWithValues := func(renterFunds, hostCollateralAvailable uint64) types.FileContractRevision {
		validOuts := make([]types.SiacoinOutput, 2)
		missedOuts := make([]types.SiacoinOutput, 3)

		// funds remaining for renter, and payout to host.
		validOuts[0].Value = types.NewCurrency64(renterFunds)
		validOuts[1].Value = types.NewCurrency64(0)

		// Void payout from renter
		missedOuts[0].Value = types.NewCurrency64(renterFunds)

		// Collateral
		missedOuts[1].Value = types.NewCurrency64(hostCollateralAvailable)

		return types.FileContractRevision{
			NewValidProofOutputs:  validOuts,
			NewMissedProofOutputs: missedOuts,
		}
	}

	// Cost is less than renter funds should be okay.
	_, err := newDownloadRevision(revWithValues(100, 0), types.NewCurrency64(99))
	if err != nil {
		t.Fatal(err)
	}
	// Cost equal to renter funds should be okay.
	_, err = newDownloadRevision(revWithValues(100, 0), types.NewCurrency64(100))
	if err != nil {
		t.Fatal(err)
	}
	// Cost is more than renter funds should fail.
	_, err = newDownloadRevision(revWithValues(100, 0), types.NewCurrency64(101))
	if !errors.Contains(err, types.ErrRevisionCostTooHigh) {
		t.Fatal(err)
	}

	// Collateral checks (in each, renter funds <= cost)
	//
	// Cost less than collateral should be okay.
	_, err = newUploadRevision(revWithValues(100, 100), crypto.Hash{}, types.NewCurrency64(99), types.NewCurrency64(99))
	if err != nil {
		t.Fatal(err)
	}
	// Using up all collateral should be okay.
	_, err = newUploadRevision(revWithValues(100, 100), crypto.Hash{}, types.NewCurrency64(99), types.NewCurrency64(100))
	if err != nil {
		t.Fatal(err)
	}
	// Not enough collateral should cause an error.
	_, err = newUploadRevision(revWithValues(100, 100), crypto.Hash{}, types.NewCurrency64(99), types.NewCurrency64(100))
	if errors.Contains(err, types.ErrRevisionCollateralTooLow) {
		t.Fatal(err)
	}
}
