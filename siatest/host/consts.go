package host

import (
	"os"

	"gitlab.com/NebulousLabs/Sia/siatest"
)

// hostTestDir creates a temporary testing directory for a host. This should
// only every be called once per test. Otherwise it will delete the directory
// again.
func hostTestDir(testName string) string {
	path := siatest.TestDir("host", testName)
	if err := os.MkdirAll(path, 0777); err != nil {
		panic(err)
	}
	return path
}
