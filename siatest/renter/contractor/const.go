package contractor

import (
	"os"

	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/siatest"
)

// contractorTestDir creates a temporary testing directory for a contractor
// test. This should only every be called once per test. Otherwise it will
// delete the directory again.
func contractorTestDir(testName string) string {
	path := siatest.TestDir("renter/contractor", testName)
	if err := os.MkdirAll(path, persist.DefaultDiskPermissions); err != nil {
		panic(err)
	}
	return path
}
