package siatest

import (
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// Fuzz returns 0, 1 or -1. This can be used to test for random off-by-one
// errors in the code. For example fuzz can be used to create a File that is
// either sector aligned or off-by-one.
func Fuzz() int {
	// Intn(3) creates a number of the set [0,1,2]. By subtracting 1 we end up
	// with a number of the set [-1,0,1].
	return fastrand.Intn(3) - 1
}

// SubTest is a helper struct for running subtests when tests can use the same
// test group
type SubTest struct {
	Name string
	Test func(*testing.T, *TestGroup)
}

// RunSubTests is a helper function to run the subtests when tests can use the
// same test group
func RunSubTests(t *testing.T, params GroupParams, directory string, tests []SubTest) error {
	tg, err := NewGroupFromTemplate(directory, params)
	if err != nil {
		return errors.AddContext(err, "failed to create group")
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Run subtests
	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			test.Test(t, tg)
		})
	}
	return nil
}

// Retry will call 'fn' 'tries' times, waiting 'durationBetweenAttempts'
// between each attempt, returning 'nil' the first time that 'fn' returns nil.
// If 'nil' is never returned, then the final error returned by 'fn' is
// returned.
func Retry(tries int, durationBetweenAttempts time.Duration, fn func() error) (err error) {
	for i := 1; i < tries; i++ {
		err = fn()
		if err == nil {
			return nil
		}
		time.Sleep(durationBetweenAttempts)
	}
	return fn()
}
