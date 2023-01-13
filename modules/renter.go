package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"gitlab.com/NebulousLabs/errors"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/types"
)

type (
	// ContractParams are supplied as an argument to FormContract.
	ContractParams struct {
		Allowance     Allowance
		Host          HostDBEntry
		Funding       types.Currency
		StartHeight   types.BlockHeight
		EndHeight     types.BlockHeight
		RefundAddress types.UnlockHash
		RenterSeed    EphemeralRenterSeed

		// TODO: add optional keypair
	}
)

// WorkerPool is an interface that describes a collection of workers. It's used
// to be able to pass the renter's workers to the contractor.
type WorkerPool interface {
	Worker(types.SiaPublicKey) (Worker, error)
}

// Worker is a minimal interface for a single worker. It's used to be able to
// use workers within the contractor.
type Worker interface {
	RenewContract(ctx context.Context, fcid types.FileContractID, params ContractParams, txnBuilder TransactionBuilder) (RenterContract, []types.Transaction, error)
}

var (
	// DefaultAllowance is the set of default allowance settings that will be
	// used when allowances are not set or not fully set
	DefaultAllowance = Allowance{
		Funds:       types.SiacoinPrecision.Mul64(2500),
		Hosts:       uint64(PriceEstimationScope),
		Period:      2 * types.BlocksPerMonth,
		RenewWindow: types.BlocksPerMonth,

		ExpectedStorage:    1e12,                                         // 1 TB
		ExpectedUpload:     uint64(200e9) / uint64(types.BlocksPerMonth), // 200 GB per month
		ExpectedDownload:   uint64(100e9) / uint64(types.BlocksPerMonth), // 100 GB per month
		ExpectedRedundancy: 3.0,                                          // default is 10/30 erasure coding
		MaxPeriodChurn:     uint64(250e9),                                // 250 GB
	}
	// ErrHostFault indicates if an error is the host's fault.
	ErrHostFault = errors.New("host has returned an error")

	// ErrDownloadCancelled is the error set when a download was cancelled
	// manually by the user.
	ErrDownloadCancelled = errors.New("download was cancelled")

	// ErrNotEnoughWorkersInWorkerPool is an error that is returned whenever an
	// operation expects a certain number of workers but there aren't that many
	// available.
	ErrNotEnoughWorkersInWorkerPool = errors.New("not enough workers in worker pool")

	// PriceEstimationScope is the number of hosts that get queried by the
	// renter when providing price estimates. Especially for the 'Standard'
	// variable, there should be congruence with the number of contracts being
	// used in the renter allowance.
	PriceEstimationScope = build.Select(build.Var{
		Standard: int(50),
		Testnet:  int(50),
		Dev:      int(12),
		Testing:  int(4),
	}).(int)
	// BackupKeySpecifier is a specifier that is hashed with the wallet seed to
	// create a key for encrypting backups.
	BackupKeySpecifier = types.NewSpecifier("backupkey")
)

// DataSourceID is an identifier to uniquely identify a data source, such as for
// loading a file. Adding data sources that have the same ID should return the
// exact same data when queried. This is typically used inside of the renter to
// build stream buffers.
type DataSourceID crypto.Hash

// FilterMode is the helper type for the enum constants for the HostDB filter
// mode
type FilterMode int

// FileListFunc is a type that's passed in to functions related to iterating
// over the filesystem.
type FileListFunc func(FileInfo)

// DirListFunc is a type that's passed in to functions related to iterating
// over the filesystem.
type DirListFunc func(DirectoryInfo)

// RenterStats is a struct which tracks key metrics in a single renter. This
// struct is intended to give a large overview / large dump of data related to
// the renter, which can then be aggregated across a fleet of renters by a
// program which monitors for inconsistencies or other challenges.
type RenterStats struct {
	// A name for this renter.
	Name string `json:"name"`

	// Any alerts that are in place for this renter.
	Alerts []Alert `json:"alerts"`

	// The total amount of contract data that hosts are maintaining on behalf of
	// the renter is the sum of these fields.
	ActiveContractData  uint64 `json:"activecontractdata"`
	PassiveContractData uint64 `json:"passivecontractdata"`
	WastedContractData  uint64 `json:"wastedcontractdata"`

	TotalSiafiles uint64 `json:"totalsiafiles"`
	TotalSiadirs  uint64 `json:"totalsiadirs"`
	TotalSize     uint64 `json:"totalsize"`

	TotalContractSpentFunds     types.Currency `json:"totalcontractspentfunds"` // Includes fees
	TotalContractSpentFees      types.Currency `json:"totalcontractspentfees"`
	TotalContractRemainingFunds types.Currency `json:"totalcontractremainingfunds"`

	AllowanceFunds              types.Currency `json:"allowancefunds"`
	AllowanceUnspentUnallocated types.Currency `json:"allowanceunspentunallocated"`
	WalletFunds                 types.Currency `json:"walletfunds"` // Includes unconfirmed

	// Information about the status of the memory queue. If the memory is all
	// used up, jobs will start blocking eachother.
	HasRenterMemory         bool `json:"hasrentermemory"`
	HasPriorityRenterMemory bool `json:"haspriorityrentermemory"`
}

// HostDBFilterError HostDBDisableFilter HostDBActivateBlacklist and
// HostDBActiveWhitelist are the constants used to enable and disable the filter
// mode of the renter's hostdb
const (
	HostDBFilterError FilterMode = iota
	HostDBDisableFilter
	HostDBActivateBlacklist
	HostDBActiveWhitelist
)

// Filesystem related consts.
const (
	// DefaultDirPerm defines the default permissions used for a new dir if no
	// permissions are supplied. Changing this value is a compatibility issue
	// since users expect dirs to have these permissions.
	DefaultDirPerm = 0755

	// DefaultFilePerm defines the default permissions used for a new file if no
	// permissions are supplied. Changing this value is a compatibility issue
	// since users expect files to have these permissions.
	DefaultFilePerm = 0644
)

// String returns the string value for the FilterMode
func (fm FilterMode) String() string {
	switch fm {
	case HostDBFilterError:
		return "error"
	case HostDBDisableFilter:
		return "disable"
	case HostDBActivateBlacklist:
		return "blacklist"
	case HostDBActiveWhitelist:
		return "whitelist"
	default:
		return ""
	}
}

// FromString assigned the FilterMode from the provide string
func (fm *FilterMode) FromString(s string) error {
	switch s {
	case "disable":
		*fm = HostDBDisableFilter
	case "blacklist":
		*fm = HostDBActivateBlacklist
	case "whitelist":
		*fm = HostDBActiveWhitelist
	default:
		*fm = HostDBFilterError
		return fmt.Errorf("could not assigned FilterMode from string %v", s)
	}
	return nil
}

// IsHostsFault indicates if a returned error is the host's fault.
func IsHostsFault(err error) bool {
	return errors.Contains(err, ErrHostFault)
}

