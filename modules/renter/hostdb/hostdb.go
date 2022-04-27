// Package hostdb provides a HostDB object that implements the renter.hostDB
// interface. The blockchain is scanned for host announcements and hosts that
// are found get added to the host database. The database continually scans the
// set of hosts it has found and updates who is online.
package hostdb

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
	"gitlab.com/NebulousLabs/threadgroup"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter/hostdb/hosttree"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/types"
)

var (
	// ErrInitialScanIncomplete is returned whenever an operation is not
	// allowed to be executed before the initial host scan has finished.
	ErrInitialScanIncomplete = errors.New("initial hostdb scan is not yet completed")
	errNilCS                 = errors.New("cannot create hostdb with nil consensus set")
	errNilGateway            = errors.New("cannot create hostdb with nil gateway")
	errNilTPool              = errors.New("cannot create hostdb with nil transaction pool")
	errNilSiaMux             = errors.New("cannot create hostdb with nil siamux")

	// errHostNotFoundInTree is returned when the host is not found in the
	// hosttree
	errHostNotFoundInTree = errors.New("host not found in hosttree")
)

// blockedDomains manages a list of blocked domains
type blockedDomains struct {
	domains  map[string]struct{}
	resolver net.Resolver
	mu       sync.Mutex
}

// newBlockedDomains initializes a new blockedDomains
func newBlockedDomains(domains []string) *blockedDomains {
	domainMap := make(map[string]struct{})
	for _, domain := range domains {
		domainMap[domain] = struct{}{}
	}
	return &blockedDomains{
		domains: domainMap,
	}
}

// managedAddDomains adds domains to the map of blocked domains
func (bd *blockedDomains) managedAddDomains(domains []string) {
	bd.mu.Lock()
	defer bd.mu.Unlock()
	for _, domain := range domains {
		bd.domains[domain] = struct{}{}

		addrs, err := bd.resolver.LookupHost(context.Background(), domain)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			bd.domains[addr] = struct{}{}
		}
	}
}

// managedBlockedDomains returns a list of the blocked domains
func (bd *blockedDomains) managedBlockedDomains() []string {
	bd.mu.Lock()
	defer bd.mu.Unlock()
	var domains []string
	for domain := range bd.domains {
		domains = append(domains, domain)
	}

	return domains
}

// managedRemoveDomains removes domains from the map of blocked domains
func (bd *blockedDomains) managedRemoveDomains(domains []string) {
	bd.mu.Lock()
	defer bd.mu.Unlock()
	for _, domain := range domains {
		delete(bd.domains, domain)
	}
}

// managedIsBlocked checks to see if the domain is blocked
func (bd *blockedDomains) managedIsBlocked(addr modules.NetAddress) bool {
	// Grab the hostname
	hostname := addr.Host()

	// Address without the proper host:port format can't be blocked. This is
	// usually a testing address so we will just ignore it.
	if hostname == "" {
		return false
	}

	// Check the full hostname
	bd.mu.Lock()
	defer bd.mu.Unlock()
	_, blocked := bd.domains[hostname]

	// Return if blocked or if the hostname is localhost or if it is an ipv6
	// address
	isIPV6 := strings.Contains(hostname, ":")
	if blocked || hostname == "localhost" || isIPV6 {
		return blocked
	}

	ip := net.ParseIP(string(hostname))
	if ip != nil {
		for domain := range bd.domains {
			_, ipnet, _ := net.ParseCIDR(domain)
			if ipnet != nil && ipnet.Contains(ip) {
				return true
			}
		}
	}

	// Check for subdomains being blocked by a root domain
	//
	// Split the hostname into elements
	elements := strings.Split(hostname, ".")
	if len(elements) <= 1 {
		return blocked
	}

	// Check domains
	//
	// We want to stop at the second to last element so that the last domain
	// we check is of the format domain.com. This is to protect the user
	// from accidentally submitted `com`, or some other TLD, and blocking
	// every host in the hostdb.
	for i := 0; i < len(elements)-1; i++ {
		domainToCheck := strings.Join(elements[i:], ".")
		_, blocked := bd.domains[domainToCheck]
		if blocked {
			return true
		}
	}
	return false
}

