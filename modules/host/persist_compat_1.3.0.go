package host

import (
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/persist"
)

var (
	// v120PersistMetadata is the header of the v120 host persist file.
	v120PersistMetadata = persist.Metadata{
		Header:  "Sia Host",
		Version: "1.2.0",
	}
)

// upgradeFromV120ToV130 is an upgrade layer that aids the integration of the
// SiaMux. Seeing as the SiaMux should use the host's public and private keys,
// we need a version bump to trigger the SiaMux's compatibility flow. If a node
// starts up and we notice the host's persistence is outdated and needs an
// upgrade, we initialize the SiaMux with the host's key pair.
func (h *Host) upgradeFromV120ToV130() error {
	h.log.Println("Attempting an upgrade for the host from v1.2.0 to v1.3.0")

	// Load the persistence object.
	p := new(persistence)
	err := h.dependencies.LoadFile(v120PersistMetadata, p, filepath.Join(h.persistDir, settingsFile))
	if err != nil {
		return build.ExtendErr("could not load persistence object", err)
	}
	h.loadPersistObject(p)

	// Save the updated persist so that the upgrade is not triggered again.
	err = h.saveSync()
	if err != nil {
		return build.ExtendErr("could not save persistence object", err)
	}

	return nil
}
