// Package host is an implementation of the host module, and is responsible for
// participating in the storage ecosystem, turning available disk space an
// internet bandwidth into profit for the user.
package host

// TODO: what happens if the renter submits the revision early, before the
// final revision. Will the host mark the contract as complete?

// TODO: Host and renter are reporting errors where the renter is not adding
// enough fees to the file contract.

// TODO: Test the safety of the builder, it should be okay to have multiple
// builders open for up to 600 seconds, which means multiple blocks could be
// received in that time period. Should also check what happens if a parent
// gets confirmed on the blockchain before the builder is finished.

// TODO: Double check that any network connection has a finite deadline -
// handling action items properly requires that the locks held on the
// obligations eventually be released. There's also some more advanced
// implementation that needs to happen with the storage obligation locks to
// make sure that someone who wants a lock is able to get it eventually.

// TODO: Add contract compensation from form contract to the storage obligation
// financial metrics, and to the host's tracking.

// TODO: merge the network interfaces stuff, don't forget to include the
// 'announced' variable as one of the outputs.

// TODO: 'announced' doesn't tell you if the announcement made it to the
// blockchain.

// TODO: Need to make sure that the revision exchange for the renter and the
// host is being handled correctly. For the host, it's not so difficult. The
// host need only send the most recent revision every time. But, the host
// should not sign a revision unless the renter has explicitly signed such that
// the 'WholeTransaction' fields cover only the revision and that the
// signatures for the revision don't depend on anything else. The renter needs
// to verify the same when checking on a file contract revision from the host.
// If the host has submitted a file contract revision where the signatures have
// signed the whole file contract, there is an issue.

// TODO: there is a mistake in the file contract revision rpc, the host, if it
// does not have the right file contract id, should be returning an error there
// to the renter (and not just to it's calling function without informing the
// renter what's up).

// TODO: Need to make sure that the correct height is being used when adding
// sectors to the storage manager - in some places right now WindowStart is
// being used but really it's WindowEnd that should be in use.

// TODO: The host needs some way to blacklist file contracts that are being
// abusive by repeatedly getting free download batches.

// TODO: clean up all of the magic numbers in the host.

// TODO: revamp the finances for the storage obligations.

// TODO: host_test.go has commented out tests.

// TODO: network_test.go has commented out tests.

// TODO: persist_test.go has commented out tests.

// TODO: update_test.go has commented out tests.

import (
	"container/heap"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/host/contractmanager"
	"gitlab.com/NebulousLabs/Sia/modules/host/mdm"
	"gitlab.com/NebulousLabs/Sia/persist"
	siasync "gitlab.com/NebulousLabs/Sia/sync"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
	connmonitor "gitlab.com/NebulousLabs/monitor"
	"gitlab.com/NebulousLabs/siamux"
)

const (
	// Names of the various persistent files in the host.
	dbFilename   = modules.HostDir + ".db"
	logFile      = modules.HostDir + ".log"
	settingsFile = modules.HostDir + ".json"
)

var (
	// dbMetadata is a header that gets put into the database to identify a
	// version and indicate that the database holds host information.
	dbMetadata = persist.Metadata{
		Header:  "Sia Host DB",
		Version: "0.5.2",
	}

	// Nil dependency errors.
	errNilCS      = errors.New("host cannot use a nil state")
	errNilTpool   = errors.New("host cannot use a nil transaction pool")
	errNilWallet  = errors.New("host cannot use a nil wallet")
	errNilGateway = errors.New("host cannot use nil gateway")

	// rpcPriceGuaranteePeriod defines the amount of time a host will guarantee
	// its prices to the renter.
	rpcPriceGuaranteePeriod = build.Select(build.Var{
		Standard: 10 * time.Minute,
		Dev:      5 * time.Minute,
		Testing:  30 * time.Second,
	}).(time.Duration)

	// pruneExpiredRPCPriceTableFrequency is the frequency at which the host
	// checks if it can expire price tables that have an expiry in the past.
	pruneExpiredRPCPriceTableFrequency = build.Select(build.Var{
		Standard: 15 * time.Minute,
		Dev:      5 * time.Minute,
		Testing:  10 * time.Second,
	}).(time.Duration)
)

