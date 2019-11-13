package renter

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestAddUnigueBubblePaths probes the addUniqueBubblePaths function
func TestAddUniqueBubblePaths(t *testing.T) {
	// Create some directory tree paths
	paths := []modules.SiaPath{
		modules.RootSiaPath(),
		{Path: "root"},
		{Path: "root/SubDir1"},
		{Path: "root/SubDir1/SubDir1"},
		{Path: "root/SubDir1/SubDir1/SubDir1"},
		{Path: "root/SubDir1/SubDir2"},
		{Path: "root/SubDir2"},
		{Path: "root/SubDir2/SubDir1"},
		{Path: "root/SubDir2/SubDir2"},
		{Path: "root/SubDir2/SubDir2/SubDir2"},
	}

	// Create a map of directories to be bubbled
	dirsToBubble := newUniqueBubblePaths()

	// Add all paths to map
	for _, path := range paths {
		dirsToBubble.addPath(path)
	}

	// No randomly add more paths
	for i := 0; i < 10; i++ {
		dirsToBubble.addPath(paths[fastrand.Intn(len(paths))])
	}

	// There should only be the following paths in the map
	uniquePaths := []modules.SiaPath{
		{Path: "root/SubDir1/SubDir1/SubDir1"},
		{Path: "root/SubDir1/SubDir2"},
		{Path: "root/SubDir2/SubDir1"},
		{Path: "root/SubDir2/SubDir2/SubDir2"},
	}
	if len(dirsToBubble.childDirs) != len(uniquePaths) {
		t.Fatalf("Expected %v paths in map but got %v", len(uniquePaths), len(dirsToBubble.childDirs))
	}
	for _, path := range uniquePaths {
		if _, ok := dirsToBubble.childDirs[path]; !ok {
			t.Fatal("Did not find path in map", path)
		}
	}
}
