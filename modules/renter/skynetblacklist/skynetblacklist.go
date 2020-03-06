package skynetblacklist

import (
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// SkynetBlacklist manages a set of blacklisted skylinks by tracking the
// merkleroots and persists the list to disk
type SkynetBlacklist struct {
	merkleroots      map[crypto.Hash]struct{}
	persistLength    int64
	staticPersistDir string

	mu sync.Mutex
}

// New creates a new SkynetBlacklist
func New(persistDir string) (*SkynetBlacklist, error) {
	sb := &SkynetBlacklist{
		merkleroots:      make(map[crypto.Hash]struct{}),
		staticPersistDir: persistDir,
	}

	// Initialize the persistence of the blacklist
	err := sb.callInitPersist()
	if err != nil {
		return nil, errors.AddContext(err, "unable to initialize the skynet blacklist persistence")
	}

	return sb, nil
}

// Blacklist returns the merkleroots that are blacklisted
func (sb *SkynetBlacklist) Blacklist() []crypto.Hash {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	var blacklist []crypto.Hash
	for mr := range sb.merkleroots {
		blacklist = append(blacklist, mr)
	}
	return blacklist
}

// IsBlacklisted indicates if a skylink is currently blacklisted
func (sb *SkynetBlacklist) IsBlacklisted(skylink modules.Skylink) bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	_, ok := sb.merkleroots[skylink.MerkleRoot()]
	return ok
}

// UpdateSkynetBlacklist updates the list of skylinks that are blacklisted
func (sb *SkynetBlacklist) UpdateSkynetBlacklist(additions, removals []modules.Skylink) error {
	return sb.callUpdateAndAppend(additions, removals)
}