// A Host contains all the fields necessary for storing files for clients and
// performing the storage proofs on the received files.
type Host struct {
	// RPC Metrics - atomic variables need to be placed at the top to preserve
	// compatibility with 32bit systems. These values are not persistent.
	atomicDownloadCalls     uint64
	atomicErroredCalls      uint64
	atomicFormContractCalls uint64
	atomicRenewCalls        uint64
	atomicReviseCalls       uint64
	atomicSettingsCalls     uint64
	atomicUnrecognizedCalls uint64

	// Error management. There are a few different types of errors returned by
	// the host. These errors intentionally not persistent, so that the logging
	// limits of each error type will be reset each time the host is reset.
	// These values are not persistent.
	atomicCommunicationErrors uint64
	atomicConnectionErrors    uint64
	atomicConsensusErrors     uint64
	atomicInternalErrors      uint64
	atomicNormalErrors        uint64

	// Dependencies.
	cs            modules.ConsensusSet
	g             modules.Gateway
	tpool         modules.TransactionPool
	wallet        modules.Wallet
	staticAlerter *modules.GenericAlerter
	staticMux     *siamux.SiaMux
	dependencies  modules.Dependencies
	modules.StorageManager

	// Subsystems
	staticAccountManager *accountManager
	staticMDM            *mdm.MDM

	// Host ACID fields - these fields need to be updated in serial, ACID
	// transactions.
	announced    bool
	blockHeight  types.BlockHeight
	publicKey    types.SiaPublicKey
	secretKey    crypto.SecretKey
	recentChange modules.ConsensusChangeID
	unlockHash   types.UnlockHash // A wallet address that can receive coins.

	// Host transient fields - these fields are either determined at startup or
	// otherwise are not critical to always be correct.
	autoAddress          modules.NetAddress // Determined using automatic tooling in network.go
	financialMetrics     modules.HostFinancialMetrics
	settings             modules.HostInternalSettings
	revisionNumber       uint64
	workingStatus        modules.HostWorkingStatus
	connectabilityStatus modules.HostConnectabilityStatus

	// A map of storage obligations that are currently being modified. Locks on
	// storage obligations can be long-running, and each storage obligation can
	// be locked separately.
	lockedStorageObligations map[types.FileContractID]*lockedObligation

	// A collection of rpc price tables, covered by its own RW mutex. It
	// contains the host's current price table and the set of price tables the
	// host has communicated to all renters, thus guaranteeing a set of prices
	// for a fixed period of time. The host's RPC prices are dynamic, and are
	// subject to various conditions specific to the RPC in question. Examples
	// of such conditions are congestion, load, liquidity, etc.
	staticPriceTables *hostPrices

	// Misc state.
	db            *persist.BoltDatabase
	listener      net.Listener
	log           *persist.Logger
	mu            sync.RWMutex
	staticMonitor *connmonitor.Monitor
	persistDir    string
	port          string
	tg            siasync.ThreadGroup
}

// hostPrices is a helper type that wraps both the host's RPC price table and
// the set of price tables containing prices it has guaranteed to all renters,
// covered by a read write mutex to help lock contention. It contains a separate
// minheap that enables efficiently purging expired price tables from the
// 'guaranteed' map.
type hostPrices struct {
	current       modules.RPCPriceTable
	guaranteed    map[modules.UniqueID]*modules.RPCPriceTable
	staticMinHeap priceTableHeap
	mu            sync.RWMutex
}

// managedCurrent returns the host's current price table
func (hp *hostPrices) managedCurrent() modules.RPCPriceTable {
	hp.mu.RLock()
	defer hp.mu.RUnlock()
	return hp.current
}

// managedGet returns the price table with given uid
func (hp *hostPrices) managedGet(uid modules.UniqueID) (pt *modules.RPCPriceTable, found bool) {
	hp.mu.RLock()
	defer hp.mu.RUnlock()
	pt, found = hp.guaranteed[uid]
	return
}