const (
	// RenterDir is the name of the directory that is used to store the
	// renter's persistent data.
	RenterDir = "renter"

	// FileSystemRoot is the name of the directory that is used as the root of
	// the renter's filesystem.
	FileSystemRoot = "fs"

	// CombinedChunksRoot is the name of the directory that contains combined
	// chunks consisting of multiple partial chunks.
	CombinedChunksRoot = "combinedchunks"

	// EstimatedFileContractTransactionSetSize is the estimated blockchain size
	// of a transaction set between a renter and a host that contains a file
	// contract. This transaction set will contain a setup transaction from each
	// the host and the renter, and will also contain a file contract and file
	// contract revision that have each been signed by all parties.
	EstimatedFileContractTransactionSetSize = 2048

	// EstimatedFileContractRevisionAndProofTransactionSetSize is the
	// estimated blockchain size of a transaction set used by the host to
	// provide the storage proof at the end of the contract duration.
	EstimatedFileContractRevisionAndProofTransactionSetSize = 5000

	// StreamDownloadSize is the size of downloaded in a single streaming download
	// request.
	StreamDownloadSize = uint64(1 << 16) // 64 KiB

	// StreamUploadSize is the size of downloaded in a single streaming upload
	// request.
	StreamUploadSize = uint64(1 << 16) // 64 KiB
)

type (
	// DownloadID is a unique identifier used to identify downloads within the
	// download history.
	DownloadID string

	// CombinedChunkID is a unique identifier for a combined chunk which makes up
	// part of its filename on disk.
	CombinedChunkID string

	// PartialChunk holds some information about a combined chunk
	PartialChunk struct {
		ChunkID        CombinedChunkID // The ChunkID of the combined chunk the partial is in.
		InPartialsFile bool            // 'true' if the combined chunk is already in the partials siafile.
		Length         uint64          // length of the partial chunk within the combined chunk.
		Offset         uint64          // offset of the partial chunk within the combined chunk.
	}
)

// An Allowance dictates how much the Renter is allowed to spend in a given
// period. Note that funds are spent on both storage and bandwidth.
//
// NOTE: When changing the allowance struct, any new or adjusted fields are
// going to be loaded as blank when the contractor first starts up. The startup
// code either needs to set sane defaults, or the code which depends on the
// values needs to appropriately handle the values being empty.
type Allowance struct {
	Funds       types.Currency    `json:"funds"`
	Hosts       uint64            `json:"hosts"`
	Period      types.BlockHeight `json:"period"`
	RenewWindow types.BlockHeight `json:"renewwindow"`

	// ExpectedStorage is the amount of data that we expect to have in a contract.
	ExpectedStorage uint64 `json:"expectedstorage"`

	// ExpectedUpload is the expected amount of data uploaded through the API,
	// before redundancy, per block.
	ExpectedUpload uint64 `json:"expectedupload"`

	// ExpectedDownload is the expected amount of data downloaded through the
	// API per block.
	ExpectedDownload uint64 `json:"expecteddownload"`

	// ExpectedRedundancy is the average redundancy of files being uploaded.
	ExpectedRedundancy float64 `json:"expectedredundancy"`

	// MaxPeriodChurn is maximum amount of contract churn allowed in a single
	// period.
	MaxPeriodChurn uint64 `json:"maxperiodchurn"`

	// The following fields provide price gouging protection for the user. By
	// setting a particular maximum price for each mechanism that a host can use
	// to charge users, the workers know to avoid hosts that go outside of the
	// safety range.
	//
	// The intention is that if the fields are not set, a reasonable value will
	// be derived from the other allowance settings. The intention is that the
	// hostdb will pay attention to these limits when forming contracts,
	// understanding that a certain feature (such as storage) will not be used
	// if the host price is above the limit. If the hostdb believes that a host
	// is valuable for its other, more reasonably priced features, the hostdb
	// may choose to form a contract with the host anyway.
	//
	// NOTE: If the allowance max price fields are ever extended, all of the
	// price gouging checks throughout the worker code and contract formation
	// code also need to be extended.
	MaxRPCPrice               types.Currency `json:"maxrpcprice"`
	MaxContractPrice          types.Currency `json:"maxcontractprice"`
	MaxDownloadBandwidthPrice types.Currency `json:"maxdownloadbandwidthprice"`
	MaxSectorAccessPrice      types.Currency `json:"maxsectoraccessprice"`
	MaxStoragePrice           types.Currency `json:"maxstorageprice"`
	MaxUploadBandwidthPrice   types.Currency `json:"maxuploadbandwidthprice"`
}

// Active returns true if and only if this allowance has been set in the
// contractor.
func (a Allowance) Active() bool {
	return a.Period != 0
}

// ContractUtility contains metrics internal to the contractor that reflect the
// utility of a given contract.
type ContractUtility struct {
	GoodForUpload bool `json:"goodforupload"`
	GoodForRenew  bool `json:"goodforrenew"`

	// BadContract will be set to true if there's good reason to believe that
	// the contract is unusable and will continue to be unusable. For example,
	// if the host is claiming that the contract does not exist, the contract
	// should be marked as bad.
	BadContract bool              `json:"badcontract"`
	LastOOSErr  types.BlockHeight `json:"lastooserr"` // OOS means Out Of Storage

	// If a contract is locked, the utility should not be updated. 'Locked' is a
	// value that gets persisted.
	Locked bool `json:"locked"`
}

// ContractWatchStatus provides information about the status of a contract in
// the renter's watchdog.
type ContractWatchStatus struct {
	Archived                  bool              `json:"archived"`
	FormationSweepHeight      types.BlockHeight `json:"formationsweepheight"`
	ContractFound             bool              `json:"contractfound"`
	LatestRevisionFound       uint64            `json:"latestrevisionfound"`
	StorageProofFoundAtHeight types.BlockHeight `json:"storageprooffoundatheight"`
	DoubleSpendHeight         types.BlockHeight `json:"doublespendheight"`
	WindowStart               types.BlockHeight `json:"windowstart"`
	WindowEnd                 types.BlockHeight `json:"windowend"`
}

