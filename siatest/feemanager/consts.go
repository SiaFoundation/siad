package feemanager

import (
	"os"

	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/siatest"
)

// feeManagerTestDir creates a temporary testing directory for a feemanager
// test. This should only every be called once per test. Otherwise it will
// delete the directory again.
func feeManagerTestDir(testName string) string {
	path := siatest.TestDir("feemanager", testName)
	if err := os.MkdirAll(path, persist.DefaultDiskPermissionsTest); err != nil {
		panic(err)
	}
	return path
}
