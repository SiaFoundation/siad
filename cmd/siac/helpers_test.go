package main

import (
	"os"
	"testing"

	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/errors"
)

// subTest is a helper struct for running subtests when tests can use the same
// test http client
type subTest struct {
	name string
	test func(*testing.T, client.Client)
}

// runSubTests is a helper function to run the subtests when tests can use the
// same test http client
func runSubTests(t *testing.T, directory string, tests []subTest) error {
	// Create a test node/client for this test group
	n, err := newTestNode(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Run subtests
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.test(t, n.Client)
		})
	}
	return nil
}

// newTestNode creates a new Sia node for a test
func newTestNode(dir string) (*siatest.TestNode, error) {
	n, err := siatest.NewNode(node.AllModules(dir))
	if err != nil {
		return nil, errors.AddContext(err, "Error creating a new test node")
	}
	return n, nil
}

// siacTestDir creates a temporary Sia testing directory for a cmd/siac test,
// removing any files or directories that previously existed at that location.
// This should only every be called once per test. Otherwise it will delete the
// directory again.
func siacTestDir(testName string) string {
	path := siatest.TestDir("cmd/siac", testName)
	if err := os.MkdirAll(path, persist.DefaultDiskPermissionsTest); err != nil {
		panic(err)
	}
	return path
}
