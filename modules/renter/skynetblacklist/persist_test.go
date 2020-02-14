package skynetblacklist

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// TestPersist tests the persistence of the SkynetBlacklist
func TestPersist(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Creat a new SkynetBlacklist
	testdir := build.TempDir("skynetblacklist", t.Name())
	sb, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// There should be no skylinks in the blacklist
	if len(sb.skylinks) != 0 {
		t.Fatal("Expected blacklist to be empty but found:", len(sb.skylinks))
	}

	// Update blacklist
	var skylink modules.Skylink
	add := []modules.Skylink{skylink}
	remove := []modules.Skylink{skylink}
	err = sb.UpdateSkynetBlacklist(add, remove)
	if err != nil {
		t.Fatal(err)
	}

	// There should be no skylinks in the blacklist because we added and then
	// removed the same skylink
	if len(sb.skylinks) != 0 {
		t.Fatal("Expected blacklist to be empty but found:", len(sb.skylinks))
	}

	// Add the skylink again
	err = sb.UpdateSkynetBlacklist(add, []modules.Skylink{})
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 skylink listed now
	if len(sb.skylinks) != 1 {
		t.Fatal("Expected 1 blacklisted skylink but found:", len(sb.skylinks))
	}
	sl, ok := sb.skylinks[skylink]
	if !ok {
		t.Fatalf("Expected skylink listed in blacklist to be %v but found %v", skylink, sl)
	}

	// Load a new Skynet Blacklist to verify the contents from disk get loaded
	// properly
	sb2, err := New(testdir)
	if err != nil {
		t.Fatal(err)
	}

	// There should be 1 skylink listed now
	if len(sb2.skylinks) != 1 {
		t.Fatal("Expected 1 blacklisted skylink but found:", len(sb2.skylinks))
	}
	sl, ok = sb.skylinks[skylink]
	if !ok {
		t.Fatalf("Expected skylink listed in blacklist to be %v but found %v", skylink, sl)
	}
}