// managedUpdate overwrites the current price table with the one that's given
func (hp *hostPrices) managedUpdate(pt modules.RPCPriceTable) {
	hp.mu.Lock()
	defer hp.mu.Unlock()
	hp.current = pt
}

// managedTrack adds the given price table to the 'guaranteed' map, that holds
// all of the price tables the host has recently guaranteed to renters. It will
// also add it to the heap which facilates efficient pruning of that map.
func (hp *hostPrices) managedTrack(pt *modules.RPCPriceTable) {
	hp.mu.Lock()
	hp.guaranteed[pt.UID] = pt
	hp.mu.Unlock()
	hp.staticMinHeap.Push(pt)
}

// managedPruneExpired removes all of the price tables that have expired from
// the 'guaranteed' map.
func (hp *hostPrices) managedPruneExpired() {
	current := hp.managedCurrent()
	expired := hp.staticMinHeap.PopExpired()
	if len(expired) == 0 {
		return
	}
	hp.mu.Lock()
	for _, uid := range expired {
		// Sanity check to never prune the host's current price table. This can
		// never occur because the host's price table UID is not added to the
		// minheap.
		if uid == current.UID {
			build.Critical("The host's current price table should not be pruned")
			continue
		}
		delete(hp.guaranteed, uid)
	}
	hp.mu.Unlock()
}

// lockedObligation is a helper type that locks a TryMutex and a counter to
// indicate how many times the locked obligation has been fetched from the
// lockedStorageObligations map already.
type lockedObligation struct {
	mu siasync.TryMutex
	n  uint
}

// priceTableHeap is a helper type that contains a min heap of rpc price tables,
// sorted on their expiry. The heap is guarded by its own mutex and allows for
// peeking at the min expiry.
type priceTableHeap struct {
	heap rpcPriceTableHeap
	mu   sync.Mutex
}

// PopExpired returns the UIDs for all rpc price tables that have expired
func (pth *priceTableHeap) PopExpired() (expired []modules.UniqueID) {
	pth.mu.Lock()
	defer pth.mu.Unlock()

	now := time.Now().Unix()
	for {
		if pth.heap.Len() == 0 {
			return
		}

		pt := heap.Pop(&pth.heap)
		if now < pt.(*modules.RPCPriceTable).Expiry {
			heap.Push(&pth.heap, pt)
			break
		}
		expired = append(expired, pt.(*modules.RPCPriceTable).UID)
	}
	return
}

// Push will add a price table to the heap.
func (pth *priceTableHeap) Push(pt *modules.RPCPriceTable) {
	pth.mu.Lock()
	defer pth.mu.Unlock()
	heap.Push(&pth.heap, pt)
}

// rpcPriceTableHeap is a min heap of rpc price tables
type rpcPriceTableHeap []*modules.RPCPriceTable

// Implementation of heap.Interface for rpcPriceTableHeap.
func (pth rpcPriceTableHeap) Len() int           { return len(pth) }
func (pth rpcPriceTableHeap) Less(i, j int) bool { return pth[i].Expiry < pth[j].Expiry }
func (pth rpcPriceTableHeap) Swap(i, j int)      { pth[i], pth[j] = pth[j], pth[i] }
func (pth *rpcPriceTableHeap) Push(x interface{}) {
	pt := x.(*modules.RPCPriceTable)
	*pth = append(*pth, pt)
}
func (pth *rpcPriceTableHeap) Pop() interface{} {
	old := *pth
	n := len(old)
	pt := old[n-1]
	*pth = old[0 : n-1]
	return pt
}