// DirectoryInfo provides information about a siadir
type DirectoryInfo struct {
	// The following fields are aggregate values of the siadir. These values are
	// the totals of the siadir and any sub siadirs, or are calculated based on
	// all the values in the subtree
	AggregateHealth              float64   `json:"aggregatehealth"`
	AggregateLastHealthCheckTime time.Time `json:"aggregatelasthealthchecktime"`
	AggregateMaxHealth           float64   `json:"aggregatemaxhealth"`
	AggregateMaxHealthPercentage float64   `json:"aggregatemaxhealthpercentage"`
	AggregateMinRedundancy       float64   `json:"aggregateminredundancy"`
	AggregateMostRecentModTime   time.Time `json:"aggregatemostrecentmodtime"`
	AggregateNumFiles            uint64    `json:"aggregatenumfiles"`
	AggregateNumStuckChunks      uint64    `json:"aggregatenumstuckchunks"`
	AggregateNumSubDirs          uint64    `json:"aggregatenumsubdirs"`
	AggregateRepairSize          uint64    `json:"aggregaterepairsize"`
	AggregateSize                uint64    `json:"aggregatesize"`
	AggregateStuckHealth         float64   `json:"aggregatestuckhealth"`
	AggregateStuckSize           uint64    `json:"aggregatestucksize"`

	// The following fields are information specific to the siadir that is not
	// an aggregate of the entire sub directory tree
	Health              float64     `json:"health"`
	LastHealthCheckTime time.Time   `json:"lasthealthchecktime"`
	MaxHealthPercentage float64     `json:"maxhealthpercentage"`
	MaxHealth           float64     `json:"maxhealth"`
	MinRedundancy       float64     `json:"minredundancy"`
	DirMode             os.FileMode `json:"mode,siamismatch"` // Field is called DirMode for fuse compatibility
	MostRecentModTime   time.Time   `json:"mostrecentmodtime"`
	NumFiles            uint64      `json:"numfiles"`
	NumStuckChunks      uint64      `json:"numstuckchunks"`
	NumSubDirs          uint64      `json:"numsubdirs"`
	RepairSize          uint64      `json:"repairsize"`
	SiaPath             SiaPath     `json:"siapath"`
	DirSize             uint64      `json:"size,siamismatch"` // Stays as 'size' in json for compatibility
	StuckHealth         float64     `json:"stuckhealth"`
	StuckSize           uint64      `json:"stucksize"`
	UID                 uint64      `json:"uid"`
}

// Name implements os.FileInfo.
func (d DirectoryInfo) Name() string { return d.SiaPath.Name() }

// Size implements os.FileInfo.
func (d DirectoryInfo) Size() int64 { return int64(d.DirSize) }

// Mode implements os.FileInfo.
func (d DirectoryInfo) Mode() os.FileMode { return d.DirMode }

// ModTime implements os.FileInfo.
func (d DirectoryInfo) ModTime() time.Time { return d.MostRecentModTime }

// IsDir implements os.FileInfo.
func (d DirectoryInfo) IsDir() bool { return true }

// Sys implements os.FileInfo.
func (d DirectoryInfo) Sys() interface{} { return nil }

// DownloadInfo provides information about a file that has been requested for
// download.
type DownloadInfo struct {
	Destination     string  `json:"destination"`     // The destination of the download.
	DestinationType string  `json:"destinationtype"` // Can be "file", "memory buffer", or "http stream".
	Length          uint64  `json:"length"`          // The length requested for the download.
	Offset          uint64  `json:"offset"`          // The offset within the siafile requested for the download.
	SiaPath         SiaPath `json:"siapath"`         // The siapath of the file used for the download.

	Completed            bool      `json:"completed"`            // Whether or not the download has completed.
	EndTime              time.Time `json:"endtime"`              // The time when the download fully completed.
	Error                string    `json:"error"`                // Will be the empty string unless there was an error.
	Received             uint64    `json:"received"`             // Amount of data confirmed and decoded.
	StartTime            time.Time `json:"starttime"`            // The time when the download was started.
	StartTimeUnix        int64     `json:"starttimeunix"`        // The time when the download was started in unix format.
	TotalDataTransferred uint64    `json:"totaldatatransferred"` // Total amount of data transferred, including negotiation, etc.
}

// FileUploadParams contains the information used by the Renter to upload a
// file.
type FileUploadParams struct {
	Source              string
	SiaPath             SiaPath
	ErasureCode         ErasureCoder
	Force               bool
	DisablePartialChunk bool
	Repair              bool

	// CipherType was added later. If it is left blank, the renter will use the
	// default encryption method (as of writing, Threefish)
	CipherType crypto.CipherType

	// CipherKey was added in v1.5.0. If it is left blank, the renter will use it
	// to create a CipherKey with the given CipherType. This value override
	// CipherType if it is set.
	CipherKey crypto.CipherKey
}

// FileInfo provides information about a file.
type FileInfo struct {
	AccessTime       time.Time         `json:"accesstime"`
	Available        bool              `json:"available"`
	ChangeTime       time.Time         `json:"changetime"`
	CipherType       string            `json:"ciphertype"`
	CreateTime       time.Time         `json:"createtime"`
	Expiration       types.BlockHeight `json:"expiration"`
	Filesize         uint64            `json:"filesize"`
	Health           float64           `json:"health"`
	LocalPath        string            `json:"localpath"`
	MaxHealth        float64           `json:"maxhealth"`
	MaxHealthPercent float64           `json:"maxhealthpercent"`
	ModificationTime time.Time         `json:"modtime,siamismatch"` // Stays as 'modtime' in json for compatibility
	FileMode         os.FileMode       `json:"mode,siamismatch"`    // Field is called FileMode for fuse compatibility
	NumStuckChunks   uint64            `json:"numstuckchunks"`
	OnDisk           bool              `json:"ondisk"`
	Recoverable      bool              `json:"recoverable"`
	Redundancy       float64           `json:"redundancy"`
	Renewing         bool              `json:"renewing"`
	RepairBytes      uint64            `json:"repairbytes"`
	Skylinks         []string          `json:"skylinks"`
	SiaPath          SiaPath           `json:"siapath"`
	Stuck            bool              `json:"stuck"`
	StuckBytes       uint64            `json:"stuckbytes"`
	StuckHealth      float64           `json:"stuckhealth"`
	UID              uint64            `json:"uid"`
	UploadedBytes    uint64            `json:"uploadedbytes"`
	UploadProgress   float64           `json:"uploadprogress"`
}

// Name implements os.FileInfo.
func (f FileInfo) Name() string { return f.SiaPath.Name() }

// Size implements os.FileInfo.
func (f FileInfo) Size() int64 { return int64(f.Filesize) }

// Mode implements os.FileInfo.
func (f FileInfo) Mode() os.FileMode { return f.FileMode }

// ModTime implements os.FileInfo.
func (f FileInfo) ModTime() time.Time { return f.ModificationTime }

// IsDir implements os.FileInfo.
func (f FileInfo) IsDir() bool { return false }

// Sys implements os.FileInfo.
func (f FileInfo) Sys() interface{} { return nil }

// A HostDBEntry represents one host entry in the Renter's host DB. It
// aggregates the host's external settings and metrics with its public key.
type HostDBEntry struct {
	HostExternalSettings

	// FirstSeen is the last block height at which this host was announced.
	FirstSeen types.BlockHeight `json:"firstseen"`

	// Measurements that have been taken on the host. The most recent
	// measurements are kept in full detail, historic ones are compressed into
	// the historic values.
	HistoricDowntime time.Duration `json:"historicdowntime"`
	HistoricUptime   time.Duration `json:"historicuptime"`
	ScanHistory      HostDBScans   `json:"scanhistory"`

	// Measurements that are taken whenever we interact with a host.
	HistoricFailedInteractions     float64 `json:"historicfailedinteractions"`
	HistoricSuccessfulInteractions float64 `json:"historicsuccessfulinteractions"`
	RecentFailedInteractions       float64 `json:"recentfailedinteractions"`
	RecentSuccessfulInteractions   float64 `json:"recentsuccessfulinteractions"`

	LastHistoricUpdate types.BlockHeight `json:"lasthistoricupdate"`

	// Measurements related to the IP subnet mask.
	IPNets          []string  `json:"ipnets"`
	LastIPNetChange time.Time `json:"lastipnetchange"`

	// The public key of the host, stored separately to minimize risk of certain
	// MitM based vulnerabilities.
	PublicKey types.SiaPublicKey `json:"publickey"`

	// Filtered says whether or not a HostDBEntry is being filtered out of the
	// filtered hosttree due to the filter mode of the hosttree
	Filtered bool `json:"filtered"`
}

