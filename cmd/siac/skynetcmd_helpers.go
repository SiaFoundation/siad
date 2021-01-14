package main

import "strings"

// sanitizeSkylinks will trim away `sia://` from skylinks
func sanitizeSkylinks(links []string) []string {
	var result []string

	for _, link := range links {
		trimmed := strings.TrimPrefix(link, "sia://")
		result = append(result, trimmed)
	}

	return result
}