// checkUnlockHash will check that the host has an unlock hash. If the host
// does not have an unlock hash, an attempt will be made to get an unlock hash
// from the wallet. That may fail due to the wallet being locked, in which case
// an error is returned.
func (h *Host) checkUnlockHash() error {
	addrs, err := h.wallet.AllAddresses()
	if err != nil {
		return err
	}
	hasAddr := false
	for _, addr := range addrs {
		if h.unlockHash == addr {
			hasAddr = true
			break
		}
	}
	if !hasAddr || h.unlockHash == (types.UnlockHash{}) {
		uc, err := h.wallet.NextAddress()
		if err != nil {
			return err
		}

		// Set the unlock hash and save the host. Saving is important, because
		// the host will be using this unlock hash to establish identity, and
		// losing it will mean silently losing part of the host identity.
		h.unlockHash = uc.UnlockHash()
		err = h.saveSync()
		if err != nil {
			return err
		}
	}
	return nil
}

// managedInternalSettings returns the settings of a host.
func (h *Host) managedInternalSettings() modules.HostInternalSettings {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.settings
}

// managedUpdatePriceTable will recalculate the RPC costs and update the host's
// price table accordingly.
func (h *Host) managedUpdatePriceTable() {
	// create a new RPC price table and set the expiry
	es := h.managedExternalSettings()
	priceTable := modules.RPCPriceTable{
		Expiry: time.Now().Add(rpcPriceGuaranteePeriod).Unix(),

		// TODO: hardcoded cost should be updated to use a better value.
		FundAccountCost:      types.NewCurrency64(1),
		UpdatePriceTableCost: types.NewCurrency64(1),

		// TODO: hardcoded MDM costs should be updated to use better values.
		HasSectorBaseCost: types.NewCurrency64(1),
		InitBaseCost:      types.NewCurrency64(1),
		MemoryTimeCost:    types.NewCurrency64(1),
		ReadBaseCost:      types.NewCurrency64(1),
		ReadLengthCost:    types.NewCurrency64(1),

		// Bandwidth related fields.
		DownloadBandwidthCost: es.DownloadBandwidthPrice,
		UploadBandwidthCost:   es.UploadBandwidthPrice,
	}
	fastrand.Read(priceTable.UID[:])

	// update the pricetable
	h.staticPriceTables.managedUpdate(priceTable)
}

// threadedPruneExpiredPriceTables will expire price tables which have an expiry
// in the past.
//
// Note: threadgroup counter must be inside for loop. If not, calling 'Flush'
// on the threadgroup would deadlock.
func (h *Host) threadedPruneExpiredPriceTables() {
	for {
		func() {
			if err := h.tg.Add(); err != nil {
				return
			}
			defer h.tg.Done()
			h.staticPriceTables.managedPruneExpired()
		}()

		// Block until next cycle.
		select {
		case <-h.tg.StopChan():
			return
		case <-time.After(pruneExpiredRPCPriceTableFrequency):
			continue
		}
	}
}

