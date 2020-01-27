package hostdb

import (
	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/hostdb/hosttree"
	"gitlab.com/NebulousLabs/Sia/types"
)

// RandomHosts implements the HostDB interface's RandomHosts() method. It takes
// a number of hosts to return, and a slice of netaddresses to ignore, and
// returns a slice of entries. If the IP violation check was disabled, the
// addressBlacklist is ignored.
func (hdb *HostDB) RandomHosts(n int, blacklist, addressBlacklist []types.SiaPublicKey) ([]modules.HostDBEntry, error) {
	hdb.mu.RLock()
	initialScanComplete := hdb.initialScanComplete
	ipCheckDisabled := hdb.disableIPViolationCheck
	hdb.mu.RUnlock()
	if !initialScanComplete {
		return []modules.HostDBEntry{}, ErrInitialScanIncomplete
	}
	if ipCheckDisabled {
		return hdb.filteredTree.SelectRandom(n, blacklist, nil), nil
	}
	return hdb.filteredTree.SelectRandom(n, blacklist, addressBlacklist), nil
}

// RandomHostsWithAllowance works as RandomHosts but uses a temporary hosttree
// created from the specified allowance. This is a very expensive call and
// should be used with caution.
func (hdb *HostDB) RandomHostsWithAllowance(n int, blacklist, addressBlacklist []types.SiaPublicKey, allowance modules.Allowance) ([]modules.HostDBEntry, error) {
	hdb.mu.RLock()
	initialScanComplete := hdb.initialScanComplete
	filteredHosts := hdb.filteredHosts
	filterType := hdb.filterMode
	hdb.mu.RUnlock()
	if !initialScanComplete {
		return []modules.HostDBEntry{}, ErrInitialScanIncomplete
	}
	// Create a temporary hosttree from the given allowance.
	ht := hosttree.New(hdb.managedCalculateHostWeightFn(allowance), hdb.deps.Resolver())

	// Insert all known hosts.
	hdb.mu.RLock()
	defer hdb.mu.RUnlock()
	var insertErrs error
	allHosts := hdb.hostTree.All()
	isWhitelist := filterType == modules.HostDBActiveWhitelist
	for _, host := range allHosts {
		// Filter out listed hosts
		_, ok := filteredHosts[host.PublicKey.String()]
		if isWhitelist != ok {
			continue
		}
		if err := ht.Insert(host); err != nil {
			insertErrs = errors.Compose(insertErrs, err)
		}
	}

	// Select hosts from the temporary hosttree.
	return ht.SelectRandom(n, blacklist, addressBlacklist), insertErrs
}
