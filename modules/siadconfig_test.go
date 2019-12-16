package modules

import (
	"fmt"
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

	// Test setting deprecated field only
	err = saveLoadCheck(sc, 0, 100)
	if err != nil {
		t.Fatal(err)
	}

	// Test setting both fields
	err = saveLoadCheck(sc, 150, 250)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that set limits sets the new field and not the old field
	err = sc.SetRatelimit(0, 200)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkWriteBPS(sc, 200); err != nil {
		t.Fatal(err)
	}

	// Calling Load should have no impact now
	err = sc.load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkWriteBPS(sc, 200); err != nil {
		t.Fatal(err)
	}
}

// saveLoadCheck is a helper to check saving and loading the siad config file
// and verifying the correct values for the WriteBPS fields
func saveLoadCheck(sc *SiadConfig, writeBPS, writeBPSDeprepacted int64) error {
	// Set both fields
	sc.WriteBPSDeprecated = writeBPSDeprepacted
	sc.WriteBPS = writeBPS

	// Save
	err := sc.save()
	if err != nil {
		return err
	}

	// Load
	err = sc.load(sc.path)
	if err != nil {
		return err
	}

	// Confirm that the new field is set and the old field is zeroed out
	expectedValue := writeBPS
	if expectedValue == 0 {
		expectedValue = writeBPSDeprepacted
	}
	return checkWriteBPS(sc, expectedValue)
}

// checkWriteBPS is a helper to check the WriteBPS and WriteBPSDeprecated fields
func checkWriteBPS(sc *SiadConfig, writeBPS int64) error {
	if sc.WriteBPS != writeBPS {
		return fmt.Errorf("Expected WriteBPS to be set to %v on load but got: %v", writeBPS, sc.WriteBPS)
	}
	if sc.WriteBPSDeprecated != 0 {
		return fmt.Errorf("Expected WriteBPSDeprecated to be reset to 0 but got: %v", sc.WriteBPSDeprecated)
	}
	return nil
}
