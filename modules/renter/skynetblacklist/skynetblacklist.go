package skynetblacklist

import (
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// SkynetBlacklist manages a set of blacklisted skylinks and persists the list to disk
type SkynetBlacklist struct {
	skylinks         map[modules.Skylink]struct{}
	staticPersistDir string

	mu sync.Mutex
}

// New creates a new SkynetBlacklist
func New(persistDir string) (*SkynetBlacklist, error) {
	sb := &SkynetBlacklist{
		skylinks:         make(map[modules.Skylink]struct{}),
		staticPersistDir: persistDir,
	}

	// Initialize the persistence of the blacklist
	err := sb.initPersist()
	if err != nil {
		return nil, err
	}

	return sb, nil
}

// Blacklisted indicates if a skylink is currently blacklisted
func (sb *SkynetBlacklist) Blacklisted(skylink modules.Skylink) bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	_, ok := sb.skylinks[skylink]
	return ok
}

// UpdateSkynetBlacklist updates the list of skylinks that are blacklisted
func (sb *SkynetBlacklist) UpdateSkynetBlacklist(additions, removals []modules.Skylink) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	return sb.update(additions, removals)
}
