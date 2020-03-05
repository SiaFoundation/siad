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
	"gitlab.com/NebulousLabs/Sia/persist"
	siasync "gitlab.com/NebulousLabs/Sia/sync"
	"gitlab.com/NebulousLabs/Sia/types"
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
		Dev:      1 * time.Minute,
		Testing:  5 * time.Second,
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
	lockedStorageObligations map[types.FileContractID]*siasync.TryMutex

	// The price table contains a set of RPC costs, along with an expiry that
	// dictates up until what time the host guarantees the prices that are
	// listed. These host's RPC prices are dynamic, and are subject to various
	// conditions specific to the RPC in question. Examples of such conditions
	// are congestion, load, liquidity, etc.
	priceTable       modules.RPCPriceTable
	uuidToPriceTable map[types.Specifier]*modules.RPCPriceTable

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
	his := h.managedInternalSettings()
	priceTable := modules.RPCPriceTable{
		Expiry:               time.Now().Add(rpcPriceGuaranteePeriod).Unix(),
		UpdatePriceTableCost: h.managedCalculateUpdatePriceTableRPCPrice(),

		// TODO: hardcoded MDM costs should be updated to use better values.
		InitBaseCost:   his.MinBaseRPCPrice,
		MemoryTimeCost: his.MinBaseRPCPrice,
		ReadBaseCost:   his.MinBaseRPCPrice,
		ReadLengthCost: his.MinBaseRPCPrice,
	}

	// update the pricetable
	h.mu.Lock()
	h.priceTable = priceTable
	h.mu.Unlock()
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
		lockedStorageObligations: make(map[types.FileContractID]*siasync.TryMutex),

		persistDir: persistDir,
	}

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

	// Initialize the RPC price table.
	h.managedUpdatePriceTable()

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
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.externalSettings()
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
