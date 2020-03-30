package skynetportals

import (
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// SkynetPortals manages a list of known Skynet portals by persisting the list
// to disk.
type SkynetPortals struct {
	portals          map[modules.NetAddress]bool
	persistLength    int64
	staticPersistDir string

	mu sync.Mutex
}

// New creates a new SkynetPortals.
func New(persistDir string) (*SkynetPortals, error) {
	sp := &SkynetPortals{
		portals:          make(map[modules.NetAddress]bool),
		staticPersistDir: persistDir,
	}

	// Initialize the persistence of the portals list
	err := sp.callInitPersist()
	if err != nil {
		return nil, errors.AddContext(err, "unable to initialize the skynet portal list persistence")
	}

	return sp, nil
}

// Portals returns the list of known Skynet portals.
func (sp *SkynetPortals) Portals() []modules.SkynetPortalInfo {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	var portals []modules.SkynetPortalInfo
	for addr, public := range sp.portals {
		portal := modules.SkynetPortalInfo{
			Address: addr,
			Public:  public,
		}
		portals = append(portals, portal)
	}
	return portals
}

// UpdateSkynetPortals updates the list of known Skynet portals.
func (sp *SkynetPortals) UpdateSkynetPortals(additions []modules.SkynetPortalInfo, removals []modules.NetAddress) error {
	return sp.callUpdateAndAppend(additions, removals)
}
