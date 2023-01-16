package modules

import (
	"time"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/types"
)

const (
	// HostDir names the directory that contains the host persistence.
	HostDir = "host"

	// HostSettingsFile is the name of the host's persistence file.
	HostSettingsFile = "host.json"

	// HostSiaMuxSubscriberName is the name used by the host to register a
	// listener on the SiaMux.
	HostSiaMuxSubscriberName = "host"

	// HostWALFile is the name of the file the host's wal is stored in.
	HostWALFile = "host.wal"

	// HostRegistryFile is the name of the file the host's registry is stored
	// in.
	HostRegistryFile = "registry.dat"
)

var (
	// Hostv112PersistMetadata is the header of the v112 host persist file.
	Hostv112PersistMetadata = persist.Metadata{
		Header:  "Sia Host",
		Version: "0.5",
	}

	// Hostv120PersistMetadata is the header of the v120 host persist file.
	Hostv120PersistMetadata = persist.Metadata{
		Header:  "Sia Host",
		Version: "1.2.0",
	}

	// Hostv143PersistMetadata is the header of the v143 host persist file.
	Hostv143PersistMetadata = persist.Metadata{
		Header:  "Sia Host",
		Version: "1.4.3",
	}

	// Hostv151PersistMetadata is the header of the v151 host persist file.
	Hostv151PersistMetadata = persist.Metadata{
		Header:  "Sia Host",
		Version: "1.5.1",
	}
)

const (
	// DefaultMaxDuration defines the maximum number of blocks into the future
	// that the host will accept for the duration of an incoming file contract
	// obligation. 6 months is chosen because hosts are expected to be
	// long-term entities, and because we want to have a set of hosts that
	// support 6 month contracts when Sia leaves beta.
	DefaultMaxDuration = 144 * 30 * 6 // 6 months.
)

