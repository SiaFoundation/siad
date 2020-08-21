package renter

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/siatest"

	"os"
	"path/filepath"
	"strings"

	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
)

// TestSkynetSkylinkHandlerGET tests the behaviour of SkynetSkylinkHandlerGET
// when it handles different combinations of metadata and content. These tests
// use the fixtures in `testdata/skylink_fixtures.json`.
func TestSkynetSkylinkHandlerGET(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:  3,
		Miners: 1,
	}
	testDir := siatest.TestDir("renter", t.Name())
	if err := os.MkdirAll(testDir, persist.DefaultDiskPermissionsTest); err != nil {
		t.Fatal(err)
	}
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Add a Renter node.
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams.RenterDeps = &dependencies.DependencyResolveSkylinkToFixture{}
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	r := nodes[0]
	defer func() { _ = tg.RemoveNode(r) }()

	subTests := []siatest.SubTest{
		{Name: "ValidSkyfile", Test: testSkyfileValid},
		{Name: "SingleFileDefaultPath", Test: testSkyfileSingleFileDefaultPath},
	}

	// Run the tests.
	for _, test := range subTests {
		t.Run(test.Name, func(t *testing.T) {
			test.Test(t, tg)
		})
	}
}

// testSkyfileValid is the happy path, ensuring that we don't get errors on
// valid data.
func testSkyfileValid(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]
	_, _, err := r.SkynetSkylinkGet("_A6d-2CpM2OQ-7m5NPAYW830NdzC3wGydFzzd-KnHXhwJA")
	if err != nil {
		t.Fatal(err)
	}
}

// testSkyfileSingleFileDefaultPath ensures that we return an error if a single
// file has a `defaultpath` field.
func testSkyfileSingleFileDefaultPath(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]
	_, _, err := r.SkynetSkylinkGet("3AAcCO73xMbehYaK7bjDGCtW0GwOL6Swl-lNY52Pb_APzA")
	if err == nil || !strings.Contains(err.Error(), "defaultpath is not allowed on single files") {
		t.Fatalf("Expected error 'defaultpath is not allowed on single files', got %+v\n", err)
	}
}
