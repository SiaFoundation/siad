package renter

import (
	"testing"
)

// TestUpdateWorstIgnoredHealth probes the implementation of the
// updateWorstIgnoredHealth function.
func TestUpdateWorstIgnoredHealth(t *testing.T) {
	t.Parallel()

	// Start by updating a blank with blank values.
	var wh worstIgnoredHealth
	wh.updateWorstIgnoredHealth(0, false)
	if wh.health != 0 || wh.remote != false {
		t.Error("bad wh update")
	}
	// Try updating with a higher health, but below the repair threshold.
	wh.updateWorstIgnoredHealth(0.2, false)
	if wh.health != 0 || wh.remote != false {
		t.Error("bad wh update")
	}
	// Try updating with a higher health.
	wh.updateWorstIgnoredHealth(0.4, false)
	if wh.health != 0.4 || wh.remote != false {
		t.Error("bad wh update")
	}
	// Try updating with a lower health.
	wh.updateWorstIgnoredHealth(0.3, false)
	if wh.health != 0.4 || wh.remote != false {
		t.Error("bad wh update")
	}
	// Try updating with a lower health and remote set, but below the repair
	// threshold.
	wh.updateWorstIgnoredHealth(0.1, true)
	if wh.health != 0.4 || wh.remote != false {
		t.Error("bad wh update")
	}
	// Try updating with a lower health and remote set, above repair threshold.
	wh.updateWorstIgnoredHealth(0.3, true)
	if wh.health != 0.3 || wh.remote != true {
		t.Error("bad wh update")
	}
	// Try updating with a lower health and remote set.
	wh.updateWorstIgnoredHealth(0.28, true)
	if wh.health != 0.3 || wh.remote != true {
		t.Error("bad wh update")
	}
	// Try updating with a lower health and remote not set.
	wh.updateWorstIgnoredHealth(0.01, false)
	if wh.health != 0.3 || wh.remote != true {
		t.Error("bad wh update")
	}
	// Try updating with a lower health and remote not set.
	wh.updateWorstIgnoredHealth(0.27, false)
	if wh.health != 0.3 || wh.remote != true {
		t.Error("bad wh update")
	}
	// Try updating with a higher health but remote not set.
	wh.updateWorstIgnoredHealth(1.01, false)
	if wh.health != 0.3 || wh.remote != true {
		t.Error("bad wh update")
	}
	// Try updating with a higher health and remote set.
	wh.updateWorstIgnoredHealth(1.01, true)
	if wh.health != 1.01 || wh.remote != true {
		t.Error("bad wh update")
	}
}

// TestWIHCanSkip checks the logic of the canSkip method.
func TestWIHCanSkip(t *testing.T) {
	t.Parallel()

	// The target is not set, nothing should be skippable unless it is below the
	// repair threshold.
	wh := worstIgnoredHealth{
		health: 0.3,
		remote: false,

		nextDirHealth: 0.5,
		nextDirRemote: false,
	}
	// TEST GROUP A
	if !wh.canSkip(0.2, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.2, true) {
		t.Error("Bad skip")
	}
	if wh.canSkip(0.28, false) {
		t.Error("Bad skip")
	}
	if wh.canSkip(2.1, false) {
		t.Error("Bad skip")
	}
	if wh.canSkip(2.1, true) {
		t.Error("Bad skip")
	}
	if wh.canSkip(0.3, true) {
		t.Error("Bad skip")
	}
	if wh.canSkip(0.3, false) {
		t.Error("Bad skip")
	}

	// Set the target, now should be possible to skip non-remote chunks that are
	// better than nextDirHealth.
	//
	// TEST GROUP B
	wh.target = targetUnstuckChunks
	if !wh.canSkip(0.1, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.1, false) {
		t.Error("Bad skip")
	}
	if wh.canSkip(0.4, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.4, false) {
		t.Error("Bad skip")
	}
	if wh.canSkip(2.1, false) {
		t.Error("Bad skip")
	}
	if wh.canSkip(2.1, true) {
		t.Error("Bad skip")
	}
	if wh.canSkip(0.3, true) {
		t.Error("Bad skip")
	}

	// Set the remote health to true. Should be able to skip anything false, and
	// anything under 0.3 health.
	//
	// TEST GROUP C
	wh.remote = true
	if !wh.canSkip(0.1, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.1, false) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.28, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.28, false) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(2.1, false) {
		t.Error("Bad skip")
	}
	if wh.canSkip(2.1, true) {
		t.Error("Bad skip")
	}
	if wh.canSkip(0.31, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.3, false) {
		t.Error("Bad skip")
	}

	// Set the next dir to remote, now should be possible to skip remote chunks
	// that are better than wh, and non-remote chunks at any health.
	//
	// TEST GROUP D
	wh.nextDirRemote = true
	if !wh.canSkip(0.1, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.1, false) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.48, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.4, false) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(2.1, false) {
		t.Error("Bad skip")
	}
	if wh.canSkip(2.1, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.3, true) {
		t.Error("Bad skip")
	}

	// Flip the roles of next dir and worst ignored from test group B for test
	// group E. Results should be the same.
	//
	// TEST GROUP E
	wh.health = 0.5
	wh.remote = false
	wh.nextDirHealth = 0.3
	wh.nextDirRemote = false
	if !wh.canSkip(0.1, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.1, false) {
		t.Error("Bad skip")
	}
	if wh.canSkip(0.4, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.4, false) {
		t.Error("Bad skip")
	}
	if wh.canSkip(2.1, false) {
		t.Error("Bad skip")
	}
	if wh.canSkip(2.1, true) {
		t.Error("Bad skip")
	}
	if wh.canSkip(0.3, true) {
		t.Error("Bad skip")
	}

	// Flip the roles of next dir and worst ignored from test group C for test
	// group F. Results should be the same.
	//
	// TEST GROUP F
	wh.health = 0.5
	wh.remote = false
	wh.nextDirHealth = 0.3
	wh.nextDirRemote = true
	if !wh.canSkip(0.1, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.1, false) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.28, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.28, false) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(2.1, false) {
		t.Error("Bad skip")
	}
	if wh.canSkip(2.1, true) {
		t.Error("Bad skip")
	}
	if wh.canSkip(0.31, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.3, false) {
		t.Error("Bad skip")
	}

	// Flip the roles of next dir and worst ignored from test group D for test
	// group G. Results should be the same.
	//
	// TEST GROUP G
	wh.health = 0.5
	wh.remote = true
	wh.nextDirHealth = 0.3
	wh.nextDirRemote = true
	if !wh.canSkip(0.1, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.1, false) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.48, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.4, false) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(2.1, false) {
		t.Error("Bad skip")
	}
	if wh.canSkip(2.1, true) {
		t.Error("Bad skip")
	}
	if !wh.canSkip(0.3, true) {
		t.Error("Bad skip")
	}
}