var (
	// DefaultMaxDownloadBatchSize defines the maximum number of bytes that the
	// host will allow to be requested by a single download request. 17 MiB has
	// been chosen because it's 4 full sectors plus some wiggle room. 17 MiB is
	// a conservative default, most hosts will be fine with a number like 65
	// MiB.
	DefaultMaxDownloadBatchSize = 17 * (1 << 20)

	// DefaultMaxReviseBatchSize defines the maximum number of bytes that the
	// host will allow to be sent during a single batch update in a revision
	// RPC. 17 MiB has been chosen because it's four full sectors, plus some
	// wiggle room for the extra data or a few delete operations. The whole
	// batch will be held in memory, so the batch size should only be increased
	// substantially if the host has a lot of memory. Additionally, the whole
	// batch is sent in one network connection. Additionally, the renter can
	// steal funds for upload bandwidth all the way out to the size of a batch.
	// 17 MiB is a conservative default, most hosts are likely to be just fine
	// with a number like 65 MiB.
	DefaultMaxReviseBatchSize = 17 * (1 << 20)

	// DefaultWindowSize is the size of the proof of storage window requested
	// by the host. The host will not delete any obligations until the window
	// has closed and buried under several confirmations. For release builds,
	// the default is set to 144 blocks, or about 1 day. This gives the host
	// flexibility to experience downtime without losing file contracts. The
	// optimal default, especially as the network matures, is probably closer
	// to 36 blocks. An experienced or high powered host should not be
	// frustrated by lost coins due to long periods of downtime.
	DefaultWindowSize = build.Select(build.Var{
		Dev:      types.BlockHeight(36),  // 3.6 minutes.
		Standard: types.BlockHeight(144), // 1 day.
		Testnet:  types.BlockHeight(144), // 1 day.
		Testing:  types.BlockHeight(5),   // 5 seconds.
	}).(types.BlockHeight)

	// DefaultBaseRPCPrice is the default price of talking to the host. It is
	// roughly equal to the default bandwidth cost of exchanging a pair of
	// 4096-byte messages.
	DefaultBaseRPCPrice = types.SiacoinPrecision.Mul64(100).Div64(1e9) // 100 nS

	// DefaultCollateral defines the amount of money that the host puts up as
	// collateral per-byte by default. The collateral should be considered as
	// an absolute instead of as a percentage, because low prices result in
	// collaterals which may be significant by percentage, but insignificant
	// overall. A default of 25 KS / TB / Month has been chosen, which is 2.5x
	// the default price for storage. The host is expected to put up a
	// significant amount of collateral as a commitment to faithfulness,
	// because this guarantees that the incentives are aligned for the host to
	// keep the data even if the price of siacoin fluctuates, the price of raw
	// storage fluctuates, or the host realizes that there is unexpected
	// opportunity cost in being a host.
	DefaultCollateral = types.SiacoinPrecision.Mul64(100).Div(BlockBytesPerMonthTerabyte) // 100 SC / TB / Month

	// DefaultMaxCollateral defines the maximum amount of collateral that the
	// host is comfortable putting into a single file contract. 10e3 is a
	// relatively small file contract, but millions of siacoins could be locked
	// away by only a few hundred file contracts. As the ecosystem matures, it
	// is expected that the safe default for this value will increase quite a
	// bit.
	DefaultMaxCollateral = types.SiacoinPrecision.Mul64(5e3)

	// DefaultContractPrice defines the default price of creating a contract
	// with the host. The current default is 0.1. This was chosen since it is
	// the minimum fee estimation of the transactionpool for a filecontract
	// transaction.
	DefaultContractPrice = types.SiacoinPrecision.Div64(100).Div64(1e3).Mul64(EstimatedFileContractRevisionAndProofTransactionSetSize)

	// DefaultDownloadBandwidthPrice defines the default price of upload
	// bandwidth. The default is set to 10 siacoins per gigabyte, because
	// download bandwidth is expected to be plentiful but also in-demand.
	DefaultDownloadBandwidthPrice = types.SiacoinPrecision.Mul64(25).Div(BytesPerTerabyte) // 25 SC / TB

	// DefaultEphemeralAccountExpiry defines the default maximum amount of
	// time an ephemeral account can be inactive before it expires and gets
	// deleted.
	DefaultEphemeralAccountExpiry = time.Minute * 60 * 24 * 7 // 1 week

	// DefaultMaxEphemeralAccountBalance defines the default maximum amount of
	// money that the host will allow to deposit into a single ephemeral account
	DefaultMaxEphemeralAccountBalance = types.SiacoinPrecision

	// DefaultSectorAccessPrice defines the default price of a sector access. It
	// is roughly equal to the cost of downloading 64 KiB.
	DefaultSectorAccessPrice = types.SiacoinPrecision.Mul64(2).Div64(1e6) // 2 uS

	// DefaultStoragePrice defines the starting price for hosts selling
	// storage. We try to match a number that is both reasonably profitable and
	// reasonably competitive.
	DefaultStoragePrice = types.SiacoinPrecision.Mul64(50).Div(BlockBytesPerMonthTerabyte) // 50 SC / TB / Month

	// DefaultUploadBandwidthPrice defines the default price of upload
	// bandwidth. The default is set to 1 siacoin per GB, because the host is
	// presumed to have a large amount of downstream bandwidth. Furthermore,
	// the host is typically only downloading data if it is planning to store
	// the data, meaning that the host serves to profit from accepting the
	// data.
	DefaultUploadBandwidthPrice = types.SiacoinPrecision.Mul64(1).Div(BytesPerTerabyte) // 1 SC / TB

	// CompatV1412DefaultEphemeralAccountExpiry defines the default account
	// expiry used up until v1.4.12. This constant is added to ensure changing
	// the default does not break legacy checks.
	CompatV1412DefaultEphemeralAccountExpiry = time.Minute * 60 * 24 * 7 // 1 week

	// CompatV1412DefaultMaxEphemeralAccountBalance defines the default maximum
	// ephemeral account balance used up until v1.4.12. This constant is added
	// to ensure changing the default does not break legacy checks.
	CompatV1412DefaultMaxEphemeralAccountBalance = types.SiacoinPrecision
)

