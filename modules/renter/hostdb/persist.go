package hostdb

import (
	"path/filepath"
	"time"

	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter/hostdb/hosttree"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/types"
)

var (
	// persistFilename defines the name of the file that holds the hostdb's
	// persistence.
	persistFilename = "hostdb.json"

	// persistMetadata defines the metadata that tags along with the most recent
	// version of the hostdb persistence file.
	persistMetadata = persist.Metadata{
		Header:  "HostDB Persistence",
		Version: "0.5",
	}
)

// hdbPersist defines what HostDB data persists across sessions.
type hdbPersist struct {
	AllHosts                 []modules.HostDBEntry
	BlockedDomains           []string
	BlockHeight              types.BlockHeight
	DisableIPViolationsCheck bool
	KnownContracts           map[string]contractInfo
	LastChange               modules.ConsensusChangeID
	FilteredHosts            map[string]types.SiaPublicKey
	FilterMode               modules.FilterMode
}

// persistData returns the data in the hostdb that will be saved to disk.
func (hdb *HostDB) persistData() (data hdbPersist) {
	data.AllHosts = hdb.staticHostTree.All()
	data.BlockedDomains = hdb.blockedDomains.managedBlockedDomains()
	data.BlockHeight = hdb.blockHeight
	data.DisableIPViolationsCheck = hdb.disableIPViolationCheck
	data.KnownContracts = hdb.knownContracts
	data.LastChange = hdb.lastChange
	data.FilteredHosts = hdb.filteredHosts
	data.FilterMode = hdb.filterMode
	return data
}

// saveSync saves the hostdb persistence data to disk and then syncs to disk.
func (hdb *HostDB) saveSync() error {
	return hdb.staticDeps.SaveFileSync(persistMetadata, hdb.persistData(), filepath.Join(hdb.persistDir, persistFilename))
}

// load loads the hostdb persistence data from disk.
func (hdb *HostDB) load() error {
	// Fetch the data from the file.
	var data hdbPersist
	data.FilteredHosts = make(map[string]types.SiaPublicKey)
	err := hdb.staticDeps.LoadFile(persistMetadata, &data, filepath.Join(hdb.persistDir, persistFilename))
	if err != nil {
		return err
	}

	// Set the hostdb internal values.
	hdb.blockHeight = data.BlockHeight
	hdb.disableIPViolationCheck = data.DisableIPViolationsCheck
	hdb.lastChange = data.LastChange
	hdb.knownContracts = data.KnownContracts
	hdb.filteredHosts = data.FilteredHosts
	hdb.filterMode = data.FilterMode

	// Overwrite the initialized staticBlockedDomains with the data loaded
	// from disk
	hdb.blockedDomains = newBlockedDomains(data.BlockedDomains)

	if len(hdb.filteredHosts) > 0 {
		hdb.filteredTree = hosttree.New(hdb.weightFunc, modules.ProdDependencies.Resolver())
	}

	// Load each of the hosts into the host trees.
	for _, host := range data.AllHosts {
		// COMPATv1.1.0
		//
		// The host did not always track its block height correctly, meaning
		// that previously the FirstSeen values and the blockHeight values
		// could get out of sync.
		if hdb.blockHeight < host.FirstSeen {
			host.FirstSeen = hdb.blockHeight
		}

		err := hdb.insert(host)
		if err != nil {
			hdb.staticLog.Debugln("ERROR: could not insert host into hosttree while loading:", host.NetAddress)
		}

		// Make sure that all hosts have gone through the initial scanning.
		if len(host.ScanHistory) < 2 {
			hdb.queueScan(host)
		}
	}
	return nil
}

// threadedSaveLoop saves the hostdb to disk every 2 minutes, also saving when
// given the shutdown signal.
func (hdb *HostDB) threadedSaveLoop() {
	err := hdb.tg.Add()
	if err != nil {
		return
	}
	defer hdb.tg.Done()

	for {
		select {
		case <-hdb.tg.StopChan():
			return
		case <-time.After(saveFrequency):
			hdb.mu.Lock()
			err = hdb.saveSync()
			hdb.mu.Unlock()
			if err != nil {
				hdb.staticLog.Println("Difficulties saving the hostdb:", err)
			}
		}
	}
}
