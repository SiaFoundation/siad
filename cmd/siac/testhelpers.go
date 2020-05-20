package main

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/errors"
)

// SubTest is a helper struct for running subtests when tests can use the same
// test http client
type SubTest struct {
	Name string
	Test func(*testing.T, client.Client)
}

// RunSubTests is a helper function to run the subtests when tests can use the
// same test http client
func RunSubTests(t *testing.T, directory string, tests []SubTest) error {
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
		t.Run(test.Name, func(t *testing.T) {
			test.Test(t, n.Client)
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