var (
	// BlockBytesPerMonthTerabyte is the conversion rate between block-bytes and month-TB.
	BlockBytesPerMonthTerabyte = BytesPerTerabyte.Mul64(uint64(types.BlocksPerMonth))

	// BytesPerTerabyte is the conversion rate between bytes and terabytes.
	BytesPerTerabyte = types.NewCurrency64(1e12)

	// MaxBaseRPCPriceVsBandwidth is the max ratio for sane pricing between the
	// MinBaseRPCPrice and the MinDownloadBandwidthPrice. This ensures that 1
	// million base RPC charges are at most 1% of the cost to download 4TB. This
	// ratio should be used by checking that the MinBaseRPCPrice is less than or
	// equal to the MinDownloadBandwidthPrice multiplied by this constant
	MaxBaseRPCPriceVsBandwidth = uint64(40e3)

	// MaxSectorAccessPriceVsBandwidth is the max ratio for sane pricing between
	// the MinSectorAccessPrice and the MinDownloadBandwidthPrice. This ensures
	// that 1 million base accesses are at most 10% of the cost to download 4TB.
	// This ratio should be used by checking that the MinSectorAccessPrice is
	// less than or equal to the MinDownloadBandwidthPrice multiplied by this
	// constant
	MaxSectorAccessPriceVsBandwidth = uint64(400e3)
)

var (
	// HostConnectabilityStatusChecking is returned from ConnectabilityStatus()
	// if the host is still determining if it is connectable.
	HostConnectabilityStatusChecking = HostConnectabilityStatus("checking")

	// HostConnectabilityStatusConnectable is returned from
	// ConnectabilityStatus() if the host is connectable at its configured
	// netaddress.
	HostConnectabilityStatusConnectable = HostConnectabilityStatus("connectable")

	// HostConnectabilityStatusNotConnectable is returned from
	// ConnectabilityStatus() if the host is not connectable at its configured
	// netaddress.
	HostConnectabilityStatusNotConnectable = HostConnectabilityStatus("not connectable")

	// HostWorkingStatusChecking is returned from WorkingStatus() if the host is
	// still determining if it is working, that is, if settings calls are
	// incrementing.
	HostWorkingStatusChecking = HostWorkingStatus("checking")

	// HostWorkingStatusNotWorking is returned from WorkingStatus() if the host
	// has not received any settings calls over the duration of
	// workingStatusFrequency.
	HostWorkingStatusNotWorking = HostWorkingStatus("not working")

	// HostWorkingStatusWorking is returned from WorkingStatus() if the host has
	// received more than workingThreshold settings calls over the duration of
	// workingStatusFrequency.
	HostWorkingStatusWorking = HostWorkingStatus("working")
)