// contractInfo contains information about a contract relevant to the HostDB.
type contractInfo struct {
	HostPublicKey types.SiaPublicKey
	StoredData    uint64 `json:"storeddata"`
}

// The HostDB is a database of potential hosts. It assigns a weight to each
// host based on their hosting parameters, and then can select hosts at random
// for uploading files.
type HostDB struct {
	// dependencies
	cs          modules.ConsensusSet
	staticDeps  modules.Dependencies
	gateway     modules.Gateway
	staticMux   *siamux.SiaMux
	staticTpool modules.TransactionPool

	staticLog     *persist.Logger
	mu            sync.RWMutex
	staticAlerter *modules.GenericAlerter
	persistDir    string
	tg            threadgroup.ThreadGroup

	// knownContracts are contracts which the HostDB was informed about by the
	// Contractor. It contains infos about active contracts we have formed with
	// hosts. The mapkey is a serialized SiaPublicKey.
	knownContracts map[string]contractInfo

	// The hostdb gets initialized with an allowance that can be modified. The
	// allowance is used to build a weightFunc that the hosttree depends on to
	// determine the weight of a host.
	allowance  modules.Allowance
	weightFunc hosttree.WeightFunc

	// txnFees are the most recent fees used in the score estimation. It is
	// used to determine if the transaction fees have changed enough to warrant
	// rebuilding the hosttree with an updated weight function.
	txnFees types.Currency

	// The staticHostTree is the root node of the tree that organizes hosts by
	// weight. The tree is necessary for selecting weighted hosts at random.
	staticHostTree *hosttree.HostTree

	// the scanPool is a set of hosts that need to be scanned. There are a
	// handful of goroutines constantly waiting on the channel for hosts to
	// scan. The scan map is used to prevent duplicates from entering the scan
	// pool.
	initialScanComplete     bool
	initialScanLatencies    []time.Duration
	disableIPViolationCheck bool
	scanList                []modules.HostDBEntry
	scanMap                 map[string]struct{}
	scanWait                bool
	scanningThreads         int
	synced                  bool

	// staticFilteredTree is a hosttree that only contains the hosts that align
	// with the filterMode. The filteredHosts are the hosts that are submitted
	// with the filterMode to determine which host should be in the
	// staticFilteredTree
	filteredTree  *hosttree.HostTree
	filteredHosts map[string]types.SiaPublicKey
	filterMode    modules.FilterMode

	// blockedDomains tracks blocked domains for the hostdb.
	blockedDomains *blockedDomains

	blockHeight types.BlockHeight
	lastChange  modules.ConsensusChangeID
}

// Enforce that HostDB satisfies the modules.HostDB interface.
var _ modules.HostDB = (*HostDB)(nil)

// insert inserts the HostDBEntry into both hosttrees
func (hdb *HostDB) insert(host modules.HostDBEntry) error {
	err := hdb.staticHostTree.Insert(host)
	_, ok := hdb.filteredHosts[host.PublicKey.String()]
	isWhitelist := hdb.filterMode == modules.HostDBActiveWhitelist
	if isWhitelist == ok {
		errF := hdb.filteredTree.Insert(host)
		if errF != nil && errF != hosttree.ErrHostExists {
			err = errors.Compose(err, errF)
		}
	}
	return err
}

// modify modifies the HostDBEntry in both hosttrees
func (hdb *HostDB) modify(host modules.HostDBEntry) error {
	err := hdb.staticHostTree.Modify(host)
	_, ok := hdb.filteredHosts[host.PublicKey.String()]
	isWhitelist := hdb.filterMode == modules.HostDBActiveWhitelist
	if isWhitelist == ok {
		err = errors.Compose(err, hdb.filteredTree.Modify(host))
	}
	return err
}

// remove removes the HostDBEntry from both hosttrees
func (hdb *HostDB) remove(pk types.SiaPublicKey) error {
	err := hdb.staticHostTree.Remove(pk)
	_, ok := hdb.filteredHosts[pk.String()]
	isWhitelist := hdb.filterMode == modules.HostDBActiveWhitelist
	if isWhitelist == ok {
		errF := hdb.filteredTree.Remove(pk)
		if err == nil && errF == hosttree.ErrNoSuchHost {
			return nil
		}
		err = errors.Compose(err, errF)
	}
	return err
}

