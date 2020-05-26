package renter

import (
	"testing"
)

// TestUpdateWorstIgnoredHealth probes the implementation of the
// updateWorstIgnoredHealth function.
func TestUpdateWorstIgnoredHealth(t *testing.T) {
	// Start by updating a blank with blank values.
	var wih worstIgnoredHealth
	wih.updateWorstIgnoredHealth(0, false)
	if wih.health != 0 || wih.remote != false {
		t.Error("bad wih update")
	}
	// Try updating with a higher health.
	wih.updateWorstIgnoredHealth(0.2, false)
	if wih.health != 0.2 || wih.remote != false {
		t.Error("bad wih update")
	}
	// Try updating with a lower health.
	wih.updateWorstIgnoredHealth(0.1, false)
	if wih.health != 0.2 || wih.remote != false {
		t.Error("bad wih update")
	}
	// Try updating with a lower health and remote set.
	wih.updateWorstIgnoredHealth(0.1, true)
	if wih.health != 0.1 || wih.remote != true {
		t.Error("bad wih update")
	}
	// Try updating with a lower health and remote set.
	wih.updateWorstIgnoredHealth(0.01, true)
	if wih.health != 0.1 || wih.remote != true {
		t.Error("bad wih update")
	}
	// Try updating with a lower health and remote not set.
	wih.updateWorstIgnoredHealth(0.01, false)
	if wih.health != 0.1 || wih.remote != true {
		t.Error("bad wih update")
	}
	// Try updating with a higher health but remote not set.
	wih.updateWorstIgnoredHealth(1.01, false)
	if wih.health != 0.1 || wih.remote != true {
		t.Error("bad wih update")
	}
	// Try updating with a higher health and remote set.
	wih.updateWorstIgnoredHealth(1.01, true)
	if wih.health != 1.01 || wih.remote != true {
		t.Error("bad wih update")
	}
}

// TestWIHCanSkip checks the logic of the canSkip method.
func TestWIHCanSkip(t *testing.T) {
	// The target is not set, nothing should be skippable.
	wih := worstIgnoredHealth{
		health: 0.2,
		remote: false,

		nextDirHealth: 0.5,
		nextDirRemote: false,
	}
	// TEST GROUP A
	if wih.canSkip(0.1, true) {
		t.Error("Bad skip")
	}
	if wih.canSkip(0.1, false) {
		t.Error("Bad skip")
	}
	if wih.canSkip(2.1, false) {
		t.Error("Bad skip")
	}
	if wih.canSkip(2.1, true) {
		t.Error("Bad skip")
	}
	if wih.canSkip(0.3, true) {
		t.Error("Bad skip")
	}
	if wih.canSkip(0.3, false) {
		t.Error("Bad skip")
	}

	// Set the target, now should be possible to skip non-remote chunks that are
	// better than nextDirHealth.
	//
	// TEST GROUP B
	wih.target = targetUnstuckChunks
	if wih.canSkip(0.1, true) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(0.1, false) {
		t.Error("Bad skip")
	}
	if wih.canSkip(2.1, false) {
		t.Error("Bad skip")
	}
	if wih.canSkip(2.1, true) {
		t.Error("Bad skip")
	}
	if wih.canSkip(0.3, true) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(0.3, false) {
		t.Error("Bad skip")
	}

	// Set the remote health to true. Should be able to skip anything false, and
	// anything under 0.2 health.
	//
	// TEST GROUP C
	wih.remote = true
	if !wih.canSkip(0.1, true) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(0.1, false) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(2.1, false) {
		t.Error("Bad skip")
	}
	if wih.canSkip(2.1, true) {
		t.Error("Bad skip")
	}
	if wih.canSkip(0.3, true) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(0.3, false) {
		t.Error("Bad skip")
	}

	// Set the next dir to remote, now should be possible to skip remote chunks
	// that are better than wih, and non-remote chunks at any health.
	//
	// TEST GROUP D
	wih.nextDirRemote = true
	if !wih.canSkip(0.1, true) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(0.1, false) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(2.1, false) {
		t.Error("Bad skip")
	}
	if wih.canSkip(2.1, true) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(0.3, true) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(0.3, false) {
		t.Error("Bad skip")
	}

	// Flip the roles of next dir and worst ignored from test group B for test
	// group E. Results should be the same.
	//
	// TEST GROUP E
	wih.health = 0.5
	wih.remote = false
	wih.nextDirHealth = 0.2
	wih.nextDirRemote = false
	if wih.canSkip(0.1, true) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(0.1, false) {
		t.Error("Bad skip")
	}
	if wih.canSkip(2.1, false) {
		t.Error("Bad skip")
	}
	if wih.canSkip(2.1, true) {
		t.Error("Bad skip")
	}
	if wih.canSkip(0.3, true) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(0.3, false) {
		t.Error("Bad skip")
	}

	// Flip the roles of next dir and worst ignored from test group C for test
	// group F. Results should be the same.
	//
	// TEST GROUP F
	wih.health = 0.5
	wih.remote = false
	wih.nextDirHealth = 0.2
	wih.nextDirRemote = true
	if !wih.canSkip(0.1, true) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(0.1, false) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(2.1, false) {
		t.Error("Bad skip")
	}
	if wih.canSkip(2.1, true) {
		t.Error("Bad skip")
	}
	if wih.canSkip(0.3, true) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(0.3, false) {
		t.Error("Bad skip")
	}

	// Flip the roles of next dir and worst ignored from test group D for test
	// group G. Results should be the same.
	//
	// TEST GROUP G
	wih.health = 0.5
	wih.remote = true
	wih.nextDirHealth = 0.2
	wih.nextDirRemote = false
	wih.nextDirRemote = true
	if !wih.canSkip(0.1, true) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(0.1, false) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(2.1, false) {
		t.Error("Bad skip")
	}
	if wih.canSkip(2.1, true) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(0.3, true) {
		t.Error("Bad skip")
	}
	if !wih.canSkip(0.3, false) {
		t.Error("Bad skip")
	}
}
