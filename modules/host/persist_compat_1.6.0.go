package host

import (
	"math"
	"path/filepath"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/host/registry"
)

// upgradeFromV151ToV160 is an upgrade layer that fixes registry entries being
// persisted without a valid type. This will cause the registry to be loaded
// with the repair flag set and closed again to sync the changes to disk.
func (h *Host) upgradeFromV151ToV160(registryPath string) error {
	h.log.Println("Attempting an upgrade for the host from v1.5.1 to v1.6.0")

	// Load the persistence object
	p := new(persistence)
	err := h.dependencies.LoadFile(modules.Hostv151PersistMetadata, p, filepath.Join(h.persistDir, settingsFile))
	if err != nil {
		return errors.AddContext(err, "could not load persistence object")
	}

	// Repair the registry.
	r, err := registry.New(registryPath, math.MaxUint64, true, h.publicKey)
	if err != nil {
		return errors.AddContext(err, "failed to repair registry")
	}
	if err := r.Close(); err != nil {
		return errors.AddContext(err, "failed to close repaired registry")
	}

	// Save the updated persist so that the upgrade is not triggered again.
	err = h.saveSync()
	if err != nil {
		return errors.AddContext(err, "could not save persistence object")
	}

	return nil
}
