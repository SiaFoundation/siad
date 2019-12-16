package types

import (
	"fmt"
	"unicode/utf8"
)

// SpecifierLen is the length in bytes of a Specifier.
const SpecifierLen = 16

// A Specifier is a fixed-length byte-array that serves two purposes. In
// the wire protocol, they are used to identify a particular encoding
// algorithm, signature algorithm, etc. This allows nodes to communicate on
// their own terms; for example, to reduce bandwidth costs, a node might
// only accept compressed messages.
//
// Internally, Specifiers are used to guarantee unique IDs. Various
// consensus types have an associated ID, calculated by hashing the data
// contained in the type. By prepending the data with Specifier, we can
// guarantee that distinct types will never produce the same hash.
type Specifier [SpecifierLen]byte

// specifierMap is used for tracking unique specifiers
var specifierMap = newSpecifierMap()

// NewSpecifier returns a specifier for given name, a specifier can only be 16
// bytes so we panic if the given name is too long.
func NewSpecifier(name string) Specifier {
	if !isASCII(name) {
		panic("ERROR: specifier has to be ASCII")
	}
	if len(name) > SpecifierLen {
		panic("ERROR: specifier max length exceeded")
	}
	if _, ok := specifierMap[name]; ok {
		err := fmt.Sprint("ERROR: specifier name already in use", name)
		panic(err)
	}
	specifierMap[name] = struct{}{}
	var s Specifier
	copy(s[:], name)
	return s
}

// isASCII returns whether or not the given string contains only ASCII
// characters
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// newSpecifierMap makes a new map for tracking specifiers
func newSpecifierMap() map[string]struct{} {
	return make(map[string]struct{})
}
