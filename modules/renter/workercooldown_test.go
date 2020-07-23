package renter

import (
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/errors"
)

// TestCooldownUntil checks that the cooldownUntil function is working as
// expected.
func TestCooldownUntil(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	iters := 400

	// Run some statistical tests to ensure that the cooldowns are following the
	// pattern we expect.
	maxCooldown := time.Duration(cooldownBaseMaxMilliseconds) * time.Millisecond
	for i := uint64(0); i < cooldownMaxConsecutiveFailures; i++ {
		// Try iters times, this should give a good distribution.
		var longestCD time.Duration
		var totalCD time.Duration
		for j := 0; j < iters; j++ {
			cdu := cooldownUntil(i)
			cd := cdu.Sub(time.Now())
			if cd > longestCD {
				longestCD = cd
			}
			totalCD += cd
		}
		// Check that the cooldown is never too long.
		if longestCD > maxCooldown {
			t.Error("cooldown is lasting too long")
		}
		// Check that the average cooldown is in a reasonable range.
		expectedCD := maxCooldown * time.Duration(iters) / 2
		rangeLow := expectedCD * 4 / 5
		rangeHigh := expectedCD * 6 / 5
		if totalCD < rangeLow || totalCD > rangeHigh {
			t.Error("cooldown does not match statistical expectations", rangeLow, rangeHigh, totalCD)
		}

		// As we increase the number of consecutive failures, the expected
		// cooldown increases.
		maxCooldown *= 2
	}

	// Run the same statistical tests, now when having more than the max number
	// of consecutive failures. This function notably stops increasing the max
	// cooldown.
	for i := uint64(cooldownMaxConsecutiveFailures); i < cooldownMaxConsecutiveFailures*2; i++ {
		// Try iters times, this should give a good distribution.
		var longestCD time.Duration
		var totalCD time.Duration
		for j := 0; j < iters; j++ {
			cdu := cooldownUntil(i)
			cd := cdu.Sub(time.Now())
			if cd > longestCD {
				longestCD = cd
			}
			totalCD += cd
		}
		// Check that the cooldown is never too long.
		if longestCD > maxCooldown {
			t.Error("cooldown is lasting too long")
		}
		// Check that the average cooldown is in a reasonable range.
		expectedCD := maxCooldown * time.Duration(iters) / 2
		rangeLow := expectedCD * 4 / 5
		rangeHigh := expectedCD * 6 / 5
		if totalCD < rangeLow || totalCD > rangeHigh {
			t.Error("cooldown does not match statistical expectations", rangeLow, rangeHigh, totalCD)
		}
	}
}

// TestWorkerRHP3CoolDownn verifies the functionality of the worker's cooldown
// of the RHP3 related subsystems.
func TestWorkerRHP3CoolDown(t *testing.T) {
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

	// check the balance in a retry to allow the worker to run through it's
	// setup, e.g. updating PT, checking balance and refilling. Note we use min
	// expected balance to ensure we're not counting pending deposits
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		if !w.staticAccount.managedMinExpectedBalance().Equals(w.staticBalanceTarget) {
			return errors.New("worker account not funded")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// verify the worker is not on an RHP3 cooldown
	if w.managedRHP3OnCooldown() {
		t.Fatal("Unexpected RHP3 cooldown")
	}

	// set a negative balance, tricking the worker into thinking it has to
	// refill
	w.staticAccount.mu.Lock()
	w.staticAccount.negativeBalance = w.staticAccount.balance
	w.staticAccount.mu.Unlock()

	// manually trigger a refill
	w.managedRefillAccount()

	// verify the worker's RHP3 systems have been put on a cooldown
	if !w.managedRHP3OnCooldown() {
		t.Fatal("Expected RHP3 cooldown")
	}

	// verify recent error and consecutive failures are set
	w.mu.Lock()
	cf := w.rhp3ConsecutiveFailures
	re := w.rhp3RecentErr
	ret := w.rhp3RecentErrTime
	w.mu.Unlock()
	if cf == 0 {
		t.Fatal("Consecutive failures should be larger than zero")
	}
	if re == nil {
		t.Fatal("Recent Error should be set")
	}
	if (ret == time.Time{}) {
		t.Fatal("Recent Error Time should be set")
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
	cc := make(chan struct{})
	rc := make(chan *jobHasSectorResponse)
	jhs := w.newJobHasSector(cc, rc, crypto.Hash{})
	for i := 0; i < 100; i++ {
		if !w.staticJobHasSectorQueue.callAdd(jhs) {
			t.Fatal("could not add job to queue")
		}
	}

	// manually trigger a refill
	w.managedRefillAccount()

	// check if 'consecutiveFailures' has been reset to 0
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		w.mu.Lock()
		cf := w.rhp3ConsecutiveFailures
		w.mu.Unlock()
		if cf != 0 {
			return errors.New("consecutive failures not reset")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// verify the account is not on cooldown
	if w.managedRHP3OnCooldown() {
		t.Fatal("Worker's RHP3 subsystems should not be on cooldown")
	}
}
