package renter

import (
	"context"
	"testing"
)

// TestRenewContract is a unit test for the worker's RenewContract method.
func TestRenewContract(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a disabled worker. That way no background thread will interfere.
	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Renew the contract.
	err = wt.RenewContract(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// TODO: Check if contract was renewed.

	// Close the worker.
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}
}
