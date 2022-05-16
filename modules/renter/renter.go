// Package renter is responsible for uploading and downloading files on the sia
// network.
package renter

// TODO: Allow the 'baseMemory' to be set by the user.
//
// TODO: The repair loop currently receives new upload jobs through a channel.
// The download loop has a better model, a heap that can be pushed to and popped
// from concurrently without needing complex channel communication. Migrating
// the renter to this model should clean up some of the places where uploading
// bottlenecks, and reduce the amount of channel-ninjitsu required to make the
// uploading function.
//
// TODO: Allow user to configure the packet size when ratelimiting the renter.
// Currently the default is set to 16kb. That's going to require updating the
// API and extending the settings object, and then tweaking the
// setBandwidthLimits function.
//
// TODO: Currently 'callUpdate()' is used after setting the allowance, though
// this doesn't guarantee that anything interesting will happen because the
// contractor's 'threadedContractMaintenance' will run in the background and
// choose to update the hosts and contracts. Really, we should have the
// contractor notify the renter whenever there has been a change in the contract
// set so that 'callUpdate()' can be used. Implementation in renter.SetSettings.

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/ratelimit"
	"gitlab.com/NebulousLabs/siamux"
	"gitlab.com/NebulousLabs/threadgroup"
	"gitlab.com/NebulousLabs/writeaheadlog"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter/contractor"
	"go.sia.tech/siad/modules/renter/filesystem"
	"go.sia.tech/siad/modules/renter/hostdb"
	"go.sia.tech/siad/persist"
	siasync "go.sia.tech/siad/sync"
	"go.sia.tech/siad/types"
)

var (
	errNilContractor = errors.New("cannot create renter with nil contractor")
	errNilCS         = errors.New("cannot create renter with nil consensus set")
	errNilGateway    = errors.New("cannot create hostdb with nil gateway")
	errNilHdb        = errors.New("cannot create renter with nil hostdb")
	errNilTpool      = errors.New("cannot create renter with nil transaction pool")
	errNilWallet     = errors.New("cannot create renter with nil wallet")
)

// A hostContractor negotiates, revises, renews, and provides access to file
// contracts.
type hostContractor interface {
	modules.Alerter

	// SetAllowance sets the amount of money the contractor is allowed to
	// spend on contracts over a given time period, divided among the number
	// of hosts specified. Note that contractor can start forming contracts as
	// soon as SetAllowance is called; that is, it may block.
	SetAllowance(modules.Allowance) error

	// Allowance returns the current allowance
	Allowance() modules.Allowance

	// Close closes the hostContractor.
	Close() error

	// CancelContract cancels the Renter's contract
	CancelContract(id types.FileContractID) error

	// Contracts returns the staticContracts of the renter's hostContractor.
	Contracts() []modules.RenterContract

	// ContractByPublicKey returns the contract associated with the host key.
	ContractByPublicKey(types.SiaPublicKey) (modules.RenterContract, bool)

	// ContractPublicKey returns the public key capable of verifying the renter's
	// signature on a contract.
	ContractPublicKey(pk types.SiaPublicKey) (crypto.PublicKey, bool)

	// ChurnStatus returns contract churn stats for the current period.
	ChurnStatus() modules.ContractorChurnStatus

	// ContractUtility returns the utility field for a given contract, along
	// with a bool indicating if it exists.
	ContractUtility(types.SiaPublicKey) (modules.ContractUtility, bool)

	// ContractStatus returns the status of the given contract within the
	// watchdog.
	ContractStatus(fcID types.FileContractID) (modules.ContractWatchStatus, bool)

	// CurrentPeriod returns the height at which the current allowance period
	// began.
	CurrentPeriod() types.BlockHeight

	// InitRecoveryScan starts scanning the whole blockchain for recoverable
	// contracts within a separate thread.
	InitRecoveryScan() error

	// PeriodSpending returns the amount spent on contracts during the current
	// billing period.
	PeriodSpending() (modules.ContractorSpending, error)

	// ProvidePayment takes a stream and a set of payment details and handles
	// the payment for an RPC by sending and processing payment request and
	// response objects to the host. It returns an error in case of failure.
	ProvidePayment(stream io.ReadWriter, pt *modules.RPCPriceTable, details contractor.PaymentDetails) error

	// OldContracts returns the oldContracts of the renter's hostContractor.
	OldContracts() []modules.RenterContract

	// Editor creates an Editor from the specified contract ID, allowing the
	// insertion, deletion, and modification of sectors.
	Editor(types.SiaPublicKey, <-chan struct{}) (contractor.Editor, error)

	// IsOffline reports whether the specified host is considered offline.
	IsOffline(types.SiaPublicKey) bool

	// Downloader creates a Downloader from the specified contract ID,
	// allowing the retrieval of sectors.
	Downloader(types.SiaPublicKey, <-chan struct{}) (contractor.Downloader, error)

	// Session creates a Session from the specified contract ID.
	Session(types.SiaPublicKey, <-chan struct{}) (contractor.Session, error)

	// RecoverableContracts returns the contracts that the contractor deems
	// recoverable. That means they are not expired yet and also not part of the
	// active contracts. Usually this should return an empty slice unless the host
	// isn't available for recovery or something went wrong.
	RecoverableContracts() []modules.RecoverableContract

	// RecoveryScanStatus returns a bool indicating if a scan for recoverable
	// contracts is in progress and if it is, the current progress of the scan.
	RecoveryScanStatus() (bool, types.BlockHeight)

	// RefreshedContract checks if the contract was previously refreshed
	RefreshedContract(fcid types.FileContractID) bool

	// RenewContract takes an established connection to a host and renews the
	// given contract with that host.
	RenewContract(conn net.Conn, fcid types.FileContractID, params modules.ContractParams, txnBuilder modules.TransactionBuilder, tpool modules.TransactionPool, hdb modules.HostDB, pt *modules.RPCPriceTable) (modules.RenterContract, []types.Transaction, error)

	// Synced returns a channel that is closed when the contractor is fully
	// synced with the peer-to-peer network.
	Synced() <-chan struct{}

	// UpdateWorkerPool updates the workerpool currently in use by the contractor.
	UpdateWorkerPool(modules.WorkerPool)
}

