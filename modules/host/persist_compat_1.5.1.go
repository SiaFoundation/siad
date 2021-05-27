package host

import (
	"path/filepath"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/modules"
)

// upgradeFromV143ToV151 is an upgrade layer that fixes a bug in the host's
// settings which got introduced when EphemeralAccountExpiry got moved from a
// uint64 to a time.Duration. At that time, no compat was added, resulting in a
// persist value in seconds, that is being interpreted as nanoseconds.
func (h *Host) upgradeFromV143ToV151() error {
	h.log.Println("Attempting an upgrade for the host from v1.4.3 to v1.5.1")

	// Load the persistence object
	p := new(persistence)
	err := h.dependencies.LoadFile(modules.Hostv143PersistMetadata, p, filepath.Join(h.persistDir, settingsFile))
	if err != nil {
		return errors.AddContext(err, "could not load persistence object")
	}

	// The persistence object for hosts that upgraded to v1.5.0 (so non-new
	// hosts) will have the EphemeralAccountExpiry persisted in seconds, and
	// wrongfully interpreted as a time.Duration, expressed in nanoseconds.
	//
	// We fix this by updating the field to the default value, but only if the
	// value in the persistence object wasn't manually altered to zero
	if shouldResetEphemeralAccountExpiry(p.Settings) {
		p.Settings.EphemeralAccountExpiry = modules.DefaultEphemeralAccountExpiry // reset to default
	}

	// Load it on the host
	h.loadPersistObject(p)

	// Save the updated persist so that the upgrade is not triggered again.
	err = h.saveSync()
	if err != nil {
		return errors.AddContext(err, "could not save persistence object")
	}

	return nil
}

// shouldResetEphemeralAccountExpiry is a helper function that returns true if
// the given settings contain a value for the `EphemeralAccountExpiry` field
// that needs to be reset. Extracted for unit testing purposes.
func shouldResetEphemeralAccountExpiry(his modules.HostInternalSettings) bool {
	return his.EphemeralAccountExpiry != modules.CompatV1412DefaultEphemeralAccountExpiry && his.EphemeralAccountExpiry.Nanoseconds() != 0
}