// HostDBScan represents a single scan event.
type HostDBScan struct {
	Timestamp time.Time `json:"timestamp"`
	Success   bool      `json:"success"`
}

// HostScoreBreakdown provides a piece-by-piece explanation of why a host has
// the score that they do.
//
// NOTE: Renters are free to use whatever scoring they feel appropriate for
// hosts. Some renters will outright blacklist or whitelist sets of hosts. The
// results provided by this struct can only be used as a guide, and may vary
// significantly from machine to machine.
type HostScoreBreakdown struct {
	Score          types.Currency `json:"score"`
	ConversionRate float64        `json:"conversionrate"`

	AcceptContractAdjustment   float64 `json:"acceptcontractadjustment"`
	AgeAdjustment              float64 `json:"ageadjustment"`
	BasePriceAdjustment        float64 `json:"basepriceadjustment"`
	BurnAdjustment             float64 `json:"burnadjustment"`
	CollateralAdjustment       float64 `json:"collateraladjustment"`
	DurationAdjustment         float64 `json:"durationadjustment"`
	InteractionAdjustment      float64 `json:"interactionadjustment"`
	PriceAdjustment            float64 `json:"pricesmultiplier,siamismatch"`
	StorageRemainingAdjustment float64 `json:"storageremainingadjustment"`
	UptimeAdjustment           float64 `json:"uptimeadjustment"`
	VersionAdjustment          float64 `json:"versionadjustment"`
}

// MemoryStatus contains information about the status of the memory managers in
// the renter.
type MemoryStatus struct {
	MemoryManagerStatus

	Registry     MemoryManagerStatus `json:"registry"`
	UserUpload   MemoryManagerStatus `json:"userupload"`
	UserDownload MemoryManagerStatus `json:"userdownload"`
	System       MemoryManagerStatus `json:"system"`
}

// MemoryManagerStatus contains the memory status of a single memory manager.
type MemoryManagerStatus struct {
	Available uint64 `json:"available"`
	Base      uint64 `json:"base"`
	Requested uint64 `json:"requested"`

	PriorityAvailable uint64 `json:"priorityavailable"`
	PriorityBase      uint64 `json:"prioritybase"`
	PriorityRequested uint64 `json:"priorityrequested"`
	PriorityReserve   uint64 `json:"priorityreserve"`
}

// Add combines two MemoryManagerStatus objects into one.
func (ms MemoryManagerStatus) Add(ms2 MemoryManagerStatus) MemoryManagerStatus {
	return MemoryManagerStatus{
		Available:         ms.Available + ms2.Available,
		Base:              ms.Base + ms2.Base,
		Requested:         ms.Requested + ms2.Requested,
		PriorityAvailable: ms.PriorityAvailable + ms2.PriorityAvailable,
		PriorityBase:      ms.PriorityBase + ms2.PriorityBase,
		PriorityRequested: ms.PriorityRequested + ms2.PriorityRequested,
		PriorityReserve:   ms.PriorityReserve + ms2.PriorityReserve,
	}
}

// MountInfo contains information about a mounted FUSE filesystem.
type MountInfo struct {
	MountPoint string  `json:"mountpoint"`
	SiaPath    SiaPath `json:"siapath"`

	MountOptions MountOptions `json:"mountoptions"`
}

// RenterPriceEstimation contains a bunch of files estimating the costs of
// various operations on the network.
type RenterPriceEstimation struct {
	// The cost of downloading 1 TB of data.
	DownloadTerabyte types.Currency `json:"downloadterabyte"`

	// The cost of forming a set of contracts using the defaults.
	FormContracts types.Currency `json:"formcontracts"`

	// The cost of storing 1 TB for a month, including redundancy.
	StorageTerabyteMonth types.Currency `json:"storageterabytemonth"`

	// The cost of consuming 1 TB of upload bandwidth from the host, including
	// redundancy.
	UploadTerabyte types.Currency `json:"uploadterabyte"`
}

// RenterSettings control the behavior of the Renter.
type RenterSettings struct {
	Allowance        Allowance     `json:"allowance"`
	IPViolationCheck bool          `json:"ipviolationcheck"`
	MaxUploadSpeed   int64         `json:"maxuploadspeed"`
	MaxDownloadSpeed int64         `json:"maxdownloadspeed"`
	UploadsStatus    UploadsStatus `json:"uploadsstatus"`
}

// UploadsStatus contains information about the Renter's Uploads
type UploadsStatus struct {
	Paused       bool      `json:"paused"`
	PauseEndTime time.Time `json:"pauseendtime"`
}

// HostDBScans represents a sortable slice of scans.
type HostDBScans []HostDBScan

func (s HostDBScans) Len() int           { return len(s) }
func (s HostDBScans) Less(i, j int) bool { return s[i].Timestamp.Before(s[j].Timestamp) }
func (s HostDBScans) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// MerkleRootSet is a set of Merkle roots, and gets encoded more efficiently.
type MerkleRootSet []crypto.Hash

// MarshalJSON defines a JSON encoding for a MerkleRootSet.
func (mrs MerkleRootSet) MarshalJSON() ([]byte, error) {
	// Copy the whole array into a giant byte slice and then encode that.
	fullBytes := make([]byte, crypto.HashSize*len(mrs))
	for i := range mrs {
		copy(fullBytes[i*crypto.HashSize:(i+1)*crypto.HashSize], mrs[i][:])
	}
	return json.Marshal(fullBytes)
}

// UnmarshalJSON attempts to decode a MerkleRootSet, falling back on the legacy
// decoding of a []crypto.Hash if that fails.
func (mrs *MerkleRootSet) UnmarshalJSON(b []byte) error {
	// Decode the giant byte slice, and then split it into separate arrays.
	var fullBytes []byte
	err := json.Unmarshal(b, &fullBytes)
	if err != nil {
		// Encoding the byte slice has failed, try decoding it as a []crypto.Hash.
		var hashes []crypto.Hash
		err := json.Unmarshal(b, &hashes)
		if err != nil {
			return err
		}
		*mrs = MerkleRootSet(hashes)
		return nil
	}

	umrs := make(MerkleRootSet, len(fullBytes)/32)
	for i := range umrs {
		copy(umrs[i][:], fullBytes[i*crypto.HashSize:(i+1)*crypto.HashSize])
	}
	*mrs = umrs
	return nil
}

// MountOptions specify various settings of a FUSE filesystem mount.
type MountOptions struct {
	AllowOther bool `json:"allowother"`
	ReadOnly   bool `json:"readonly"`
}

