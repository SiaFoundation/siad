package types

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

// NewSpecifier returns a specifier for given name, a specifier can only be 16
// bytes so we panic if the given name is too long.
func NewSpecifier(name string) Specifier {
	if len(name) > 16 {
		panic("ERROR: specifier max length exceeded")
	}
	var s Specifier
	copy(s[:], name)
	return s
}
