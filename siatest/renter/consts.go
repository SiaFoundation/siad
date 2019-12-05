package renter

import (
	"os"

	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/siatest"
)

// renterTestDir creates a temporary testing directory for a renter test. This
// should only every be called once per test. Otherwise it will delete the
// directory again.
func renterTestDir(testName string) string {
	path := siatest.TestDir("renter", testName)
	if err := os.MkdirAll(path, persist.DefaultDiskPermissionsTest); err != nil {
		panic(err)
	}
	return path
}

// fuseTestDir creates a temporary testing directory for a fuse test. This
// should only every be called once per test. Otherwise it will delete the
// directory again.
func fuseTestDir(testName string) string {
	path := siatest.TestDir("fuse", testName)
	if err := os.MkdirAll(path, persist.DefaultDiskPermissionsTest); err != nil {
		panic(err)
	}
	return path
}
