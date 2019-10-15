package contractor

import (
	"gitlab.com/NebulousLabs/Sia/persist"
)

// stdPersist implements the persister interface. The filename required by
// these functions is internal to stdPersist.
type stdPersist struct {
	filename string
}

var persistMeta = persist.Metadata{
	Header:  "Contractor Persistence",
	Version: "1.3.1",
}