type (
	// HostFinancialMetrics provides financial statistics for the host,
	// including money that is locked in contracts. Though verbose, these
	// statistics should provide a clear picture of where the host's money is
	// currently being used. The front end can consolidate stats where desired.
	// Potential revenue refers to revenue that is available in a file
	// contract for which the file contract window has not yet closed.
	HostFinancialMetrics struct {
		// Metrics related to ephemeral accounts. Account funding represents all
		// funds used to fund ephemeral acccounts with for storage obligations
		// that were successful. Potential account funding represents the same
		// but for storage obligations that have not been confirmed yet.
		AccountFunding          types.Currency `json:"accountfunding"`
		PotentialAccountFunding types.Currency `json:"potentialaccountfunding"`

		// Every time a renter forms a contract with a host, a contract fee is
		// paid by the renter. These stats track the total contract fees.
		ContractCount                 uint64         `json:"contractcount"`
		ContractCompensation          types.Currency `json:"contractcompensation"`
		PotentialContractCompensation types.Currency `json:"potentialcontractcompensation"`

		// Metrics related to storage proofs, collateral, and submitting
		// transactions to the blockchain.
		LockedStorageCollateral types.Currency `json:"lockedstoragecollateral"`
		LostRevenue             types.Currency `json:"lostrevenue"`
		LostStorageCollateral   types.Currency `json:"loststoragecollateral"`
		PotentialStorageRevenue types.Currency `json:"potentialstoragerevenue"`
		RiskedStorageCollateral types.Currency `json:"riskedstoragecollateral"`
		StorageRevenue          types.Currency `json:"storagerevenue"`
		TransactionFeeExpenses  types.Currency `json:"transactionfeeexpenses"`

		// Bandwidth financial metrics.
		DownloadBandwidthRevenue          types.Currency `json:"downloadbandwidthrevenue"`
		PotentialDownloadBandwidthRevenue types.Currency `json:"potentialdownloadbandwidthrevenue"`
		PotentialUploadBandwidthRevenue   types.Currency `json:"potentialuploadbandwidthrevenue"`
		UploadBandwidthRevenue            types.Currency `json:"uploadbandwidthrevenue"`
	}

	// HostInternalSettings contains a list of settings that can be changed.
	HostInternalSettings struct {
		AcceptingContracts   bool              `json:"acceptingcontracts"`
		MaxDownloadBatchSize uint64            `json:"maxdownloadbatchsize"`
		MaxDuration          types.BlockHeight `json:"maxduration"`
		MaxReviseBatchSize   uint64            `json:"maxrevisebatchsize"`
		NetAddress           NetAddress        `json:"netaddress"`
		WindowSize           types.BlockHeight `json:"windowsize"`

		Collateral       types.Currency `json:"collateral"`
		CollateralBudget types.Currency `json:"collateralbudget"`
		MaxCollateral    types.Currency `json:"maxcollateral"`

		MinBaseRPCPrice           types.Currency `json:"minbaserpcprice"`
		MinContractPrice          types.Currency `json:"mincontractprice"`
		MinDownloadBandwidthPrice types.Currency `json:"mindownloadbandwidthprice"`
		MinSectorAccessPrice      types.Currency `json:"minsectoraccessprice"`
		MinStoragePrice           types.Currency `json:"minstorageprice"`
		MinUploadBandwidthPrice   types.Currency `json:"minuploadbandwidthprice"`

		EphemeralAccountExpiry     time.Duration  `json:"ephemeralaccountexpiry"`
		MaxEphemeralAccountBalance types.Currency `json:"maxephemeralaccountbalance"`
		MaxEphemeralAccountRisk    types.Currency `json:"maxephemeralaccountrisk"`

		CustomRegistryPath string `json:"customregistrypath"`
		RegistrySize       uint64 `json:"registrysize"`
	}

	// HostNetworkMetrics reports the quantity of each type of RPC call that
	// has been made to the host.
	HostNetworkMetrics struct {
		DownloadCalls     uint64 `json:"downloadcalls"`
		ErrorCalls        uint64 `json:"errorcalls"`
		FormContractCalls uint64 `json:"formcontractcalls"`
		RenewCalls        uint64 `json:"renewcalls"`
		ReviseCalls       uint64 `json:"revisecalls"`
		SettingsCalls     uint64 `json:"settingscalls"`
		UnrecognizedCalls uint64 `json:"unrecognizedcalls"`
	}

	// StorageObligation contains information about a storage obligation that
	// the host has accepted.
	StorageObligation struct {
		ContractCost             types.Currency       `json:"contractcost"`
		RevisionNumber           uint64               `json:"revisionnumber"`
		DataSize                 uint64               `json:"datasize"`
		LockedCollateral         types.Currency       `json:"lockedcollateral"`
		ObligationId             types.FileContractID `json:"obligationid"`
		PotentialAccountFunding  types.Currency       `json:"potentialaccountfunding"`
		PotentialDownloadRevenue types.Currency       `json:"potentialdownloadrevenue"`
		PotentialStorageRevenue  types.Currency       `json:"potentialstoragerevenue"`
		PotentialUploadRevenue   types.Currency       `json:"potentialuploadrevenue"`
		RiskedCollateral         types.Currency       `json:"riskedcollateral"`
		SectorRootsCount         uint64               `json:"sectorrootscount"`
		TransactionFeesAdded     types.Currency       `json:"transactionfeesadded"`
		TransactionID            types.TransactionID  `json:"transactionid"`

		// The negotiation height specifies the block height at which the file
		// contract was negotiated. The expiration height and the proof deadline
		// are equal to the window start and window end. Between the expiration height
		// and the proof deadline, the host must submit the storage proof.
		ExpirationHeight  types.BlockHeight `json:"expirationheight"`
		NegotiationHeight types.BlockHeight `json:"negotiationheight"`
		ProofDeadLine     types.BlockHeight `json:"proofdeadline"`

		// Variables indicating whether the critical transactions in a storage
		// obligation have been confirmed on the blockchain.
		ObligationStatus    string `json:"obligationstatus"`
		OriginConfirmed     bool   `json:"originconfirmed"`
		ProofConfirmed      bool   `json:"proofconfirmed"`
		ProofConstructed    bool   `json:"proofconstructed"`
		RevisionConfirmed   bool   `json:"revisionconfirmed"`
		RevisionConstructed bool   `json:"revisionconstructed"`

		// The outputs that will be created after the expiration of the contract
		// or a proof has been confirmed on the blockchain.
		ValidProofOutputs  []types.SiacoinOutput `json:"validproofoutputs"`
		MissedProofOutputs []types.SiacoinOutput `json:"missedproofoutputs"`
	}

	// HostWorkingStatus reports the working state of a host. Can be one of
	// "checking", "working", or "not working".
	HostWorkingStatus string

	// HostConnectabilityStatus reports the connectability state of a host. Can be
	// one of "checking", "connectable", or "not connectable"
	HostConnectabilityStatus string

	// A Host can take storage from disk and offer it to the network, managing
	// things such as announcements, settings, and implementing all of the RPCs
	// of the host protocol.
	Host interface {
		Alerter

		// AddSector will add a sector on the host. If the sector already
		// exists, a virtual sector will be added, meaning that the 'sectorData'
		// will be ignored and no new disk space will be consumed. The expiry
		// height is used to track what height the sector can be safely deleted
		// at, though typically the host will manually delete the sector before
		// the expiry height. The same sector can be added multiple times at
		// different expiry heights, and the host is expected to only store the
		// data once.
		AddSector(sectorRoot crypto.Hash, sectorData []byte) error

		// HasSector indicates whether the host stores a sector with a given
		// root or not.
		HasSector(crypto.Hash) bool

		// AddSectorBatch is a performance optimization over AddSector when
		// adding a bunch of virtual sectors. It is necessary because otherwise
		// potentially thousands or even tens-of-thousands of fsync calls would
		// need to be made in serial, which would prevent renters from ever
		// successfully renewing.
		AddSectorBatch(sectorRoots []crypto.Hash) error

		// AddStorageFolder adds a storage folder to the host. The host may not
		// check that there is enough space available on-disk to support as much
		// storage as requested, though the manager should gracefully handle
		// running out of storage unexpectedly.
		AddStorageFolder(path string, size uint64) error

		// Announce submits a host announcement to the blockchain.
		Announce() error

		// AnnounceAddress submits an announcement using the given address.
		AnnounceAddress(NetAddress) error

		// The host needs to be able to shut down.
		Close() error

		// ConnectabilityStatus returns the connectability status of the host,
		// that is, if it can connect to itself on the configured NetAddress.
		ConnectabilityStatus() HostConnectabilityStatus

		// DeleteSector deletes a sector, meaning that the host will be
		// unable to upload that sector and be unable to provide a storage
		// proof on that sector. DeleteSector is for removing the data
		// entirely, and will remove instances of the sector appearing at all
		// heights. The primary purpose of DeleteSector is to comply with legal
		// requests to remove data.
		DeleteSector(sectorRoot crypto.Hash) error

		// ExternalSettings returns the settings of the host as seen by an
		// untrusted node querying the host for settings.
		ExternalSettings() HostExternalSettings

		// BandwidthCounters returns the Hosts's upload and download bandwidth
		BandwidthCounters() (uint64, uint64, time.Time, error)

		// FinancialMetrics returns the financial statistics of the host.
		FinancialMetrics() HostFinancialMetrics

		// InternalSettings returns the host's internal settings, including
		// potentially private or sensitive information.
		InternalSettings() HostInternalSettings

		// NetworkMetrics returns information on the types of RPC calls that
		// have been made to the host.
		NetworkMetrics() HostNetworkMetrics

		PaymentProcessor

		// PriceTable returns the host's current price table.
		PriceTable() RPCPriceTable

		// PruneStaleStorageObligations will delete storage obligations from the
		// host that, for whatever reason, did not make it on the block chain.
		// As these stale storage obligations have an impact on the host
		// financial metrics, this method updates the host financial metrics to
		// show the correct values.
		PruneStaleStorageObligations() error

		// PublicKey returns the public key of the host.
		PublicKey() types.SiaPublicKey

		// ReadSector will read a sector from the host, returning the bytes that
		// match the input sector root.
		ReadSector(sectorRoot crypto.Hash) ([]byte, error)

		// ReadPartialSector will read a sector from the storage manager, returning the
		// 'length' bytes at offset 'offset' that match the input sector root.
		ReadPartialSector(sectorRoot crypto.Hash, offset, length uint64) ([]byte, error)

		// RemoveSector will remove a sector from the host. The height at which
		// the sector expires should be provided, so that the auto-expiry
		// information for that sector can be properly updated.
		RemoveSector(sectorRoot crypto.Hash) error

		// MarkSectorsForRemoval is a non-ACID performance optimization to
		// remove a ton of sectors from the host. Works around lockups in the
		// WAL by batching sectors to be removed over a period of time instead
		// of all at once.
		MarkSectorsForRemoval(sectorRoots []crypto.Hash) error

		// RemoveStorageFolder will remove a storage folder from the host. All
		// storage on the folder will be moved to other storage folders, meaning
		// that no data will be lost. If the host is unable to save data, an
		// error will be returned and the operation will be stopped. If the
		// force flag is set to true, errors will be ignored and the remove
		// operation will be completed, meaning that data will be lost.
		RemoveStorageFolder(index uint16, force bool) error

		// ResetStorageFolderHealth will reset the health statistics on a
		// storage folder.
		ResetStorageFolderHealth(index uint16) error

		// ResizeStorageFolder will grow or shrink a storage folder on the host.
		// The host may not check that there is enough space on-disk to support
		// growing the storage folder, but should gracefully handle running out
		// of space unexpectedly. When shrinking a storage folder, any data in
		// the folder that needs to be moved will be placed into other storage
		// folders, meaning that no data will be lost. If the manager is unable
		// to migrate the data, an error will be returned and the operation will
		// be stopped. If the force flag is set to true, errors will be ignored
		// and the resize operation completed, meaning that data will be lost.
		ResizeStorageFolder(index uint16, newSize uint64, force bool) error

		// SetInternalSettings sets the hosting parameters of the host.
		SetInternalSettings(HostInternalSettings) error

		// StorageObligation returns the storage obligation matching the id or
		// an error if it does not exist
		StorageObligation(obligationID types.FileContractID) (StorageObligation, error)

		// StorageObligations returns the set of storage obligations held by
		// the host.
		StorageObligations() []StorageObligation

		// StorageFolders will return a list of storage folders tracked by the
		// host.
		StorageFolders() []StorageFolderMetadata

		// WorkingStatus returns the working state of the host, determined by if
		// settings calls are increasing.
		WorkingStatus() HostWorkingStatus
	}
)