type renterFuseManager interface {
	// Mount mounts the files under the specified siapath under the 'mountPoint' folder on
	// the local filesystem.
	Mount(mountPoint string, sp modules.SiaPath, opts modules.MountOptions) (err error)

	// MountInfo returns the list of currently mounted fuse filesystems.
	MountInfo() []modules.MountInfo

	// Unmount unmounts the fuse filesystem currently mounted at mountPoint.
	Unmount(mountPoint string) error
}

// cachedUtilities contains the cached utilities used when bubbling file and
// folder metadata.
type cachedUtilities struct {
	offline      map[string]bool
	goodForRenew map[string]bool
	contracts    map[string]modules.RenterContract
	used         []types.SiaPublicKey
}

// A Renter is responsible for tracking all of the files that a user has
// uploaded to Sia, as well as the locations and health of these files.
type Renter struct {
	// Download management. The heap has a separate mutex because it is always
	// accessed in isolation.
	downloadHeapMu sync.Mutex         // Used to protect the downloadHeap.
	downloadHeap   *downloadChunkHeap // A heap of priority-sorted chunks to download.
	newDownloads   chan struct{}      // Used to notify download loop that new downloads are available.

	// Download history. The history list has its own mutex because it is always
	// accessed in isolation.
	//
	// TODO: Currently the download history doesn't include repair-initiated
	// downloads, and instead only contains user-initiated downloads.
	downloadHistory   map[modules.DownloadID]*download
	downloadHistoryMu sync.Mutex

	// Upload management.
	uploadHeap    uploadHeap
	directoryHeap directoryHeap
	stuckStack    stuckStack

	// Cache the hosts from the last price estimation result.
	lastEstimationHosts []modules.HostDBEntry

	// staticBubbleScheduler manages the bubble requests for the renter
	staticBubbleScheduler *bubbleScheduler

	// cachedUtilities contain contract information used when calculating metadata
	// information about the filesystem, such as health. This information is used
	// in various functions such as listing filesystem information and bubble.
	// These values are cached to prevent recomputing them too often.
	cachedUtilities cachedUtilities

	// The renter's bandwidth ratelimit.
	rl *ratelimit.RateLimit

	// stats cache related fields.
	statsChan chan struct{}
	statsMu   sync.Mutex

	// read registry stats
	staticRRS *readRegistryStats

	// Memory management
	//
	// registryMemoryManager is used for updating registry entries and reading
	// them.
	//
	// userUploadManager is used for user-initiated uploads
	//
	// userDownloadMemoryManager is used for user-initiated downloads
	//
	// repairMemoryManager is used for repair work scheduled by siad
	//
	registryMemoryManager     *memoryManager
	userUploadMemoryManager   *memoryManager
	userDownloadMemoryManager *memoryManager
	repairMemoryManager       *memoryManager

	// Utilities.
	cs                                 modules.ConsensusSet
	deps                               modules.Dependencies
	g                                  modules.Gateway
	w                                  modules.Wallet
	hostContractor                     hostContractor
	hostDB                             modules.HostDB
	log                                *persist.Logger
	persist                            persistence
	persistDir                         string
	mu                                 *siasync.RWMutex
	repairLog                          *persist.Logger
	staticAccountManager               *accountManager
	staticAlerter                      *modules.GenericAlerter
	staticFileSystem                   *filesystem.FileSystem
	staticFuseManager                  renterFuseManager
	staticStreamBufferSet              *streamBufferSet
	tg                                 threadgroup.ThreadGroup
	tpool                              modules.TransactionPool
	wal                                *writeaheadlog.WAL
	staticWorkerPool                   *workerPool
	staticMux                          *siamux.SiaMux
	memoryManager                      *memoryManager
	staticUploadChunkDistributionQueue *uploadChunkDistributionQueue
}

// Close closes the Renter and its dependencies
func (r *Renter) Close() error {
	// TODO: Is this check needed?
	if r == nil {
		return nil
	}

	return errors.Compose(r.tg.Stop(), r.hostDB.Close(), r.hostContractor.Close())
}

// MemoryStatus returns the current status of the memory manager
func (r *Renter) MemoryStatus() (modules.MemoryStatus, error) {
	if err := r.tg.Add(); err != nil {
		return modules.MemoryStatus{}, err
	}
	defer r.tg.Done()

	repairStatus := r.repairMemoryManager.callStatus()
	userDownloadStatus := r.userDownloadMemoryManager.callStatus()
	userUploadStatus := r.userUploadMemoryManager.callStatus()
	registryStatus := r.registryMemoryManager.callStatus()
	total := repairStatus.Add(userDownloadStatus).Add(userUploadStatus).Add(registryStatus)
	return modules.MemoryStatus{
		MemoryManagerStatus: total,

		Registry:     registryStatus,
		System:       repairStatus,
		UserDownload: userDownloadStatus,
		UserUpload:   userUploadStatus,
	}, nil
}