// newHost returns an initialized Host, taking a set of dependencies as input.
// By making the dependencies an argument of the 'new' call, the host can be
// mocked such that the dependencies can return unexpected errors or unique
// behaviors during testing, enabling easier testing of the failure modes of
// the Host.
func newHost(dependencies modules.Dependencies, smDeps modules.Dependencies, cs modules.ConsensusSet, g modules.Gateway, tpool modules.TransactionPool, wallet modules.Wallet, mux *siamux.SiaMux, listenerAddress string, persistDir string) (*Host, error) {
	// Check that all the dependencies were provided.
	if cs == nil {
		return nil, errNilCS
	}
	if g == nil {
		return nil, errNilGateway
	}
	if tpool == nil {
		return nil, errNilTpool
	}
	if wallet == nil {
		return nil, errNilWallet
	}

	// Create the host object.
	h := &Host{
		cs:                       cs,
		g:                        g,
		tpool:                    tpool,
		wallet:                   wallet,
		staticAlerter:            modules.NewAlerter("host"),
		staticMux:                mux,
		dependencies:             dependencies,
		lockedStorageObligations: make(map[types.FileContractID]*lockedObligation),
		staticPriceTables: &hostPrices{
			guaranteed: make(map[modules.UniqueID]*modules.RPCPriceTable),
			staticMinHeap: priceTableHeap{
				heap: make([]*modules.RPCPriceTable, 0),
			},
		},
		persistDir: persistDir,
	}

	// Create MDM.
	h.staticMDM = mdm.New(h)

	// Call stop in the event of a partial startup.
	var err error
	defer func() {
		if err != nil {
			err = composeErrors(h.tg.Stop(), err)
		}
	}()

	// Create the perist directory if it does not yet exist.
	err = dependencies.MkdirAll(h.persistDir, 0700)
	if err != nil {
		return nil, err
	}

	// Initialize the logger, and set up the stop call that will close the
	// logger.
	h.log, err = dependencies.NewLogger(filepath.Join(h.persistDir, logFile))
	if err != nil {
		return nil, err
	}

	h.tg.AfterStop(func() {
		err = h.log.Close()
		if err != nil {
			// State of the logger is uncertain, a Println will have to
			// suffice.
			fmt.Println("Error when closing the logger:", err)
		}
	})

	// Add the storage manager to the host, and set up the stop call that will
	// close the storage manager.
	h.StorageManager, err = contractmanager.NewCustomContractManager(smDeps, filepath.Join(persistDir, "contractmanager"))
	if err != nil {
		h.log.Println("Could not open the storage manager:", err)
		return nil, err
	}
	h.tg.AfterStop(func() {
		err = h.StorageManager.Close()
		if err != nil {
			h.log.Println("Could not close storage manager:", err)
		}
	})

	// Load the prior persistence structures, and configure the host to save
	// before shutting down.
	err = h.load()
	if err != nil {
		return nil, err
	}
	h.tg.AfterStop(func() {
		err = h.saveSync()
		if err != nil {
			h.log.Println("Could not save host upon shutdown:", err)
		}
	})

	// Add the account manager subsystem
	h.staticAccountManager, err = h.newAccountManager()
	if err != nil {
		return nil, err
	}

	// Subscribe to the consensus set.
	err = h.initConsensusSubscription()
	if err != nil {
		return nil, err
	}

	// Ensure the host is consistent by pruning any stale storage obligations.
	if err := h.PruneStaleStorageObligations(); err != nil {
		h.log.Println("Could not prune stale storage obligations:", err)
		return nil, err
	}

	// Create bandwidth monitor
	h.staticMonitor = connmonitor.NewMonitor()

	// Initialize the networking. We need to hold the lock while doing so since
	// the previous load subscribed the host to the consensus set.
	h.mu.Lock()
	err = h.initNetworking(listenerAddress)
	h.mu.Unlock()
	if err != nil {
		h.log.Println("Could not initialize host networking:", err)
		return nil, err
	}

	// Initialize the RPC price table
	h.managedUpdatePriceTable()

	// Ensure the expired RPC tables get pruned as to not leak memory
	go h.threadedPruneExpiredPriceTables()

	return h, nil
}

// New returns an initialized Host.
func New(cs modules.ConsensusSet, g modules.Gateway, tpool modules.TransactionPool, wallet modules.Wallet, mux *siamux.SiaMux, address string, persistDir string) (*Host, error) {
	return newHost(modules.ProdDependencies, new(modules.ProductionDependencies), cs, g, tpool, wallet, mux, address, persistDir)
}

// NewCustomHost returns an initialized Host using the provided dependencies.
func NewCustomHost(deps modules.Dependencies, cs modules.ConsensusSet, g modules.Gateway, tpool modules.TransactionPool, wallet modules.Wallet, mux *siamux.SiaMux, address string, persistDir string) (*Host, error) {
	return newHost(deps, new(modules.ProductionDependencies), cs, g, tpool, wallet, mux, address, persistDir)
}

// NewCustomTestHost allows passing in both host dependencies and storage
// manager dependencies. Used solely for testing purposes, to allow dependency
// injection into the host's submodules.
func NewCustomTestHost(deps modules.Dependencies, smDeps modules.Dependencies, cs modules.ConsensusSet, g modules.Gateway, tpool modules.TransactionPool, wallet modules.Wallet, mux *siamux.SiaMux, address string, persistDir string) (*Host, error) {
	return newHost(deps, smDeps, cs, g, tpool, wallet, mux, address, persistDir)
}