// MaxBaseRPCPrice returns the maximum value for the MinBaseRPCPrice based on
// the MinDownloadBandwidthPrice
func (his HostInternalSettings) MaxBaseRPCPrice() types.Currency {
	return his.MinDownloadBandwidthPrice.Mul64(MaxBaseRPCPriceVsBandwidth)
}

// MaxSectorAccessPrice returns the maximum value for the MinSectorAccessPrice
// based on the MinDownloadBandwidthPrice
func (his HostInternalSettings) MaxSectorAccessPrice() types.Currency {
	return his.MinDownloadBandwidthPrice.Mul64(MaxSectorAccessPriceVsBandwidth)
}

// DefaultHostExternalSettings returns HostExternalSettings with certain default
// fields set. NetAddress, RemainingStorage, TotalStorage, UnlockHash, RevisionNumber and SiaMuxPort are not set.
func DefaultHostExternalSettings() HostExternalSettings {
	return HostExternalSettings{
		AcceptingContracts:   true,
		MaxDownloadBatchSize: uint64(DefaultMaxDownloadBatchSize),
		MaxDuration:          DefaultMaxDuration,
		MaxReviseBatchSize:   uint64(DefaultMaxReviseBatchSize),
		SectorSize:           SectorSize,
		WindowSize:           DefaultWindowSize,

		Collateral:    DefaultCollateral,
		MaxCollateral: DefaultMaxCollateral,

		BaseRPCPrice:           DefaultBaseRPCPrice,
		ContractPrice:          DefaultContractPrice,
		DownloadBandwidthPrice: DefaultDownloadBandwidthPrice,
		SectorAccessPrice:      DefaultSectorAccessPrice,
		StoragePrice:           DefaultStoragePrice,
		UploadBandwidthPrice:   DefaultUploadBandwidthPrice,

		EphemeralAccountExpiry:     DefaultEphemeralAccountExpiry,
		MaxEphemeralAccountBalance: DefaultMaxEphemeralAccountBalance,

		Version: RHPVersion,
	}
}