// PriceEstimation estimates the cost in siacoins of performing various storage
// and data operations.  The estimation will be done using the provided
// allowance, if an empty allowance is provided then the renter's current
// allowance will be used if one is set.  The final allowance used will be
// returned.
func (r *Renter) PriceEstimation(allowance modules.Allowance) (modules.RenterPriceEstimation, modules.Allowance, error) {
	if err := r.tg.Add(); err != nil {
		return modules.RenterPriceEstimation{}, modules.Allowance{}, err
	}
	defer r.tg.Done()
	// Use provide allowance. If no allowance provided use the existing
	// allowance. If no allowance exists, use a sane default allowance.
	if reflect.DeepEqual(allowance, modules.Allowance{}) {
		rs, err := r.Settings()
		if err != nil {
			return modules.RenterPriceEstimation{}, modules.Allowance{}, errors.AddContext(err, "error getting renter settings:")
		}
		allowance = rs.Allowance
		if reflect.DeepEqual(allowance, modules.Allowance{}) {
			allowance = modules.DefaultAllowance
		}
	}

	// Get hosts for estimate
	var hosts []modules.HostDBEntry
	hostmap := make(map[string]struct{})

	// Start by grabbing hosts from contracts
	// Get host pubkeys from contracts
	contracts := r.Contracts()
	var pks []types.SiaPublicKey
	for _, c := range contracts {
		u, ok := r.ContractUtility(c.HostPublicKey)
		if !ok {
			continue
		}
		// Check for active contracts only
		if !u.GoodForRenew {
			continue
		}
		pks = append(pks, c.HostPublicKey)
	}
	// Get hosts from pubkeys
	for _, pk := range pks {
		host, ok, err := r.hostDB.Host(pk)
		if !ok || host.Filtered || err != nil {
			continue
		}
		// confirm host wasn't already added
		if _, ok := hostmap[host.PublicKey.String()]; ok {
			continue
		}
		hosts = append(hosts, host)
		hostmap[host.PublicKey.String()] = struct{}{}
	}
	// Add hosts from previous estimate cache if needed
	if len(hosts) < int(allowance.Hosts) {
		id := r.mu.Lock()
		cachedHosts := r.lastEstimationHosts
		r.mu.Unlock(id)
		for _, host := range cachedHosts {
			// confirm host wasn't already added
			if _, ok := hostmap[host.PublicKey.String()]; ok {
				continue
			}
			hosts = append(hosts, host)
			hostmap[host.PublicKey.String()] = struct{}{}
		}
	}
	// Add random hosts if needed
	if len(hosts) < int(allowance.Hosts) {
		// Re-initialize the list with SiaPublicKeys to hold the public keys from the current
		// set of hosts. This list will be used as address filter when requesting random hosts.
		var pks []types.SiaPublicKey
		for _, host := range hosts {
			pks = append(pks, host.PublicKey)
		}
		// Grab hosts to perform the estimation.
		var err error
		randHosts, err := r.hostDB.RandomHostsWithAllowance(int(allowance.Hosts)-len(hosts), pks, pks, allowance)
		if err != nil {
			return modules.RenterPriceEstimation{}, allowance, errors.AddContext(err, "could not generate estimate, could not get random hosts")
		}
		// As the returned random hosts are checked for IP violations and double entries against the current
		// slice of hosts, the returned hosts can be safely added to the current slice.
		hosts = append(hosts, randHosts...)
	}
	// Check if there are zero hosts, which means no estimation can be made.
	if len(hosts) == 0 {
		return modules.RenterPriceEstimation{}, allowance, errors.New("estimate cannot be made, there are no hosts")
	}

	// Add up the costs for each host.
	var totalContractCost types.Currency
	var totalDownloadCost types.Currency
	var totalStorageCost types.Currency
	var totalUploadCost types.Currency
	for _, host := range hosts {
		totalContractCost = totalContractCost.Add(host.ContractPrice)
		totalDownloadCost = totalDownloadCost.Add(host.DownloadBandwidthPrice)
		totalStorageCost = totalStorageCost.Add(host.StoragePrice)
		totalUploadCost = totalUploadCost.Add(host.UploadBandwidthPrice)
	}

	// Convert values to being human-scale.
	totalDownloadCost = totalDownloadCost.Mul(modules.BytesPerTerabyte)
	totalStorageCost = totalStorageCost.Mul(modules.BlockBytesPerMonthTerabyte)
	totalUploadCost = totalUploadCost.Mul(modules.BytesPerTerabyte)

	// Factor in redundancy.
	totalStorageCost = totalStorageCost.Mul64(3) // TODO: follow file settings?
	totalUploadCost = totalUploadCost.Mul64(3)   // TODO: follow file settings?

	// Perform averages.
	totalContractCost = totalContractCost.Div64(uint64(len(hosts)))
	totalDownloadCost = totalDownloadCost.Div64(uint64(len(hosts)))
	totalStorageCost = totalStorageCost.Div64(uint64(len(hosts)))
	totalUploadCost = totalUploadCost.Div64(uint64(len(hosts)))

	// Take the average of the host set to estimate the overall cost of the
	// contract forming. This is to protect against the case where less hosts
	// were gathered for the estimate that the allowance requires
	totalContractCost = totalContractCost.Mul64(allowance.Hosts)

	// Add the cost of paying the transaction fees and then double the contract
	// costs to account for renewing a full set of contracts.
	_, feePerByte := r.tpool.FeeEstimation()
	txnsFees := feePerByte.Mul64(modules.EstimatedFileContractTransactionSetSize).Mul64(uint64(allowance.Hosts))
	totalContractCost = totalContractCost.Add(txnsFees)
	totalContractCost = totalContractCost.Mul64(2)

	// Determine host collateral to be added to siafund fee
	var hostCollateral types.Currency
	contractCostPerHost := totalContractCost.Div64(allowance.Hosts)
	fundingPerHost := allowance.Funds.Div64(allowance.Hosts)
	numHosts := uint64(0)
	for _, host := range hosts {
		// Assume that the ContractPrice equals contractCostPerHost and that
		// the txnFee was zero. It doesn't matter since RenterPayoutsPreTax
		// simply subtracts both values from the funding.
		host.ContractPrice = contractCostPerHost
		expectedStorage := allowance.ExpectedStorage / uint64(len(hosts))
		_, _, collateral, err := modules.RenterPayoutsPreTax(host, fundingPerHost, types.ZeroCurrency, types.ZeroCurrency, types.ZeroCurrency, allowance.Period, expectedStorage)
		if err != nil {
			continue
		}
		hostCollateral = hostCollateral.Add(collateral)
		numHosts++
	}

	// Divide by zero check. The only way to get 0 numHosts is if
	// RenterPayoutsPreTax errors for every host. This would happen if the
	// funding of the allowance is not enough as that would cause the
	// fundingPerHost to be less than the contract price
	if numHosts == 0 {
		return modules.RenterPriceEstimation{}, allowance, errors.New("funding insufficient for number of hosts")
	}
	// Calculate average collateral and determine collateral for allowance
	hostCollateral = hostCollateral.Div64(numHosts)
	hostCollateral = hostCollateral.Mul64(allowance.Hosts)

	// Add in siafund fee. which should be around 10%. The 10% siafund fee
	// accounts for paying 3.9% siafund on transactions and host collateral. We
	// estimate the renter to spend all of it's allowance so the siafund fee
	// will be calculated on the sum of the allowance and the hosts collateral
	totalPayout := allowance.Funds.Add(hostCollateral)
	siafundFee := types.Tax(r.cs.Height(), totalPayout)
	totalContractCost = totalContractCost.Add(siafundFee)

	// Increase estimates by a factor of safety to account for host churn and
	// any potential missed additions
	totalContractCost = totalContractCost.MulFloat(PriceEstimationSafetyFactor)
	totalDownloadCost = totalDownloadCost.MulFloat(PriceEstimationSafetyFactor)
	totalStorageCost = totalStorageCost.MulFloat(PriceEstimationSafetyFactor)
	totalUploadCost = totalUploadCost.MulFloat(PriceEstimationSafetyFactor)

	est := modules.RenterPriceEstimation{
		FormContracts:        totalContractCost,
		DownloadTerabyte:     totalDownloadCost,
		StorageTerabyteMonth: totalStorageCost,
		UploadTerabyte:       totalUploadCost,
	}

	id := r.mu.Lock()
	r.lastEstimationHosts = hosts
	r.mu.Unlock(id)

	return est, allowance, nil
}