// Close shuts down the host.
func (h *Host) Close() error {
	return h.tg.Stop()
}

// ExternalSettings returns the hosts external settings. These values cannot be
// set by the user (host is configured through InternalSettings), and are the
// values that get displayed to other hosts on the network.
func (h *Host) ExternalSettings() modules.HostExternalSettings {
	err := h.tg.Add()
	if err != nil {
		build.Critical("Call to ExternalSettings after close")
	}
	defer h.tg.Done()
	return h.managedExternalSettings()
}

// BandwidthCounters returns the Hosts's upload and download bandwidth
func (h *Host) BandwidthCounters() (uint64, uint64, time.Time, error) {
	if err := h.tg.Add(); err != nil {
		return 0, 0, time.Time{}, err
	}
	defer h.tg.Done()
	readBytes, writeBytes := h.staticMonitor.Counts()
	startTime := h.staticMonitor.StartTime()
	return writeBytes, readBytes, startTime, nil
}

// WorkingStatus returns the working state of the host, where working is
// defined as having received more than workingStatusThreshold settings calls
// over the period of workingStatusFrequency.
func (h *Host) WorkingStatus() modules.HostWorkingStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.workingStatus
}

// ConnectabilityStatus returns the connectability state of the host, whether
// the host can connect to itself on its configured netaddress.
func (h *Host) ConnectabilityStatus() modules.HostConnectabilityStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.connectabilityStatus
}

// FinancialMetrics returns information about the financial commitments,
// rewards, and activities of the host.
func (h *Host) FinancialMetrics() modules.HostFinancialMetrics {
	err := h.tg.Add()
	if err != nil {
		build.Critical("Call to FinancialMetrics after close")
	}
	defer h.tg.Done()
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.financialMetrics
}

// PublicKey returns the public key of the host that is used to facilitate
// relationships between the host and renter.
func (h *Host) PublicKey() types.SiaPublicKey {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.publicKey
}

// SetInternalSettings updates the host's internal HostInternalSettings object.
func (h *Host) SetInternalSettings(settings modules.HostInternalSettings) error {
	err := h.tg.Add()
	if err != nil {
		return err
	}
	defer h.tg.Done()
	h.mu.Lock()
	defer h.mu.Unlock()

	// The host should not be accepting file contracts if it does not have an
	// unlock hash.
	if settings.AcceptingContracts {
		err := h.checkUnlockHash()
		if err != nil {
			return errors.New("internal settings not updated, no unlock hash: " + err.Error())
		}
	}

	if settings.NetAddress != "" {
		err := settings.NetAddress.IsValid()
		if err != nil {
			return errors.New("internal settings not updated, invalid NetAddress: " + err.Error())
		}
	}

	// Check if the net address for the host has changed. If it has, and it's
	// not equal to the auto address, then the host is going to need to make
	// another blockchain announcement.
	if h.settings.NetAddress != settings.NetAddress && settings.NetAddress != h.autoAddress {
		h.announced = false
	}

	h.settings = settings
	h.revisionNumber++

	// The locked storage collateral was altered, we potentially want to
	// unregister the insufficient collateral budget alert
	h.TryUnregisterInsufficientCollateralBudgetAlert()

	err = h.saveSync()
	if err != nil {
		return errors.New("internal settings updated, but failed saving to disk: " + err.Error())
	}
	return nil
}

// InternalSettings returns the settings of a host.
func (h *Host) InternalSettings() modules.HostInternalSettings {
	err := h.tg.Add()
	if err != nil {
		return modules.HostInternalSettings{}
	}
	defer h.tg.Done()
	return h.managedInternalSettings()
}

// BlockHeight returns the host's current blockheight.
func (h *Host) BlockHeight() types.BlockHeight {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.blockHeight
}

// managedExternalSettings returns the host's external settings. These values
// cannot be set by the user (host is configured through InternalSettings), and
// are the values that get displayed to other hosts on the network.
func (h *Host) managedExternalSettings() modules.HostExternalSettings {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.externalSettings()
}
