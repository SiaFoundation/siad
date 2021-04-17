package renter

import (
	"context"
	"math"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/siatest/dependencies"
	"go.sia.tech/siad/types"
)

// TestWorkerMaintenanceCoolDown verifies the functionality of the worker's
// cooldown of the RHP3 related subsystems.
func TestWorkerMaintenanceCoolDown(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	wt, err := newWorkerTesterCustomDependency(t.Name(), &dependencies.DependencyDisableCriticalOnMaxBalance{}, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := wt.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
	w := wt.worker

	// wait until the worker is done with its maintenance tasks - this basically
	// ensures we have a working worker, with valid PT and funded EA
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		if !w.managedMaintenanceSucceeded() {
			return errors.New("worker not ready with maintenance")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// verify the worker is not on an maintenance cooldown
	if w.managedOnMaintenanceCooldown() {
		t.Fatal("Unexpected maintenance cooldown")
	}

	// set a negative balance, tricking the worker into thinking it has to
	// refill
	w.staticAccount.mu.Lock()
	w.staticAccount.negativeBalance = w.staticAccount.balance
	w.staticAccount.mu.Unlock()

	// manually trigger a refill
	w.managedRefillAccount()

	// verify the worker has been put on maintenance cooldown
	if !w.managedOnMaintenanceCooldown() {
		t.Fatal("Expected maintenance cooldown")
	}

	// the workerloop should have synced the account balance
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		w.staticAccount.mu.Lock()
		defer w.staticAccount.mu.Unlock()
		if !w.staticAccount.negativeBalance.IsZero() {
			return errors.New("worker account balance not reset")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// run a couple of has sector jobs to spend money
	ctx := context.Background()
	rc := make(chan *jobHasSectorResponse)
	jhs := w.newJobHasSector(ctx, rc, crypto.Hash{})
	for i := 0; i < 100; i++ {
		if !w.staticJobHasSectorQueue.callAdd(jhs) {
			t.Fatal("could not add job to queue")
		}
	}

	// manually trigger a refill
	w.managedRefillAccount()

	// verify the account is not on cooldown
	if w.managedOnMaintenanceCooldown() {
		t.Fatal("Worker's RHP3 subsystems should not be on cooldown")
	}
}

// TestWorkerMaintenanceRefillLowContractFunds verifies that a contract with
// less remaining funds than the EA balance target can still be used to refill
// an EA.
func TestWorkerMaintenanceRefillLowContractFunds(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	deps := &dependencies.DependencyDisableWorker{}
	wt, err := newWorkerTesterCustomDependency(t.Name(), deps, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := wt.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	// allow for a large balance on the host.
	is := wt.host.InternalSettings()
	is.MaxEphemeralAccountBalance = types.SiacoinPrecision.Mul64(math.MaxUint64)
	err = wt.host.SetInternalSettings(is)
	if err != nil {
		t.Fatal(err)
	}

	w := wt.worker

	// fetch a pricetable.
	w.staticUpdatePriceTable()

	// balance should be 0 right now.
	w.staticAccount.mu.Lock()
	accountBalance := w.staticAccount.balance
	w.staticAccount.mu.Unlock()
	if !accountBalance.IsZero() {
		t.Fatal("balance should be zero at beginning of test")
	}

	// check remaining balance on contract.
	contract, ok := w.renter.hostContractor.ContractByPublicKey(wt.staticHostPubKey)
	if !ok {
		t.Fatal("contract not found")
	}
	funds := contract.RenterFunds

	// set the target to the balance.
	w.staticBalanceTarget = funds

	// trigger a refill.
	w.managedRefillAccount()

	// check if the balance increased.
	w.staticAccount.mu.Lock()
	accountBalance = w.staticAccount.balance
	w.staticAccount.mu.Unlock()
	expectedBalance := funds.Sub(wt.staticPriceTable().staticPriceTable.FundAccountCost)
	if !accountBalance.Equals(expectedBalance) {
		t.Fatalf("expected balance %v but got %v", accountBalance, expectedBalance)
	}
}