// managedContractUtilityMaps returns a set of maps that contain contract
// information. Information about which contracts are offline, goodForRenew are
// available, as well as a full list of contracts keyed by their public key.
func (r *Renter) managedContractUtilityMaps() (offline map[string]bool, goodForRenew map[string]bool, contracts map[string]modules.RenterContract) {
	// Save host keys in map.
	contracts = make(map[string]modules.RenterContract)
	goodForRenew = make(map[string]bool)
	offline = make(map[string]bool)

	// Get the list of public keys from the contractor and use it to fill out
	// the contracts map.
	cs := r.hostContractor.Contracts()
	for i := 0; i < len(cs); i++ {
		contracts[cs[i].HostPublicKey.String()] = cs[i]
	}

	// Fill out the goodForRenew and offline maps based on the utility values of
	// the contractor.
	for pkString, contract := range contracts {
		cu, ok := r.ContractUtility(contract.HostPublicKey)
		if !ok {
			continue
		}
		goodForRenew[pkString] = cu.GoodForRenew
		offline[pkString] = r.hostContractor.IsOffline(contract.HostPublicKey)
	}
	return offline, goodForRenew, contracts
}

// callRenterContractsAndUtilities returns the cached contracts and utilities
// from the renter. They can be updated by calling
// managedUpdateRenterContractsAndUtilities.
func (r *Renter) callRenterContractsAndUtilities() (offline map[string]bool, goodForRenew map[string]bool, contracts map[string]modules.RenterContract, used []types.SiaPublicKey) {
	id := r.mu.Lock()
	defer r.mu.Unlock(id)
	cu := r.cachedUtilities
	return cu.offline, cu.goodForRenew, cu.contracts, cu.used
}

