package renter

import (
	"testing"
	"time"
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