// RecoverableContract is a types.FileContract as it appears on the blockchain
// with additional fields which contain the information required to recover its
// latest revision from a host.
type RecoverableContract struct {
	types.FileContract
	// ID is the FileContract's ID.
	ID types.FileContractID `json:"id"`
	// HostPublicKey is the public key of the host we formed this contract
	// with.
	HostPublicKey types.SiaPublicKey `json:"hostpublickey"`
	// InputParentID is the ParentID of the first SiacoinInput of the
	// transaction that contains this contract.
	InputParentID types.SiacoinOutputID `json:"inputparentid"`
	// StartHeight is the estimated startheight of a recoverable contract.
	StartHeight types.BlockHeight `json:"startheight"`
	// TxnFee of the transaction which contains the contract.
	TxnFee types.Currency `json:"txnfee"`
}

// A RenterContract contains metadata about a file contract. It is read-only;
// modifying a RenterContract does not modify the actual file contract.
type RenterContract struct {
	ID            types.FileContractID
	HostPublicKey types.SiaPublicKey
	Transaction   types.Transaction

	StartHeight types.BlockHeight
	EndHeight   types.BlockHeight

	// RenterFunds is the amount remaining in the contract that the renter can
	// spend.
	RenterFunds types.Currency

	// The FileContract does not indicate what funds were spent on, so we have
	// to track the various costs manually.
	DownloadSpending    types.Currency
	FundAccountSpending types.Currency
	MaintenanceSpending MaintenanceSpending
	StorageSpending     types.Currency
	UploadSpending      types.Currency

	// Utility contains utility information about the renter.
	Utility ContractUtility

	// TotalCost indicates the amount of money that the renter spent and/or
	// locked up while forming a contract. This includes fees, and includes
	// funds which were allocated (but not necessarily committed) to spend on
	// uploads/downloads/storage.
	TotalCost types.Currency

	// ContractFee is the amount of money paid to the host to cover potential
	// future transaction fees that the host may incur, and to cover any other
	// overheads the host may have.
	//
	// TxnFee is the amount of money spent on the transaction fee when putting
	// the renter contract on the blockchain.
	//
	// SiafundFee is the amount of money spent on siafund fees when creating the
	// contract. The siafund fee that the renter pays covers both the renter and
	// the host portions of the contract, and therefore can be unexpectedly high
	// if the the host collateral is high.
	ContractFee types.Currency
	TxnFee      types.Currency
	SiafundFee  types.Currency
}

// SpendingDetails is a helper struct that contains a breakdown of where exactly
// the money was spent. The MaintenanceSpending field is an aggregate of costs
// spent on RHP3 maintenance, this includes updating the price table, syncing
// the account balance, etc.
type SpendingDetails struct {
	DownloadSpending    types.Currency
	FundAccountSpending types.Currency
	MaintenanceSpending MaintenanceSpending
	StorageSpending     types.Currency
	UploadSpending      types.Currency
}

// MaintenanceSpending is a helper struct that contains a breakdown of costs
// related to the maintenance (a.k.a upkeep) of the RHP3 protocol. This includes
// the costs to sync the account balance, update the price table, etc.
type MaintenanceSpending struct {
	AccountBalanceCost   types.Currency `json:"accountbalancecost"`
	FundAccountCost      types.Currency `json:"fundaccountcost"`
	UpdatePriceTableCost types.Currency `json:"updatepricetablecost"`
}

// Add is a convenience function that sums the fields of the spending object
// with the corresponding fields of the given object.
func (x MaintenanceSpending) Add(y MaintenanceSpending) MaintenanceSpending {
	return MaintenanceSpending{
		AccountBalanceCost:   x.AccountBalanceCost.Add(y.AccountBalanceCost),
		FundAccountCost:      x.FundAccountCost.Add(y.FundAccountCost),
		UpdatePriceTableCost: x.UpdatePriceTableCost.Add(y.UpdatePriceTableCost),
	}
}

// Sum is a convenience function that sums up all of the fields in the spending
// object and returns the total as a types.Currency.
func (x MaintenanceSpending) Sum() types.Currency {
	return x.AccountBalanceCost.Add(x.FundAccountCost).Add(x.UpdatePriceTableCost)
}

// Size returns the contract size
func (rc *RenterContract) Size() uint64 {
	var size uint64
	if len(rc.Transaction.FileContractRevisions) != 0 {
		size = rc.Transaction.FileContractRevisions[0].NewFileSize
	}
	return size
}

// ContractorSpending contains the metrics about how much the Contractor has
// spent during the current billing period.
type ContractorSpending struct {
	// ContractFees are the sum of all fees in the contract. This means it
	// includes the ContractFee, TxnFee and SiafundFee
	ContractFees types.Currency `json:"contractfees"`
	// DownloadSpending is the money currently spent on downloads.
	DownloadSpending types.Currency `json:"downloadspending"`
	// FundAccountSpending is the money used to fund an ephemeral account on the
	// host.
	FundAccountSpending types.Currency `json:"fundaccountspending"`
	// MaintenanceSpending is the money spent on maintenance tasks in support of
	// the RHP3 protocol, this includes updating the price table as well as
	// syncing the ephemeral account balance.
	MaintenanceSpending MaintenanceSpending `json:"maintenancespending"`
	// StorageSpending is the money currently spent on storage.
	StorageSpending types.Currency `json:"storagespending"`
	// ContractSpending is the total amount of money that the renter has put
	// into contracts, whether it's locked and the renter gets that money
	// back or whether it's spent and the renter won't get the money back.
	TotalAllocated types.Currency `json:"totalallocated"`
	// UploadSpending is the money currently spent on uploads.
	UploadSpending types.Currency `json:"uploadspending"`
	// Unspent is locked-away, unspent money.
	Unspent types.Currency `json:"unspent"`
	// ContractSpendingDeprecated was renamed to TotalAllocated and always has the
	// same value as TotalAllocated.
	ContractSpendingDeprecated types.Currency `json:"contractspending,siamismatch"`
	// WithheldFunds are the funds from the previous period that are tied up
	// in contracts and have not been released yet
	WithheldFunds types.Currency `json:"withheldfunds"`
	// ReleaseBlock is the block at which the WithheldFunds should be
	// released to the renter, based on worst case.
	// Contract End Height + Host Window Size + Maturity Delay
	ReleaseBlock types.BlockHeight `json:"releaseblock"`
	// PreviousSpending is the total spend funds from old contracts
	// that are not included in the current period spending
	PreviousSpending types.Currency `json:"previousspending"`
}

// SpendingBreakdown provides a breakdown of a few fields in the Contractor
// Spending
func (cs ContractorSpending) SpendingBreakdown() (totalSpent, unspentAllocated, unspentUnallocated types.Currency) {
	totalSpent = cs.ContractFees.Add(cs.UploadSpending).
		Add(cs.DownloadSpending).Add(cs.StorageSpending)
	// Calculate unspent allocated
	if cs.TotalAllocated.Cmp(totalSpent) >= 0 {
		unspentAllocated = cs.TotalAllocated.Sub(totalSpent)
	}
	// Calculate unspent unallocated
	if cs.Unspent.Cmp(unspentAllocated) >= 0 {
		unspentUnallocated = cs.Unspent.Sub(unspentAllocated)
	}
	return totalSpent, unspentAllocated, unspentUnallocated
}