// managedUpdateRenterContractsAndUtilities grabs the pubkeys of the hosts that
// the file(s) have been uploaded to and then generates maps of the contract's
// utilities showing which hosts are GoodForRenew and which hosts are Offline.
// Additionally a map of host pubkeys to renter contract is created. The offline
// and goodforrenew maps are needed for calculating redundancy and other file
// metrics. All of that information is cached within the renter.
func (r *Renter) managedUpdateRenterContractsAndUtilities() {
	var used []types.SiaPublicKey
	goodForRenew := make(map[string]bool)
	offline := make(map[string]bool)
	allContracts := r.hostContractor.Contracts()
	contracts := make(map[string]modules.RenterContract)
	for _, contract := range allContracts {
		pk := contract.HostPublicKey
		cu := contract.Utility
		goodForRenew[pk.String()] = cu.GoodForRenew
		offline[pk.String()] = r.hostContractor.IsOffline(pk)
		contracts[pk.String()] = contract
		if cu.GoodForRenew {
			used = append(used, pk)
		}
	}
	// Update the used hosts of the Siafile. Only consider the ones that
	// are goodForRenew.
	for _, contract := range allContracts {
		pk := contract.HostPublicKey
		if _, gfr := goodForRenew[pk.String()]; gfr {
			used = append(used, pk)
		}
	}

	// Update cache.
	id := r.mu.Lock()
	r.cachedUtilities = cachedUtilities{
		offline:      offline,
		goodForRenew: goodForRenew,
		contracts:    contracts,
		used:         used,
	}
	r.mu.Unlock(id)
}

// setBandwidthLimits will change the bandwidth limits of the renter based on
// the persist values for the bandwidth.
func (r *Renter) setBandwidthLimits(downloadSpeed int64, uploadSpeed int64) error {
	// Input validation.
	if downloadSpeed < 0 || uploadSpeed < 0 {
		return errors.New("download/upload rate limit can't be below 0")
	}

	// Check for sentinel "no limits" value.
	if downloadSpeed == 0 && uploadSpeed == 0 {
		r.rl.SetLimits(0, 0, 0)
	} else {
		// Set the rate limits according to the provided values.
		r.rl.SetLimits(downloadSpeed, uploadSpeed, 4*4096)
	}
	return nil
}

// SetSettings will update the settings for the renter.
//
// NOTE: This function can't be atomic. Typically we try to have user requests
// be atomic, so that either everything changes or nothing changes, but since
// these changes happen progressively, it's possible for some of the settings
// (like the allowance) to succeed, but then if the bandwidth limits for example
// are bad, then the allowance will update but the bandwidth will not update.
func (r *Renter) SetSettings(s modules.RenterSettings) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	// Early input validation.
	if s.MaxDownloadSpeed < 0 || s.MaxUploadSpeed < 0 {
		return errors.New("bandwidth limits cannot be negative")
	}

	// Set allowance.
	err := r.hostContractor.SetAllowance(s.Allowance)
	if err != nil {
		return err
	}

	// Set IPViolationsCheck
	r.hostDB.SetIPViolationCheck(s.IPViolationCheck)

	// Set the bandwidth limits.
	err = r.setBandwidthLimits(s.MaxDownloadSpeed, s.MaxUploadSpeed)
	if err != nil {
		return err
	}

	// Save the changes.
	id := r.mu.Lock()
	r.persist.MaxDownloadSpeed = s.MaxDownloadSpeed
	r.persist.MaxUploadSpeed = s.MaxUploadSpeed
	err = r.saveSync()
	r.mu.Unlock(id)
	if err != nil {
		return err
	}

	// Update the worker pool so that the changes are immediately apparent to
	// users.
	r.staticWorkerPool.callUpdate()
	return nil
}

// SetFileTrackingPath sets the on-disk location of an uploaded file to a new
// value. Useful if files need to be moved on disk. SetFileTrackingPath will
// check that a file exists at the new location and it ensures that it has the
// right size, but it can't check that the content is the same. Therefore the
// caller is responsible for not accidentally corrupting the uploaded file by
// providing a different file with the same size.
func (r *Renter) SetFileTrackingPath(siaPath modules.SiaPath, newPath string) (err error) {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	// Check if file exists and is being tracked.
	entry, err := r.staticFileSystem.OpenSiaFile(siaPath)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Compose(err, entry.Close())
	}()

	// Sanity check that a file with the correct size exists at the new
	// location.
	fi, err := os.Stat(newPath)
	if err != nil {
		return errors.AddContext(err, "failed to get fileinfo of the file")
	}
	if uint64(fi.Size()) != entry.Size() {
		return fmt.Errorf("file sizes don't match - want %v but got %v", entry.Size(), fi.Size())
	}

	// Set the new path on disk.
	return entry.SetLocalPath(newPath)
}

// ActiveHosts returns an array of hostDB's active hosts
func (r *Renter) ActiveHosts() ([]modules.HostDBEntry, error) { return r.hostDB.ActiveHosts() }

// AllHosts returns an array of all hosts
func (r *Renter) AllHosts() ([]modules.HostDBEntry, error) { return r.hostDB.AllHosts() }

// Filter returns the renter's hostdb's filterMode and filteredHosts
func (r *Renter) Filter() (modules.FilterMode, map[string]types.SiaPublicKey, []string, error) {
	var fm modules.FilterMode
	hosts := make(map[string]types.SiaPublicKey)
	if err := r.tg.Add(); err != nil {
		return fm, hosts, nil, err
	}
	defer r.tg.Done()
	fm, hosts, netAddresses, err := r.hostDB.Filter()
	if err != nil {
		return fm, hosts, netAddresses, errors.AddContext(err, "error getting hostdb filter:")
	}
	return fm, hosts, netAddresses, nil
}

