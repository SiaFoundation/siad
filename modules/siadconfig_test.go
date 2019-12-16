package modules

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/persist"
)

// TestSiadConfigPersistCompat confirms that the compat code for the writebps
// json tag update performs as expected
func TestSiadConfigPersistCompat(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create siadconfig
	testDir := build.TempDir("siadconfig", t.Name())
	if err := os.MkdirAll(testDir, persist.DefaultDiskPermissionsTest); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(testDir, ConfigName)
	sc, err := NewConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	// Set the old rate limit field
	sc.WriteBPSDeprecated = 100

	// Save
	err = sc.save()
	if err != nil {
		t.Fatal(err)
	}

	// Load
	err = sc.load(path)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that the new field is set and the old field is zeroed out
	if sc.WriteBPS != 100 {
		t.Fatal("Expected WriteBPS to be set to 100 on load but got:", sc.WriteBPS)
	}
	if sc.WriteBPSDeprecated != 0 {
		t.Fatal("Expected WriteBPSDeprecated to be reset to 0 but got:", sc.WriteBPSDeprecated)
	}

	// Confirm that set limits sets the new field and not the old field
	err = sc.SetRatelimit(200, 200)
	if err != nil {
		t.Fatal(err)
	}
	if sc.WriteBPS != 200 {
		t.Fatal("Expected WriteBPS to be set to 200 but got:", sc.WriteBPS)
	}
	if sc.WriteBPSDeprecated != 0 {
		t.Fatal("Expected WriteBPSDeprecated not to be set but got:", sc.WriteBPSDeprecated)
	}

	// Calling Load should have no impact now
	err = sc.load(path)
	if err != nil {
		t.Fatal(err)
	}
	if sc.WriteBPS != 200 {
		t.Fatal("Expected WriteBPS to be set to 200 but got:", sc.WriteBPS)
	}
	if sc.WriteBPSDeprecated != 0 {
		t.Fatal("Expected WriteBPSDeprecated not to be set but got:", sc.WriteBPSDeprecated)
	}
}