// ContractorChurnStatus contains the current churn budgets for the Contractor's
// churnLimiter and the aggregate churn for the current period.
type ContractorChurnStatus struct {
	// AggregateCurrentPeriodChurn is the total size of files from churned contracts in this
	// period.
	AggregateCurrentPeriodChurn uint64 `json:"aggregatecurrentperiodchurn"`
	// MaxPeriodChurn is the (adjustable) maximum churn allowed per period.
	MaxPeriodChurn uint64 `json:"maxperiodchurn"`
}

// UploadedBackup contains metadata about an uploaded backup.
type UploadedBackup struct {
	Name           string
	UID            [16]byte
	CreationDate   types.Timestamp
	Size           uint64 // size of snapshot .sia file
	UploadProgress float64
}

type (
	// WorkerPoolStatus contains information about the status of the workerPool
	// and the workers
	WorkerPoolStatus struct {
		NumWorkers               int            `json:"numworkers"`
		TotalDownloadCoolDown    int            `json:"totaldownloadcooldown"`
		TotalMaintenanceCoolDown int            `json:"totalmaintenancecooldown"`
		TotalUploadCoolDown      int            `json:"totaluploadcooldown"`
		Workers                  []WorkerStatus `json:"workers"`
	}

	// WorkerStatus contains information about the status of a worker
	WorkerStatus struct {
		// Worker contract information
		ContractID      types.FileContractID `json:"contractid"`
		ContractUtility ContractUtility      `json:"contractutility"`
		HostPubKey      types.SiaPublicKey   `json:"hostpubkey"`

		// Download status information
		DownloadCoolDownError string        `json:"downloadcooldownerror"`
		DownloadCoolDownTime  time.Duration `json:"downloadcooldowntime"`
		DownloadOnCoolDown    bool          `json:"downloadoncooldown"`
		DownloadQueueSize     int           `json:"downloadqueuesize"`
		DownloadTerminated    bool          `json:"downloadterminated"`

		// Upload status information
		UploadCoolDownError string        `json:"uploadcooldownerror"`
		UploadCoolDownTime  time.Duration `json:"uploadcooldowntime"`
		UploadOnCoolDown    bool          `json:"uploadoncooldown"`
		UploadQueueSize     int           `json:"uploadqueuesize"`
		UploadTerminated    bool          `json:"uploadterminated"`

		// Maintenance Cooldown information
		MaintenanceOnCooldown    bool          `json:"maintenanceoncooldown"`
		MaintenanceCoolDownError string        `json:"maintenancecooldownerror"`
		MaintenanceCoolDownTime  time.Duration `json:"maintenancecooldowntime"`

		// Ephemeral Account information
		AccountBalanceTarget types.Currency      `json:"accountbalancetarget"`
		AccountStatus        WorkerAccountStatus `json:"accountstatus"`

		// PriceTable information
		PriceTableStatus WorkerPriceTableStatus `json:"pricetablestatus"`

		// Job Queues
		DownloadSnapshotJobQueueSize int `json:"downloadsnapshotjobqueuesize"`
		UploadSnapshotJobQueueSize   int `json:"uploadsnapshotjobqueuesize"`

		// Read Jobs Information
		ReadJobsStatus WorkerReadJobsStatus `json:"readjobsstatus"`

		// HasSector Job Information
		HasSectorJobsStatus WorkerHasSectorJobsStatus `json:"hassectorjobsstatus"`

		// ReadRegistry Job Information
		ReadRegistryJobsStatus WorkerReadRegistryJobStatus `json:"readregistryjobsstatus"`

		// UpdateRegistry Job information
		UpdateRegistryJobsStatus WorkerUpdateRegistryJobStatus `json:"updateregistryjobsstatus"`
	}

	// WorkerGenericJobsStatus contains the common information for worker jobs.
	WorkerGenericJobsStatus struct {
		ConsecutiveFailures uint64    `json:"consecutivefailures"`
		JobQueueSize        uint64    `json:"jobqueuesize"`
		OnCooldown          bool      `json:"oncooldown"`
		OnCooldownUntil     time.Time `json:"oncooldownuntil"`
		RecentErr           string    `json:"recenterr"`
		RecentErrTime       time.Time `json:"recenterrtime"`
	}

	// WorkerAccountStatus contains detailed information about the account
	WorkerAccountStatus struct {
		AvailableBalance types.Currency `json:"availablebalance"`
		NegativeBalance  types.Currency `json:"negativebalance"`

		RecentErr         string    `json:"recenterr"`
		RecentErrTime     time.Time `json:"recenterrtime"`
		RecentSuccessTime time.Time `json:"recentsuccesstime"`
	}

	// WorkerPriceTableStatus contains detailed information about the price
	// table
	WorkerPriceTableStatus struct {
		ExpiryTime time.Time `json:"expirytime"`
		UpdateTime time.Time `json:"updatetime"`

		Active bool `json:"active"`

		RecentErr     string    `json:"recenterr"`
		RecentErrTime time.Time `json:"recenterrtime"`
	}

	// WorkerReadJobsStatus contains detailed information about the read jobs
	WorkerReadJobsStatus struct {
		AvgJobTime64k uint64 `json:"avgjobtime64k"` // in ms
		AvgJobTime1m  uint64 `json:"avgjobtime1m"`  // in ms
		AvgJobTime4m  uint64 `json:"avgjobtime4m"`  // in ms

		ConsecutiveFailures uint64 `json:"consecutivefailures"`

		JobQueueSize uint64 `json:"jobqueuesize"`

		RecentErr     string    `json:"recenterr"`
		RecentErrTime time.Time `json:"recenterrtime"`
	}

	// WorkerHasSectorJobsStatus contains detailed information about the has
	// sector jobs
	WorkerHasSectorJobsStatus struct {
		AvgJobTime uint64 `json:"avgjobtime"` // in ms

		ConsecutiveFailures uint64 `json:"consecutivefailures"`

		JobQueueSize uint64 `json:"jobqueuesize"`

		RecentErr     string    `json:"recenterr"`
		RecentErrTime time.Time `json:"recenterrtime"`
	}

	// WorkerReadRegistryJobStatus contains detailed information about the read
	// registry jobs.
	WorkerReadRegistryJobStatus struct {
		WorkerGenericJobsStatus
	}

	// WorkerUpdateRegistryJobStatus contains detailed information about the update
	// registry jobs.
	WorkerUpdateRegistryJobStatus struct {
		WorkerGenericJobsStatus
	}
)