// SetFilterMode sets the renter's hostdb filter mode
func (r *Renter) SetFilterMode(lm modules.FilterMode, hosts []types.SiaPublicKey, netAddresses []string) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	// Check to see how many hosts are needed for the allowance
	settings, err := r.Settings()
	if err != nil {
		return errors.AddContext(err, "error getting renter settings:")
	}
	minHosts := settings.Allowance.Hosts
	if len(hosts) < int(minHosts) && lm == modules.HostDBActiveWhitelist {
		r.log.Printf("WARN: There are fewer whitelisted hosts than the allowance requires.  Have %v whitelisted hosts, need %v to support allowance\n", len(hosts), minHosts)
	}

	// Set list mode filter for the hostdb
	if err := r.hostDB.SetFilterMode(lm, hosts, netAddresses); err != nil {
		return err
	}

	return nil
}

// Host returns the host associated with the given public key
func (r *Renter) Host(spk types.SiaPublicKey) (modules.HostDBEntry, bool, error) {
	return r.hostDB.Host(spk)
}

// InitialScanComplete returns a boolean indicating if the initial scan of the
// hostdb is completed.
func (r *Renter) InitialScanComplete() (bool, error) { return r.hostDB.InitialScanComplete() }

// ScoreBreakdown returns the score breakdown
func (r *Renter) ScoreBreakdown(e modules.HostDBEntry) (modules.HostScoreBreakdown, error) {
	return r.hostDB.ScoreBreakdown(e)
}

// EstimateHostScore returns the estimated host score
func (r *Renter) EstimateHostScore(e modules.HostDBEntry, a modules.Allowance) (modules.HostScoreBreakdown, error) {
	if reflect.DeepEqual(a, modules.Allowance{}) {
		settings, err := r.Settings()
		if err != nil {
			return modules.HostScoreBreakdown{}, errors.AddContext(err, "error getting renter settings:")
		}
		a = settings.Allowance
	}
	if reflect.DeepEqual(a, modules.Allowance{}) {
		a = modules.DefaultAllowance
	}
	return r.hostDB.EstimateHostScore(e, a)
}

// CancelContract cancels a renter's contract by ID by setting goodForRenew and goodForUpload to false
func (r *Renter) CancelContract(id types.FileContractID) error {
	return r.hostContractor.CancelContract(id)
}

// Contracts returns an array of host contractor's staticContracts
func (r *Renter) Contracts() []modules.RenterContract { return r.hostContractor.Contracts() }

// CurrentPeriod returns the host contractor's current period
func (r *Renter) CurrentPeriod() types.BlockHeight { return r.hostContractor.CurrentPeriod() }

// ContractUtility returns the utility field for a given contract, along
// with a bool indicating if it exists.
func (r *Renter) ContractUtility(pk types.SiaPublicKey) (modules.ContractUtility, bool) {
	return r.hostContractor.ContractUtility(pk)
}

// ContractStatus returns the status of the given contract within the watchdog,
// and a bool indicating whether or not it is being monitored.
func (r *Renter) ContractStatus(fcID types.FileContractID) (modules.ContractWatchStatus, bool) {
	return r.hostContractor.ContractStatus(fcID)
}

// ContractorChurnStatus returns contract churn stats for the current period.
func (r *Renter) ContractorChurnStatus() modules.ContractorChurnStatus {
	return r.hostContractor.ChurnStatus()
}

// InitRecoveryScan starts scanning the whole blockchain for recoverable
// contracts within a separate thread.
func (r *Renter) InitRecoveryScan() error {
	return r.hostContractor.InitRecoveryScan()
}

// RecoveryScanStatus returns a bool indicating if a scan for recoverable
// contracts is in progress and if it is, the current progress of the scan.
func (r *Renter) RecoveryScanStatus() (bool, types.BlockHeight) {
	return r.hostContractor.RecoveryScanStatus()
}

// OldContracts returns an array of host contractor's oldContracts
func (r *Renter) OldContracts() []modules.RenterContract {
	return r.hostContractor.OldContracts()
}

// PeriodSpending returns the host contractor's period spending
func (r *Renter) PeriodSpending() (modules.ContractorSpending, error) {
	return r.hostContractor.PeriodSpending()
}

// RecoverableContracts returns the host contractor's recoverable contracts.
func (r *Renter) RecoverableContracts() []modules.RecoverableContract {
	return r.hostContractor.RecoverableContracts()
}

// RefreshedContract returns a bool indicating if the contract was previously
// refreshed
func (r *Renter) RefreshedContract(fcid types.FileContractID) bool {
	return r.hostContractor.RefreshedContract(fcid)
}

// Settings returns the Renter's current settings.
func (r *Renter) Settings() (modules.RenterSettings, error) {
	if err := r.tg.Add(); err != nil {
		return modules.RenterSettings{}, err
	}
	defer r.tg.Done()
	download, upload, _ := r.rl.Limits()
	enabled, err := r.hostDB.IPViolationsCheck()
	if err != nil {
		return modules.RenterSettings{}, errors.AddContext(err, "error getting IPViolationsCheck:")
	}
	paused, endTime := r.uploadHeap.managedPauseStatus()
	return modules.RenterSettings{
		Allowance:        r.hostContractor.Allowance(),
		IPViolationCheck: enabled,
		MaxDownloadSpeed: download,
		MaxUploadSpeed:   upload,
		UploadsStatus: modules.UploadsStatus{
			Paused:       paused,
			PauseEndTime: endTime,
		},
	}, nil
}