// managedSetWeightFunction is a helper function that sets the weightFunc field
// of the hostdb and also updates the the weight function used by the hosttrees
// by rebuilding them. Apart from the constructor of the hostdb, this method
// should be used to update the weight function in the hostdb and hosttrees.
func (hdb *HostDB) managedSetWeightFunction(wf hosttree.WeightFunc) error {
	// Set the weight function in the hostdb.
	hdb.mu.Lock()
	defer hdb.mu.Unlock()
	hdb.weightFunc = wf
	// Update the hosttree and also the filteredTree if they are not the same.
	err := hdb.staticHostTree.SetWeightFunction(wf)
	if hdb.filteredTree != hdb.staticHostTree {
		err = errors.Compose(err, hdb.filteredTree.SetWeightFunction(wf))
	}
	return err
}

// managedSynced returns true if the hostdb is synced with the consensusset.
func (hdb *HostDB) managedSynced() bool {
	hdb.mu.RLock()
	defer hdb.mu.RUnlock()
	return hdb.synced
}

// updateContracts rebuilds the knownContracts of the HostDB using the provided
// contracts.
func (hdb *HostDB) updateContracts(contracts []modules.RenterContract) {
	// Build a new set of known contracts.
	knownContracts := make(map[string]contractInfo)
	for _, contract := range contracts {
		if n := len(contract.Transaction.FileContractRevisions); n != 1 {
			build.Critical("contract's transaction should contain 1 revision but had ", n)
			continue
		}
		knownContracts[contract.HostPublicKey.String()] = contractInfo{
			HostPublicKey: contract.HostPublicKey,
			StoredData:    contract.Transaction.FileContractRevisions[0].NewFileSize,
		}
	}

	// Update the set of known contracts in the hostdb, log if the number of
	// contracts has decreased.
	if len(hdb.knownContracts) > len(knownContracts) {
		hdb.staticLog.Printf("Hostdb is decreasing from %v known contracts to %v known contracts", len(hdb.knownContracts), len(knownContracts))
	}
	hdb.knownContracts = knownContracts

	// Save the hostdb to persist the update.
	err := hdb.saveSync()
	if err != nil {
		hdb.staticLog.Println("Error saving set of known contracts:", err)
	}
}

