package modules

import (
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
)

const (
	// HostDir names the directory that contains the host persistence.
	HostDir = "host"
)

var (
	// BlockBytesPerMonthTerabyte is the conversion rate between block-bytes and month-TB.
	BlockBytesPerMonthTerabyte = BytesPerTerabyte.Mul64(uint64(types.BlocksPerMonth))

	// BytesPerTerabyte is the conversion rate between bytes and terabytes.
	BytesPerTerabyte = types.NewCurrency64(1e12)
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
		DataSize                 uint64               `json:"datasize"`
		LockedCollateral         types.Currency       `json:"lockedcollateral"`
		ObligationId             types.FileContractID `json:"obligationid"`
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

		// FinancialMetrics returns the financial statistics of the host.
		FinancialMetrics() HostFinancialMetrics

		// InternalSettings returns the host's internal settings, including
		// potentially private or sensitive information.
		InternalSettings() HostInternalSettings

		// NetworkMetrics returns information on the types of RPC calls that
		// have been made to the host.
		NetworkMetrics() HostNetworkMetrics

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

		// RemoveSector will remove a sector from the host. The height at which
		// the sector expires should be provided, so that the auto-expiry
		// information for that sector can be properly updated.
		RemoveSector(sectorRoot crypto.Hash) error

		// RemoveSectorBatch is a non-ACID performance optimization to remove a
		// ton of sectors from the host all at once. This is necessary when
		// clearing out an entire contract from the host.
		RemoveSectorBatch(sectorRoots []crypto.Hash) error

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
