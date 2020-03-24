package main

import (
	"testing"
)

// TestFileHealthSummary is a regression test to check for panics
func TestFileHealthSummary(t *testing.T) {
	// Test with empty slice
	var dirs []directoryInfo
	renterFileHealthSummary(dirs)

	// Test with empty struct
	dirs = append(dirs, directoryInfo{})
	renterFileHealthSummary(dirs)
}