// hostdbBlockingStartup handles the blocking portion of NewCustomHostDB.
func hostdbBlockingStartup(g modules.Gateway, cs modules.ConsensusSet, tpool modules.TransactionPool, siamux *siamux.SiaMux, persistDir string, deps modules.Dependencies) (*HostDB, error) {
	// Check for nil inputs.
	if g == nil {
		return nil, errNilGateway
	}
	if cs == nil {
		return nil, errNilCS
	}
	if tpool == nil {
		return nil, errNilTPool
	}
	if siamux == nil {
		return nil, errNilSiaMux
	}

	// Create the HostDB object.
	hdb := &HostDB{
		cs:          cs,
		staticDeps:  deps,
		gateway:     g,
		persistDir:  persistDir,
		staticMux:   siamux,
		staticTpool: tpool,

		blockedDomains: newBlockedDomains(nil),
		filteredHosts:  make(map[string]types.SiaPublicKey),
		knownContracts: make(map[string]contractInfo),
		scanMap:        make(map[string]struct{}),
		staticAlerter:  modules.NewAlerter("hostdb"),
	}

	// Set the allowance, txnFees and hostweight function.
	hdb.allowance = modules.DefaultAllowance
	_, hdb.txnFees = hdb.staticTpool.FeeEstimation()
	hdb.weightFunc = hdb.managedCalculateHostWeightFn(hdb.allowance)

	// Create the persist directory if it does not yet exist.
	err := os.MkdirAll(persistDir, 0700)
	if err != nil {
		return nil, err
	}

	// Create the logger.
	logger, err := persist.NewFileLogger(filepath.Join(persistDir, "hostdb.log"))
	if err != nil {
		return nil, err
	}
	hdb.staticLog = logger
	err = hdb.tg.AfterStop(func() error {
		if err := hdb.staticLog.Close(); err != nil {
			// Resort to println as the logger is in an uncertain state.
			fmt.Println("Failed to close the hostdb logger:", err)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// The host tree is used to manage hosts and query them at random. The
	// filteredTree is used when whitelist or blacklist is enabled
	hdb.staticHostTree = hosttree.New(hdb.weightFunc, deps.Resolver())
	hdb.filteredTree = hdb.staticHostTree

	// Load the prior persistence structures.
	hdb.mu.Lock()
	err = hdb.load()
	hdb.mu.Unlock()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	err = hdb.tg.AfterStop(func() error {
		hdb.mu.Lock()
		err := hdb.saveSync()
		hdb.mu.Unlock()
		if err != nil {
			hdb.staticLog.Println("Unable to save the hostdb:", err)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Loading is complete, establish the save loop.
	go hdb.threadedSaveLoop()

	// Don't perform the remaining startup in the presence of a quitAfterLoad
	// disruption.
	if hdb.staticDeps.Disrupt("quitAfterLoad") {
		return hdb, nil
	}

	// COMPATv1.4.7
	// Before version v1.4.7, the hostdb sometimes removed hosts from the host
	// tree even if it was known that the contractor had contracts with them. To
	// fix this, we must initiate a rescan if we don't have a knownContract's host
	// in the hostdb.
	hdb.mu.Lock()
	knownContractHosts := make([]types.SiaPublicKey, 0, len(hdb.knownContracts))
	for _, contractInfo := range hdb.knownContracts {
		knownContractHosts = append(knownContractHosts, contractInfo.HostPublicKey)
	}
	hdb.mu.Unlock()
	compatV147ForceRescan := false
	for _, host := range knownContractHosts {
		_, exists := hdb.staticHostTree.Select(host)
		if !exists {
			compatV147ForceRescan = true
			break
		}
	}

	// COMPATv1.1.0
	//
	// If the block height has loaded as zero, the most recent consensus change
	// needs to be set to perform a full rescan. This will also help the hostdb
	// to pick up any hosts that it has incorrectly dropped in the past.
	hdb.mu.Lock()
	if hdb.blockHeight == 0 || compatV147ForceRescan {
		hdb.lastChange = modules.ConsensusChangeBeginning
		hdb.blockHeight = 0
	}
	hdb.mu.Unlock()

	// Spawn the scan loop during production, but allow it to be disrupted
	// during testing. Primary reason is so that we can fill the hostdb with
	// fake hosts and not have them marked as offline as the scanloop operates.
	if !hdb.staticDeps.Disrupt("disableScanLoop") {
		go hdb.threadedScan()
	} else {
		hdb.initialScanComplete = true
	}
	err = hdb.tg.OnStop(func() error {
		cs.Unsubscribe(hdb)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return hdb, nil
}

// hostdbAsyncStartup handles the async portion of NewCustomHostDB.
func hostdbAsyncStartup(hdb *HostDB, cs modules.ConsensusSet) error {
	if hdb.staticDeps.Disrupt("BlockAsyncStartup") {
		return nil
	}
	err := cs.ConsensusSetSubscribe(hdb, hdb.lastChange, hdb.tg.StopChan())
	if err != nil && strings.Contains(err.Error(), threadgroup.ErrStopped.Error()) {
		return err
	}
	if errors.Contains(err, modules.ErrInvalidConsensusChangeID) {
		// Subscribe again using the new ID. This will cause a triggered scan
		// on all of the hosts, but that should be acceptable.
		hdb.mu.Lock()
		hdb.blockHeight = 0
		hdb.lastChange = modules.ConsensusChangeBeginning
		hdb.mu.Unlock()
		err = cs.ConsensusSetSubscribe(hdb, hdb.lastChange, hdb.tg.StopChan())
	}
	if err != nil && strings.Contains(err.Error(), threadgroup.ErrStopped.Error()) {
		return nil
	}
	if err != nil {
		return err
	}
	return nil
}

// New returns a new HostDB.
func New(g modules.Gateway, cs modules.ConsensusSet, tpool modules.TransactionPool, siamux *siamux.SiaMux, persistDir string) (*HostDB, <-chan error) {
	// Create HostDB using production dependencies.
	return NewCustomHostDB(g, cs, tpool, siamux, persistDir, modules.ProdDependencies)
}

// NewCustomHostDB creates a HostDB using the provided dependencies. It loads the old
// persistence data, spawns the HostDB's scanning threads, and subscribes it to
// the consensusSet.
func NewCustomHostDB(g modules.Gateway, cs modules.ConsensusSet, tpool modules.TransactionPool, siamux *siamux.SiaMux, persistDir string, deps modules.Dependencies) (*HostDB, <-chan error) {
	errChan := make(chan error, 1)

	// Blocking startup.
	hdb, err := hostdbBlockingStartup(g, cs, tpool, siamux, persistDir, deps)
	if err != nil {
		errChan <- err
		return nil, errChan
	}
	// Parts of the blocking startup and the whole async startup should be
	// skipped.
	if hdb.staticDeps.Disrupt("quitAfterLoad") {
		close(errChan)
		return hdb, errChan
	}
	// non-blocking startup.
	go func() {
		defer close(errChan)
		if err := hdb.tg.Add(); err != nil {
			errChan <- err
			return
		}
		defer hdb.tg.Done()
		// Subscribe to the consensus set in a separate goroutine.
		err := hostdbAsyncStartup(hdb, cs)
		if err != nil {
			errChan <- err
		}
	}()
	return hdb, errChan
}

// ActiveHosts returns a list of hosts that are currently online, sorted by
// weight. If hostdb is in black or white list mode, then only active hosts from
// the filteredTree will be returned
func (hdb *HostDB) ActiveHosts() (activeHosts []modules.HostDBEntry, err error) {
	if err = hdb.tg.Add(); err != nil {
		return activeHosts, err
	}
	defer hdb.tg.Done()

	hdb.mu.RLock()
	allHosts := hdb.filteredTree.All()
	hdb.mu.RUnlock()
	for _, entry := range allHosts {
		if len(entry.ScanHistory) == 0 {
			continue
		}
		if !entry.ScanHistory[len(entry.ScanHistory)-1].Success {
			continue
		}
		if !entry.AcceptingContracts {
			continue
		}
		activeHosts = append(activeHosts, entry)
	}
	return activeHosts, err
}

// AllHosts returns all of the hosts known to the hostdb, including the inactive
// ones. AllHosts is not filtered by blacklist or whitelist mode.
func (hdb *HostDB) AllHosts() (allHosts []modules.HostDBEntry, err error) {
	if err := hdb.tg.Add(); err != nil {
		return allHosts, err
	}
	defer hdb.tg.Done()
	hdb.mu.RLock()
	defer hdb.mu.RUnlock()
	return hdb.staticHostTree.All(), nil
}

// CheckForIPViolations accepts a number of host public keys and returns the
// ones that violate the rules of the addressFilter.
func (hdb *HostDB) CheckForIPViolations(hosts []types.SiaPublicKey) ([]types.SiaPublicKey, error) {
	if err := hdb.tg.Add(); err != nil {
		return nil, err
	}
	defer hdb.tg.Done()
	// If the check was disabled we don't return any bad hosts.
	hdb.mu.RLock()
	defer hdb.mu.RUnlock()
	disabled := hdb.disableIPViolationCheck
	if disabled {
		return nil, nil
	}

	var entries []modules.HostDBEntry
	var badHosts []types.SiaPublicKey

	// Get the entries which correspond to the keys.
	for _, host := range hosts {
		entry, exists := hdb.staticHostTree.Select(host)
		if !exists {
			// A host that's not in the hostdb is bad.
			badHosts = append(badHosts, host)
			continue
		}
		entries = append(entries, entry)
	}

	// Sort the entries by the amount of time they have occupied their
	// corresponding subnets. This is the order in which they will be passed
	// into the filter which prioritizes entries which are passed in earlier.
	// That means 'younger' entries will be replaced in case of a violation.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LastIPNetChange.Before(entries[j].LastIPNetChange)
	})

	// Create a filter and apply it.
	filter := hosttree.NewFilter(hdb.staticDeps.Resolver())
	for _, entry := range entries {
		// Check if the host violates the rules.
		if filter.Filtered(entry.NetAddress) {
			badHosts = append(badHosts, entry.PublicKey)
			continue
		}
		// If it didn't then we add it to the filter.
		filter.Add(entry.NetAddress)
	}
	return badHosts, nil
}

// Close closes the hostdb, terminating its scanning threads
func (hdb *HostDB) Close() error {
	return hdb.tg.Stop()
}

// Host returns the HostSettings associated with the specified pubkey. If no
// matching host is found, Host returns false.  For black and white list modes,
// the Filtered field for the HostDBEntry is set to indicate it the host is
// being filtered from the filtered hosttree
func (hdb *HostDB) Host(spk types.SiaPublicKey) (modules.HostDBEntry, bool, error) {
	if err := hdb.tg.Add(); err != nil {
		return modules.HostDBEntry{}, false, errors.AddContext(err, "error adding hostdb threadgroup:")
	}
	defer hdb.tg.Done()

	hdb.mu.Lock()
	whitelist := hdb.filterMode == modules.HostDBActiveWhitelist
	filteredHosts := hdb.filteredHosts
	hdb.mu.Unlock()
	host, exists := hdb.staticHostTree.Select(spk)
	if !exists {
		return host, exists, errHostNotFoundInTree
	}
	_, ok := filteredHosts[spk.String()]
	host.Filtered = whitelist != ok
	hdb.mu.RLock()
	updateHostHistoricInteractions(&host, hdb.blockHeight)
	hdb.mu.RUnlock()
	return host, exists, nil
}

// Filter returns the hostdb's filterMode and filteredHosts
func (hdb *HostDB) Filter() (modules.FilterMode, map[string]types.SiaPublicKey, []string, error) {
	if err := hdb.tg.Add(); err != nil {
		return modules.HostDBFilterError, nil, nil, errors.AddContext(err, "error adding hostdb threadgroup:")
	}
	defer hdb.tg.Done()

	hdb.mu.RLock()
	defer hdb.mu.RUnlock()
	filteredHosts := make(map[string]types.SiaPublicKey)
	for k, v := range hdb.filteredHosts {
		filteredHosts[k] = v
	}
	return hdb.filterMode, filteredHosts, hdb.blockedDomains.managedBlockedDomains(), nil
}

// SetFilterMode sets the hostdb filter mode
func (hdb *HostDB) SetFilterMode(fm modules.FilterMode, hosts []types.SiaPublicKey, netAddresses []string) error {
	if err := hdb.tg.Add(); err != nil {
		return errors.AddContext(err, "error adding hostdb threadgroup:")
	}
	defer hdb.tg.Done()
	hdb.mu.Lock()
	defer hdb.mu.Unlock()

	// Check for error
	if fm == modules.HostDBFilterError {
		return errors.New("Cannot set hostdb filter mode, provided filter mode is an error")
	}
	// Check if disabling
	if fm == modules.HostDBDisableFilter {
		// Reset filtered field for hosts
		for _, pk := range hdb.filteredHosts {
			err := hdb.staticHostTree.SetFiltered(pk, false)
			if err != nil {
				hdb.staticLog.Println("Unable to mark entry as not filtered:", err)
			}
		}
		// Reset filtered fields
		hdb.filteredTree = hdb.staticHostTree
		hdb.filteredHosts = make(map[string]types.SiaPublicKey)
		hdb.blockedDomains = newBlockedDomains(nil)
		hdb.filterMode = fm
		return nil
	}

	// Check for no hosts submitted with whitelist enabled
	isWhitelist := fm == modules.HostDBActiveWhitelist
	if (len(hosts) == 0 && len(netAddresses) == 0) && isWhitelist {
		return errors.New("cannot enable whitelist without hosts")
	}

	// Create filtered HostTree
	hdb.filteredTree = hosttree.New(hdb.weightFunc, modules.ProdDependencies.Resolver())

	// Create filteredHosts map
	filteredHosts := make(map[string]types.SiaPublicKey)
	for _, h := range hosts {
		// Add host to filtered host map
		if _, ok := filteredHosts[h.String()]; ok {
			continue
		}
		filteredHosts[h.String()] = h

		// Update host in unfiltered hosttree
		err := hdb.staticHostTree.SetFiltered(h, true)
		if err != nil {
			hdb.staticLog.Println("Unable to mark entry as filtered:", err)
		}
	}
	hdb.blockedDomains.managedAddDomains(netAddresses)

	var allErrs error
	allHosts := hdb.staticHostTree.All()
	for _, host := range allHosts {
		if !hdb.blockedDomains.managedIsBlocked(host.NetAddress) {
			continue
		}
		filteredHosts[host.PublicKey.String()] = host.PublicKey

		// Update host in unfiltered hosttree
		err := hdb.staticHostTree.SetFiltered(host.PublicKey, true)
		if err != nil {
			hdb.staticLog.Println("Unable to mark entry as filtered:", err)
		}
	}
	for _, host := range allHosts {
		// Add hosts to filtered tree
		_, ok := filteredHosts[host.PublicKey.String()]
		if isWhitelist != ok {
			continue
		}
		err := hdb.filteredTree.Insert(host)
		if err != nil {
			allErrs = errors.Compose(allErrs, err)
		}
	}
	hdb.filteredHosts = filteredHosts
	hdb.filterMode = fm

	return errors.Compose(allErrs, hdb.saveSync())
}

// InitialScanComplete returns a boolean indicating if the initial scan of the
// hostdb is completed.
func (hdb *HostDB) InitialScanComplete() (complete bool, err error) {
	if err = hdb.tg.Add(); err != nil {
		return false, errors.AddContext(err, "error adding hostdb threadgroup:")
	}
	defer hdb.tg.Done()
	hdb.mu.Lock()
	defer hdb.mu.Unlock()
	complete = hdb.initialScanComplete
	return
}

// IPViolationsCheck returns a boolean indicating if the IP violation check is
// enabled or not.
func (hdb *HostDB) IPViolationsCheck() (bool, error) {
	if err := hdb.tg.Add(); err != nil {
		return false, errors.AddContext(err, "error adding hostdb threadgroup:")
	}
	defer hdb.tg.Done()
	hdb.mu.RLock()
	defer hdb.mu.RUnlock()
	return !hdb.disableIPViolationCheck, nil
}

// SetAllowance updates the allowance used by the hostdb for weighing hosts by
// updating the host weight function. It will completely rebuild the hosttree so
// it should be used with care.
func (hdb *HostDB) SetAllowance(allowance modules.Allowance) error {
	if err := hdb.tg.Add(); err != nil {
		return errors.AddContext(err, "error adding hostdb threadgroup:")
	}
	defer hdb.tg.Done()

	// If the allowance is empty, set it to the default allowance. This ensures
	// that the estimates are at least moderately grounded.
	if reflect.DeepEqual(allowance, modules.Allowance{}) {
		allowance = modules.DefaultAllowance
	}

	// Update the allowance.
	hdb.mu.Lock()
	hdb.allowance = allowance
	hdb.mu.Unlock()

	// Update the weight function.
	wf := hdb.managedCalculateHostWeightFn(allowance)
	return hdb.managedSetWeightFunction(wf)
}

// SetIPViolationCheck enables or disables the IP violation check. If disabled,
// CheckForIPViolations won't return bad hosts and RandomHosts will return the
// address blacklist.
func (hdb *HostDB) SetIPViolationCheck(enabled bool) error {
	if err := hdb.tg.Add(); err != nil {
		return errors.AddContext(err, "error adding hostdb threadgroup:")
	}
	defer hdb.tg.Done()

	hdb.mu.Lock()
	defer hdb.mu.Unlock()
	hdb.disableIPViolationCheck = !enabled
	return nil
}

// UpdateContracts rebuilds the knownContracts of the HostBD using the provided
// contracts.
func (hdb *HostDB) UpdateContracts(contracts []modules.RenterContract) error {
	if err := hdb.tg.Add(); err != nil {
		return errors.AddContext(err, "error adding hostdb threadgroup:")
	}
	defer hdb.tg.Done()
	hdb.mu.Lock()
	defer hdb.mu.Unlock()
	hdb.updateContracts(contracts)
	return nil
}
