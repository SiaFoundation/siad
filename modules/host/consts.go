package host

import (
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/types"

	"time"
)

// Constants related to the host's alerts.
const (
	// AlertMSGHostInsufficientCollateral indicates that a host has insufficient
	// collateral budget remaining
	AlertMSGHostInsufficientCollateral = "host has insufficient collateral budget"
)

const (
	// iteratedConnectionTime is the amount of time that is allowed to pass
	// before the host will stop accepting new iterations on an iterated
	// connection.
	iteratedConnectionTime = 1200 * time.Second

	// resubmissionTimeout defines the number of blocks that a host will wait
	// before attempting to resubmit a transaction to the blockchain.
	// Typically, this transaction will contain either a file contract, a file
	// contract revision, or a storage proof.
	resubmissionTimeout = 3

	// rpcRequestInterval is the amount of time that the renter has to send
	// the next RPC ID in the new RPC loop. (More time is alloted for sending
	// the actual RPC request object.)
	rpcRequestInterval = 2 * time.Minute

	// maxObligationLockTimeout is the maximum amount of time the host will wait
	// to lock a storage obligation.
	maxObligationLockTimeout = 10 * time.Minute
)

var (
	// connectablityCheckFirstWait defines how often the host's connectability
	// check is run.
	connectabilityCheckFirstWait = build.Select(build.Var{
		Standard: time.Minute * 2,
		Testnet:  time.Minute * 2,
		Dev:      time.Minute * 1,
		Testing:  time.Second * 3,
	}).(time.Duration)

	// connectablityCheckFrequency defines how often the host's connectability
	// check is run.
	connectabilityCheckFrequency = build.Select(build.Var{
		Standard: time.Minute * 10,
		Testnet:  time.Minute * 10,
		Dev:      time.Minute * 5,
		Testing:  time.Second * 10,
	}).(time.Duration)

	// connectabilityCheckTimeout defines how long a connectability check's dial
	// will be allowed to block before it times out.
	connectabilityCheckTimeout = build.Select(build.Var{
		Standard: time.Minute * 2,
		Testnet:  time.Minute * 2,
		Dev:      time.Minute * 5,
		Testing:  time.Second * 90,
	}).(time.Duration)

	// defaultCollateralBudget defines the maximum number of siacoins that the
	// host is going to allocate towards collateral. The number has been chosen
	// as a number that is large, but not so large that someone would be
	// furious for losing access to it for a few weeks.
	defaultCollateralBudget = types.SiacoinPrecision.Mul64(100e3)

	// defaultMaxEphemeralAccountRisk is the maximum amount of money that the
	// host is willing to risk to a power loss. If a user's withdrawal would put
	// the host over the maxunsaveddelat, the host will wait to complete the
	// user's transaction until the host has persisted the widthdrawal, to
	// prevent the host from having too much money at risk.
	defaultMaxEphemeralAccountRisk = types.SiacoinPrecision.Mul64(5)

	// logAllLimit is the number of errors of each type that the host will log
	// before switching to probabilistic logging. If there are not many errors,
	// it is reasonable that all errors get logged. If there are lots of
	// errors, to cut down on the noise only some of the errors get logged.
	logAllLimit = build.Select(build.Var{
		Dev:      uint64(50),
		Standard: uint64(250),
		Testnet:  uint64(250),
		Testing:  uint64(100),
	}).(uint64)

	// logFewLimit is the number of errors of each type that the host will log
	// before substantially constricting the amount of logging that it is
	// doing.
	logFewLimit = build.Select(build.Var{
		Dev:      uint64(500),
		Standard: uint64(2500),
		Testnet:  uint64(2500),
		Testing:  uint64(500),
	}).(uint64)

	// obligationLockTimeout defines how long a thread will wait to get a lock
	// on a storage obligation before timing out and reporting an error to the
	// renter.
	obligationLockTimeout = build.Select(build.Var{
		Dev:      time.Second * 20,
		Standard: time.Second * 60,
		Testnet:  time.Second * 60,
		Testing:  time.Second * 3,
	}).(time.Duration)

	// revisionSubmissionBuffer describes the number of blocks ahead of time
	// that the host will submit a file contract revision. The host will not
	// accept any more revisions once inside the submission buffer.
	revisionSubmissionBuffer = build.Select(build.Var{
		Dev:      types.BlockHeight(20),  // About 4 minutes
		Standard: types.BlockHeight(144), // 1 day.
		Testnet:  types.BlockHeight(144), // 1 day.
		Testing:  types.BlockHeight(4),
	}).(types.BlockHeight)

	// rpcRatelimit prevents someone from spamming the host with connections,
	// causing it to spin up enough goroutines to crash.
	rpcRatelimit = build.Select(build.Var{
		Dev:      time.Millisecond * 10,
		Standard: time.Millisecond * 50,
		Testnet:  time.Millisecond * 50,
		Testing:  time.Millisecond,
	}).(time.Duration)

	// workingStatusFirstCheck defines how frequently the Host's working status
	// check runs
	workingStatusFirstCheck = build.Select(build.Var{
		Standard: time.Minute * 3,
		Testnet:  time.Minute * 3,
		Dev:      time.Minute * 1,
		Testing:  time.Second * 3,
	}).(time.Duration)

	// workingStatusFrequency defines how frequently the Host's working status
	// check runs
	workingStatusFrequency = build.Select(build.Var{
		Standard: time.Minute * 10,
		Testnet:  time.Minute * 10,
		Dev:      time.Minute * 5,
		Testing:  time.Second * 10,
	}).(time.Duration)

	// workingStatusThreshold defines how many settings calls must occur over the
	// workingStatusFrequency for the host to be considered working.
	workingStatusThreshold = build.Select(build.Var{
		Standard: uint64(3),
		Testnet:  uint64(3),
		Dev:      uint64(1),
		Testing:  uint64(1),
	}).(uint64)
)

// All of the following variables define the names of buckets used by the host
// in the database.
var (
	// bucketActionItems maps a blockchain height to a list of storage
	// obligations that need to be managed in some way at that height. The
	// height is stored as a big endian uint64, which means that bolt will
	// store the heights sorted in numerical order. The action item itself is
	// an array of file contract ids. The host is able to contextually figure
	// out what the necessary actions for that item are based on the file
	// contract id and the associated storage obligation that can be retrieved
	// using the id.
	bucketActionItems = []byte("BucketActionItems")

	// bucketStorageObligations contains a set of serialized
	// 'storageObligations' sorted by their file contract id.
	bucketStorageObligations = []byte("BucketStorageObligations")
)

// init runs a series of sanity checks to verify that the constants have sane
// values.
func init() {
	// The revision submission buffer should be greater than the resubmission
	// timeout, because there should be time to perform resubmission if the
	// first attempt to submit the revision fails.
	if revisionSubmissionBuffer < resubmissionTimeout {
		build.Critical("revision submission buffer needs to be larger than or equal to the resubmission timeout")
	}
}