// ProcessConsensusChange returns the process consensus change
func (r *Renter) ProcessConsensusChange(cc modules.ConsensusChange) {
	id := r.mu.Lock()
	r.lastEstimationHosts = []modules.HostDBEntry{}
	r.mu.Unlock(id)
	if cc.Synced {
		_ = r.tg.Launch(r.staticWorkerPool.callUpdate)
	}
}

// SetIPViolationCheck is a passthrough method to the hostdb's method of the
// same name.
func (r *Renter) SetIPViolationCheck(enabled bool) {
	r.hostDB.SetIPViolationCheck(enabled)
}

// MountInfo returns the list of currently mounted fusefilesystems.
func (r *Renter) MountInfo() []modules.MountInfo {
	return r.staticFuseManager.MountInfo()
}

// Mount mounts the files under the specified siapath under the 'mountPoint' folder on
// the local filesystem.
func (r *Renter) Mount(mountPoint string, sp modules.SiaPath, opts modules.MountOptions) error {
	return r.staticFuseManager.Mount(mountPoint, sp, opts)
}

// Unmount unmounts the fuse filesystem currently mounted at mountPoint.
func (r *Renter) Unmount(mountPoint string) error {
	return r.staticFuseManager.Unmount(mountPoint)
}

// Enforce that Renter satisfies the modules.Renter interface.
var _ modules.Renter = (*Renter)(nil)