// A Renter uploads, tracks, repairs, and downloads a set of files for the
// user.
type Renter interface {
	Alerter

	// ActiveHosts provides the list of hosts that the renter is selecting,
	// sorted by preference.
	ActiveHosts() ([]HostDBEntry, error)

	// AllHosts returns the full list of hosts known to the renter.
	AllHosts() ([]HostDBEntry, error)

	// Close closes the Renter.
	Close() error

	// CancelContract cancels a specific contract of the renter.
	CancelContract(id types.FileContractID) error

	// Contracts returns the staticContracts of the renter's hostContractor.
	Contracts() []RenterContract

	// ContractStatus returns the status of the contract with the given ID in the
	// watchdog, and a bool indicating whether or not the watchdog is aware of it.
	ContractStatus(fcID types.FileContractID) (ContractWatchStatus, bool)

	// CreateBackup creates a backup of the renter's siafiles. If a secret is not
	// nil, the backup will be encrypted using the provided secret.
	CreateBackup(dst string, secret []byte) error

	// LoadBackup loads the siafiles of a previously created backup into the
	// renter. If the backup is encrypted, secret will be used to decrypt it.
	// Otherwise the argument is ignored.
	// If a file from the backup would have the same path as an already
	// existing file, a suffix of the form _[num] is appended to the siapath.
	// [num] is incremented until a siapath is found that is not already in
	// use.
	LoadBackup(src string, secret []byte) error

	// InitRecoveryScan starts scanning the whole blockchain for recoverable
	// contracts within a separate thread.
	InitRecoveryScan() error

	// OldContracts returns the oldContracts of the renter's hostContractor.
	OldContracts() []RenterContract

	// ContractorChurnStatus returns contract churn stats for the current period.
	ContractorChurnStatus() ContractorChurnStatus

	// ContractUtility provides the contract utility for a given host key.
	ContractUtility(pk types.SiaPublicKey) (ContractUtility, bool)

	// CurrentPeriod returns the height at which the current allowance period
	// began.
	CurrentPeriod() types.BlockHeight

	// MemoryStatus returns the current status of the memory manager
	MemoryStatus() (MemoryStatus, error)

	// Mount mounts a FUSE filesystem at mountPoint, making the contents of sp
	// available via the local filesystem.
	Mount(mountPoint string, sp SiaPath, opts MountOptions) error

	// MountInfo returns the list of currently mounted FUSE filesystems.
	MountInfo() []MountInfo

	// Unmount unmounts the FUSE filesystem currently mounted at mountPoint.
	Unmount(mountPoint string) error

	// PeriodSpending returns the amount spent on contracts in the current
	// billing period.
	PeriodSpending() (ContractorSpending, error)

	// RecoverableContracts returns the contracts that the contractor deems
	// recoverable. That means they are not expired yet and also not part of the
	// active contracts. Usually this should return an empty slice unless the host
	// isn't available for recovery or something went wrong.
	RecoverableContracts() []RecoverableContract

	// RecoveryScanStatus returns a bool indicating if a scan for recoverable
	// contracts is in progress and if it is, the current progress of the scan.
	RecoveryScanStatus() (bool, types.BlockHeight)

	// RefreshedContract checks if the contract was previously refreshed
	RefreshedContract(fcid types.FileContractID) bool

	// SetFileStuck sets the 'stuck' status of a file.
	SetFileStuck(siaPath SiaPath, stuck bool) error

	// UploadBackup uploads a backup to hosts, such that it can be retrieved
	// using only the seed.
	UploadBackup(src string, name string) error

	// DownloadBackup downloads a backup previously uploaded to hosts.
	DownloadBackup(dst string, name string) error

	// UploadedBackups returns a list of backups previously uploaded to hosts,
	// along with a list of which hosts are storing all known backups.
	UploadedBackups() ([]UploadedBackup, []types.SiaPublicKey, error)

	// BackupsOnHost returns the backups stored on the specified host.
	BackupsOnHost(hostKey types.SiaPublicKey) ([]UploadedBackup, error)

	// DeleteFile deletes a file entry from the renter.
	DeleteFile(siaPath SiaPath) error

	// Download creates a download according to the parameters passed, including
	// downloads of `offset` and `length` type. It returns a method to
	// start the download.
	Download(params RenterDownloadParameters) (DownloadID, func() error, error)

	// DownloadAsync creates a file download using the passed parameters without
	// blocking until the download is finished. The download needs to be started
	// using the method returned by DownloadAsync. DownloadAsync also accepts an
	// optional input function which will be registered to be called when the
	// download is finished.
	DownloadAsync(params RenterDownloadParameters, onComplete func(error) error) (uid DownloadID, start func() error, cancel func(), err error)

	// ClearDownloadHistory clears the download history of the renter
	// inclusive for before and after times.
	ClearDownloadHistory(after, before time.Time) error

	// DownloadByUID returns a download from the download history given its uid.
	DownloadByUID(uid DownloadID) (DownloadInfo, bool)

	// DownloadHistory lists all the files that have been scheduled for download.
	DownloadHistory() []DownloadInfo

	// File returns information on specific file queried by user
	File(siaPath SiaPath) (FileInfo, error)

	// FileList returns information on all of the files stored by the renter at the
	// specified folder. The 'cached' argument specifies whether cached values
	// should be returned or not.
	FileList(siaPath SiaPath, recursive, cached bool, flf FileListFunc) error

	// FileHosts returns a list of hosts that are storing the file data.
	FileHosts(SiaPath) ([]HostDBEntry, error)

	// Filter returns the renter's hostdb's filterMode and filteredHosts
	Filter() (FilterMode, map[string]types.SiaPublicKey, []string, error)

	// SetFilterMode sets the renter's hostdb filter mode
	SetFilterMode(fm FilterMode, hosts []types.SiaPublicKey, netAddresses []string) error

	// Host provides the DB entry and score breakdown for the requested host.
	Host(pk types.SiaPublicKey) (HostDBEntry, bool, error)

	// InitialScanComplete returns a boolean indicating if the initial scan of the
	// hostdb is completed.
	InitialScanComplete() (bool, error)

	// PriceEstimation estimates the cost in siacoins of performing various
	// storage and data operations.
	PriceEstimation(allowance Allowance) (RenterPriceEstimation, Allowance, error)

	// RenameFile changes the path of a file.
	RenameFile(siaPath, newSiaPath SiaPath) error

	// RenameDir changes the path of a dir.
	RenameDir(oldPath, newPath SiaPath) error

	// EstimateHostScore will return the score for a host with the provided
	// settings, assuming perfect age and uptime adjustments
	EstimateHostScore(entry HostDBEntry, allowance Allowance) (HostScoreBreakdown, error)

	// ReadRegistry starts a registry lookup on all available workers. The
	// jobs have 'timeout' amount of time to finish their jobs and return a
	// response. Otherwise the response with the highest revision number will be
	// used.
	ReadRegistry(spk types.SiaPublicKey, tweak crypto.Hash, timeout time.Duration) (SignedRegistryValue, error)

	// ScoreBreakdown will return the score for a host db entry using the
	// hostdb's weighting algorithm.
	ScoreBreakdown(entry HostDBEntry) (HostScoreBreakdown, error)

	// Settings returns the Renter's current settings.
	Settings() (RenterSettings, error)

	// SetSettings sets the Renter's settings.
	SetSettings(RenterSettings) error

	// SetFileTrackingPath sets the on-disk location of an uploaded file to a
	// new value. Useful if files need to be moved on disk.
	SetFileTrackingPath(siaPath SiaPath, newPath string) error

	// UpdateRegistry updates the registries on all workers with the given
	// registry value.
	UpdateRegistry(spk types.SiaPublicKey, srv SignedRegistryValue, timeout time.Duration) error

	// PauseRepairsAndUploads pauses the renter's repairs and uploads for a time
	// duration
	PauseRepairsAndUploads(duration time.Duration) error

	// ResumeRepairsAndUploads resumes the renter's repairs and uploads
	ResumeRepairsAndUploads() error

	// Streamer creates a io.ReadSeeker that can be used to stream downloads
	// from the Sia network and also returns the fileName of the streamed
	// resource.
	Streamer(siapath SiaPath, disableLocalFetch bool) (string, Streamer, error)

	// Upload uploads a file using the input parameters.
	Upload(FileUploadParams) error

	// UploadStreamFromReader reads from the provided reader until io.EOF is
	// reached and upload the data to the Sia network.
	UploadStreamFromReader(up FileUploadParams, reader io.Reader) error

	// CreateDir creates a directory for the renter
	CreateDir(siaPath SiaPath, mode os.FileMode) error

	// DeleteDir deletes a directory from the renter
	DeleteDir(siaPath SiaPath) error

	// DirList lists the directories in a siadir
	DirList(siaPath SiaPath) ([]DirectoryInfo, error)

	// WorkerPoolStatus returns the current status of the Renter's worker pool
	WorkerPoolStatus() (WorkerPoolStatus, error)

	// BubbleMetadata calculates the updated values of a directory's metadata and
	// updates the siadir metadata on disk then calls callThreadedBubbleMetadata
	// on the parent directory so that it is only blocking for the current
	// directory
	//
	// If the recursive boolean is supplied, all sub directories will be bubbled.
	//
	// If the force boolean is supplied, the LastHealthCheckTime of the directories
	// will be ignored so all directories will be considered.
	BubbleMetadata(siaPath SiaPath, force, recursive bool) error
}

