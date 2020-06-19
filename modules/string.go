package modules

import (
	"fmt"
	"strings"
)

// ensurePrefix checks if `str` starts with `prefix` and adds it if that's not
// the case.
func ensurePrefix(str, prefix string) string {
	if strings.HasPrefix(str, prefix) {
		return str
	}
	return fmt.Sprintf("%s%s", prefix, str)
}

// ensureSuffix checks if `str` ends with `prefix` and adds it if that's not
// the case.
func ensureSuffix(str, suffix string) string {
	if strings.HasSuffix(str, suffix) {
		return str
	}
	return fmt.Sprintf("%s%s", str, suffix)
}
