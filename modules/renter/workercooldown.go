package renter

import (
	"time"

	"gitlab.com/NebulousLabs/fastrand"
)

const (
	// cooldownMaxConsecutiveFailures defines the maximum number of consecutive
	// failures that will be considered when determining how long a worker
	// should be on cooldown.
	cooldownMaxConsecutiveFailures = 10

	// cooldownBaseMaxMilliseconds defines the maximum number of milliseconds
	// that a worker will go on cooldown for if they have 0 consecutive
	// failures. We use thousands of milliseconds instead of full seconds
	// because we use a random number generator to pick a random number of
	// milliseconds between 0 and max, and we want to have more granularity.
	cooldownBaseMaxMilliseconds = 10e3

	// cooldownBaseMinMilliseconds sets a minimum amount of time that a worker
	// will go on cooldown.
	cooldownBaseMinMilliseconds = 1e3
)

// cooldownUntil returns the next time a job should be attempted given the
// number of consecutive failures in attempting this type of job.
func cooldownUntil(consecutiveFailures uint64) time.Time {
	// Cap the number of consecutive failures to 10.
	if consecutiveFailures > cooldownMaxConsecutiveFailures {
		consecutiveFailures = cooldownMaxConsecutiveFailures
	}

	// Get a random cooldown time between 1e3 and 10e3 milliseconds.
	randMs := fastrand.Intn(cooldownBaseMaxMilliseconds - cooldownBaseMinMilliseconds)
	randMs += cooldownBaseMinMilliseconds
	randCooldown := time.Duration(randMs) * time.Millisecond
	// Double the cooldown time for each consecutive failure, max possible
	// cooldown time of ~3 hours.
	for i := uint64(0); i < consecutiveFailures; i++ {
		randCooldown *= 2
	}
	return time.Now().Add(randCooldown)
}