// Streamer is the interface implemented by the Renter's streamer type which
// allows for streaming files uploaded to the Sia network.
type Streamer interface {
	io.ReadSeeker
	io.Closer
}

// SkyfileStreamer is the interface implemented by the Renter's skyfile type
// which allows for streaming files uploaded to the Sia network.
type SkyfileStreamer interface {
	io.ReadSeeker
	io.Closer
}

// RenterDownloadParameters defines the parameters passed to the Renter's
// Download method.
type RenterDownloadParameters struct {
	Async            bool
	Httpwriter       io.Writer
	Length           uint64
	Offset           uint64
	SiaPath          SiaPath
	Destination      string
	DisableDiskFetch bool
}

// HealthPercentage returns the health in a more human understandable format out
// of 100%
//
// The percentage is out of 1.25, this is to account for the RepairThreshold of
// 0.25 and assumes that the worst health is 1.5. Since we do not repair until
// the health is worse than the RepairThreshold, a health of 0 - 0.25 is full
// health. Likewise, a health that is greater than 1.25 is essentially 0 health.
func HealthPercentage(health float64) float64 {
	healthPercent := 100 * (1.25 - health)
	if healthPercent > 100 {
		healthPercent = 100
	}
	if healthPercent < 0 {
		healthPercent = 0
	}
	return healthPercent
}

var (
	// RepairThreshold defines the threshold at which the renter decides to
	// repair a file. The renter will start repairing the file when the health
	// is equal to or greater than this value.
	RepairThreshold = build.Select(build.Var{
		Dev:      0.25,
		Standard: 0.25,
		Testnet:  0.25,
		Testing:  0.25,
	}).(float64)
)

// NeedsRepair is a helper to ensure consistent comparison with the
// RepairThreshold
func NeedsRepair(health float64) bool {
	return health >= RepairThreshold
}

// A HostDB is a database of hosts that the renter can use for figuring out who
// to upload to, and download from.
type HostDB interface {
	Alerter

	// ActiveHosts returns the list of hosts that are actively being selected
	// from.
	ActiveHosts() ([]HostDBEntry, error)

	// AllHosts returns the full list of hosts known to the hostdb, sorted in
	// order of preference.
	AllHosts() ([]HostDBEntry, error)

	// CheckForIPViolations accepts a number of host public keys and returns the
	// ones that violate the rules of the addressFilter.
	CheckForIPViolations([]types.SiaPublicKey) ([]types.SiaPublicKey, error)

	// Close closes the hostdb.
	Close() error

	// EstimateHostScore returns the estimated score breakdown of a host with the
	// provided settings.
	EstimateHostScore(HostDBEntry, Allowance) (HostScoreBreakdown, error)

	// Filter returns the hostdb's filterMode and filteredHosts
	Filter() (FilterMode, map[string]types.SiaPublicKey, []string, error)

	// SetFilterMode sets the renter's hostdb filter mode
	SetFilterMode(lm FilterMode, hosts []types.SiaPublicKey, netAddresses []string) error

	// Host returns the HostDBEntry for a given host.
	Host(pk types.SiaPublicKey) (HostDBEntry, bool, error)

	// IncrementSuccessfulInteractions increments the number of successful
	// interactions with a host for a given key
	IncrementSuccessfulInteractions(types.SiaPublicKey) error

	// IncrementFailedInteractions increments the number of failed interactions with
	// a host for a given key
	IncrementFailedInteractions(types.SiaPublicKey) error

	// initialScanComplete returns a boolean indicating if the initial scan of the
	// hostdb is completed.
	InitialScanComplete() (bool, error)

	// IPViolationsCheck returns a boolean indicating if the IP violation check is
	// enabled or not.
	IPViolationsCheck() (bool, error)

	// RandomHosts returns a set of random hosts, weighted by their estimated
	// usefulness / attractiveness to the renter. RandomHosts will not return
	// any offline or inactive hosts.
	RandomHosts(int, []types.SiaPublicKey, []types.SiaPublicKey) ([]HostDBEntry, error)

	// RandomHostsWithAllowance is the same as RandomHosts but accepts an
	// allowance as an argument to be used instead of the allowance set in the
	// renter.
	RandomHostsWithAllowance(int, []types.SiaPublicKey, []types.SiaPublicKey, Allowance) ([]HostDBEntry, error)

	// ScoreBreakdown returns a detailed explanation of the various properties
	// of the host.
	ScoreBreakdown(HostDBEntry) (HostScoreBreakdown, error)

	// SetAllowance updates the allowance used by the hostdb for weighing hosts by
	// updating the host weight function. It will completely rebuild the hosttree so
	// it should be used with care.
	SetAllowance(Allowance) error

	// SetIPViolationCheck enables/disables the IP violation check within the
	// hostdb.
	SetIPViolationCheck(enabled bool) error

	// UpdateContracts rebuilds the knownContracts of the HostBD using the provided
	// contracts.
	UpdateContracts([]RenterContract) error
}