// renterBlockingStartup handles the blocking portion of NewCustomRenter.
func renterBlockingStartup(g modules.Gateway, cs modules.ConsensusSet, tpool modules.TransactionPool, hdb modules.HostDB, w modules.Wallet, hc hostContractor, mux *siamux.SiaMux, persistDir string, rl *ratelimit.RateLimit, deps modules.Dependencies) (*Renter, error) {
	if g == nil {
		return nil, errNilGateway
	}
	if cs == nil {
		return nil, errNilCS
	}
	if tpool == nil {
		return nil, errNilTpool
	}
	if hc == nil {
		return nil, errNilContractor
	}
	if hdb == nil && build.Release != "testing" {
		return nil, errNilHdb
	}
	if w == nil {
		return nil, errNilWallet
	}

	r := &Renter{
		// Making newDownloads a buffered channel means that most of the time, a
		// new download will trigger an unnecessary extra iteration of the
		// download heap loop, searching for a chunk that's not there. This is
		// preferable to the alternative, where in rare cases the download heap
		// will miss work altogether.
		newDownloads: make(chan struct{}, 1),
		downloadHeap: new(downloadChunkHeap),

		uploadHeap: uploadHeap{
			repairingChunks:   make(map[uploadChunkID]*unfinishedUploadChunk),
			stuckHeapChunks:   make(map[uploadChunkID]*unfinishedUploadChunk),
			unstuckHeapChunks: make(map[uploadChunkID]*unfinishedUploadChunk),

			newUploads:        make(chan struct{}, 1),
			repairNeeded:      make(chan struct{}, 1),
			stuckChunkFound:   make(chan struct{}, 1),
			stuckChunkSuccess: make(chan struct{}, 1),

			pauseChan: make(chan struct{}),
		},
		directoryHeap: directoryHeap{
			heapDirectories: make(map[modules.SiaPath]*directory),
		},

		downloadHistory: make(map[modules.DownloadID]*download),

		cs:             cs,
		deps:           deps,
		g:              g,
		w:              w,
		hostDB:         hdb,
		hostContractor: hc,
		persistDir:     persistDir,
		rl:             rl,
		staticAlerter:  modules.NewAlerter("renter"),
		staticMux:      mux,
		mu:             siasync.New(modules.SafeMutexDelay, 1),
		tpool:          tpool,
	}
	r.staticBubbleScheduler = newBubbleScheduler(r)
	r.staticStreamBufferSet = newStreamBufferSet(&r.tg)
	r.staticUploadChunkDistributionQueue = newUploadChunkDistributionQueue(r)
	r.staticRRS = newReadRegistryStats(ReadRegistryBackgroundTimeout, readRegistryStatsInterval, readRegistryStatsDecay, readRegistryStatsPercentile)
	close(r.uploadHeap.pauseChan)

	// Seed the rrs.
	err := r.staticRRS.AddDatum(readRegistryStatsSeed)
	if err != nil {
		return nil, err
	}

	// Init the statsChan and close it right away to signal that no scan is
	// going on.
	r.statsChan = make(chan struct{})
	close(r.statsChan)

	// Initialize the loggers so that they are available for the components as
	// the components start up.
	r.log, err = persist.NewFileLogger(filepath.Join(r.persistDir, logFile))
	if err != nil {
		return nil, err
	}
	if err := r.tg.AfterStop(r.log.Close); err != nil {
		return nil, err
	}
	r.repairLog, err = persist.NewFileLogger(filepath.Join(r.persistDir, repairLogFile))
	if err != nil {
		return nil, err
	}
	if err := r.tg.AfterStop(r.repairLog.Close); err != nil {
		return nil, err
	}

	// Initialize some of the components.
	err = r.newAccountManager()
	if err != nil {
		return nil, errors.AddContext(err, "unable to create account manager")
	}

	r.registryMemoryManager = newMemoryManager(registryMemoryDefault, registryMemoryPriorityDefault, r.tg.StopChan())
	r.userUploadMemoryManager = newMemoryManager(userUploadMemoryDefault, userUploadMemoryPriorityDefault, r.tg.StopChan())
	r.userDownloadMemoryManager = newMemoryManager(userDownloadMemoryDefault, userDownloadMemoryPriorityDefault, r.tg.StopChan())
	r.repairMemoryManager = newMemoryManager(repairMemoryDefault, repairMemoryPriorityDefault, r.tg.StopChan())

	r.staticFuseManager = newFuseManager(r)
	r.stuckStack = callNewStuckStack()

	// Load all saved data.
	err = r.managedInitPersist()
	if err != nil {
		return nil, err
	}

	// After persist is initialized, create the worker pool.
	r.staticWorkerPool = r.newWorkerPool()

	// Set the worker pool on the contractor.
	r.hostContractor.UpdateWorkerPool(r.staticWorkerPool)

	// Calculate the initial cached utilities and kick off a thread that updates
	// the utilities regularly.
	r.managedUpdateRenterContractsAndUtilities()
	go r.threadedUpdateRenterContractsAndUtilities()

	// Spin up background threads which are not depending on the renter being
	// up-to-date with consensus.
	if !r.deps.Disrupt("DisableRepairAndHealthLoops") {
		// Push the root directory onto the directory heap for the repair process.
		err = r.managedPushUnexploredDirectory(modules.RootSiaPath())
		if err != nil {
			return nil, err
		}
		go r.threadedUpdateRenterHealth()
	}
	// We do not group the staticBubbleScheduler's background thread with the
	// threads disabled by "DisableRepairAndHealthLoops" so that manual calls to
	// for bubble updates are processed.
	go r.staticBubbleScheduler.callThreadedProcessBubbleUpdates()

	// Unsubscribe on shutdown.
	err = r.tg.OnStop(func() error {
		cs.Unsubscribe(r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

// renterAsyncStartup handles the non-blocking portion of NewCustomRenter.
func renterAsyncStartup(r *Renter, cs modules.ConsensusSet) error {
	if r.deps.Disrupt("BlockAsyncStartup") {
		return nil
	}
	// Subscribe to the consensus set in a separate goroutine.
	done := make(chan struct{})
	defer close(done)
	err := cs.ConsensusSetSubscribe(r, modules.ConsensusChangeRecent, r.tg.StopChan())
	if err != nil && strings.Contains(err.Error(), threadgroup.ErrStopped.Error()) {
		return err
	}
	if err != nil {
		return err
	}
	// Spin up the remaining background threads once we are caught up with the
	// consensus set.
	// Spin up the workers for the work pool.
	go r.threadedDownloadLoop()
	if !r.deps.Disrupt("DisableRepairAndHealthLoops") {
		go r.threadedUploadAndRepair()
		go r.threadedStuckFileLoop()
	}
	// Spin up the snapshot synchronization thread.
	if !r.deps.Disrupt("DisableSnapshotSync") {
		go r.threadedSynchronizeSnapshots()
	}
	return nil
}

// threadedUpdateRenterContractsAndUtilities periodically calls
// managedUpdateRenterContractsAndUtilities.
func (r *Renter) threadedUpdateRenterContractsAndUtilities() {
	err := r.tg.Add()
	if err != nil {
		return
	}
	defer r.tg.Done()
	for {
		select {
		case <-r.tg.StopChan():
			return
		case <-time.After(cachedUtilitiesUpdateInterval):
		}
		r.managedUpdateRenterContractsAndUtilities()
	}
}

// NewCustomRenter initializes a renter and returns it.
func NewCustomRenter(g modules.Gateway, cs modules.ConsensusSet, tpool modules.TransactionPool, hdb modules.HostDB, w modules.Wallet, hc hostContractor, mux *siamux.SiaMux, persistDir string, rl *ratelimit.RateLimit, deps modules.Dependencies) (*Renter, <-chan error) {
	errChan := make(chan error, 1)

	// Blocking startup.
	r, err := renterBlockingStartup(g, cs, tpool, hdb, w, hc, mux, persistDir, rl, deps)
	if err != nil {
		errChan <- err
		return nil, errChan
	}

	// non-blocking startup
	go func() {
		defer close(errChan)
		if err := r.tg.Add(); err != nil {
			errChan <- err
			return
		}
		defer r.tg.Done()
		err := renterAsyncStartup(r, cs)
		if err != nil {
			errChan <- err
		}
	}()
	return r, errChan
}

// New returns an initialized renter.
func New(g modules.Gateway, cs modules.ConsensusSet, wallet modules.Wallet, tpool modules.TransactionPool, mux *siamux.SiaMux, rl *ratelimit.RateLimit, persistDir string) (*Renter, <-chan error) {
	errChan := make(chan error, 1)
	hdb, errChanHDB := hostdb.New(g, cs, tpool, mux, persistDir)
	if err := modules.PeekErr(errChanHDB); err != nil {
		errChan <- err
		return nil, errChan
	}
	hc, errChanContractor := contractor.New(cs, wallet, tpool, hdb, rl, persistDir)
	if err := modules.PeekErr(errChanContractor); err != nil {
		errChan <- err
		return nil, errChan
	}
	renter, errChanRenter := NewCustomRenter(g, cs, tpool, hdb, wallet, hc, mux, persistDir, rl, modules.ProdDependencies)
	if err := modules.PeekErr(errChanRenter); err != nil {
		errChan <- err
		return nil, errChan
	}
	go func() {
		errChan <- errors.Compose(<-errChanHDB, <-errChanContractor, <-errChanRenter)
		close(errChan)
	}()
	return renter, errChan
}
