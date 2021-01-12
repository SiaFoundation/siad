package main

import "testing"

// TestSanitizeSkylinks probes the helper function sanitizeSkylinks
func TestSanitizeSkylinks(t *testing.T) {
	t.Parallel()

	in := []string{"", "sia://", "sia://link", "sia:link", "sia:/link", "sia//link", "link"}
	out := []string{"", "", "link", "sia:link", "sia:/link", "sia//link", "link"}

	results := sanitizeSkylinks(in)
	for i, r := range results {
		if r != out[i] {
			t.Errorf("result `%v`, expected `%v`", r, out[i])
		}
	}
}
