package renter

import (
	"time"

	"gitlab.com/NebulousLabs/fastrand"
)

// cooldownUntil returns the next time a job should be attempted given the
// number of consecutive failures in attempting this type of job.
func cooldownUntil(consecutiveFailures uint64) time.Time {
	// Cap the number of consecutive failures to 10.
	//
	// TODO: Could be a const.
	if consecutiveFailures > 10 {
		consecutiveFailures = 10
	}

	// Get a random cooldown time between 0 and 10e3 milliseconds.
	//
	// TODO: Could be a const.
	randCooldown := time.Duration(fastrand.Intn(10e3)) * time.Millisecond
	// Double the cooldown time for each consecutive failure, max possible
	// cooldown time of ~3 hours.
	for i := uint64(0); i < consecutiveFailures; i++ {
		randCooldown *= 2 // TODO: Could be a const
	}
	return time.Now().Add(randCooldown)
}
