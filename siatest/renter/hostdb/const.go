package hostdb

import (
	"os"

	"gitlab.com/NebulousLabs/Sia/siatest"
)

// hostdbTestDir creates a temporary testing directory for a hostdb test. This
// should only every be called once per test. Otherwise it will delete the
// directory again.
func hostdbTestDir(testName string) string {
	path := siatest.TestDir("renter/hostdb", testName)
	if err := os.MkdirAll(path, siatest.DefaultDiskPermissions); err != nil {
		panic(err)
	}
	return path
}
