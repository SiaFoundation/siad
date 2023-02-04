package transactionpool

import (
	"time"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/types"
)

// Consts related to the persisting structures of the transactoin pool.
const (
	dbFilename = "transactionpool.db"
	logFile    = "transactionpool.log"
)

// Constants related to the size and ease-of-entry of the transaction pool.
const (
	// logSizeFrequency is how often the transaction pool size will be logged
	// when running in debug mode.
	logSizeFrequency = time.Minute * 5

	// TransactionPoolFeeExponentiation defines the polynomial rate of growth
	// required to keep putting transactions into the transaction pool. If the
	// exponentiation is 2, then doubling the size of the transaction pool
	// requires quadrupling the fees of the transactions being added. A higher
	// number makes it harder for the transaction pool to grow beyond its
	// default size during times of congestion.
	TransactionPoolExponentiation = 3

	// TransactionPoolSizeForFee defines how large the transaction pool needs to
	// be before it starts expecting fees to be on the transaction. This initial
	// limit is to help the network grow and provide some wiggle room for
	// wallets that are not yet able to operate via a fee market.
	TransactionPoolSizeForFee = 500e3

	// TransactionPoolSizeTarget defines the target size of the pool when the
	// transactions are paying 1 SC / kb in fees.
	TransactionPoolSizeTarget = 3e6
)

// Constants related to fee estimation.
const (
	// blockFeeEstimationDepth defines how far backwards in the blockchain the
	// fee estimator looks when using blocks to figure out the appropriate fees
	// to add to transactions.
	blockFeeEstimationDepth = 6

	// maxMultiplier defines the general gap between the maximum recommended fee
	// and the minimum recommended fee.
	maxMultiplier = 3

	// feeEstimationConstantPadding is the constant amount of padding added to
	// the current tpool size when estimating a good fee rate for new
	// transactions.
	feeEstimationConstantPadding = 250e3

	// feeEstimationProportionalPadding is the amount of proportional padding
	// added to the current tpool size when estimating a good fee rate for new
	// transactions.
	feeEstimationProportionalPadding = 1.25
)

// Variables related to the persisting structures of the transaction pool.
var (
	dbMetadata = persist.Metadata{
		Header:  "Sia Transaction Pool DB",
		Version: "0.6.0",
	}
)

// Variables related to the size and ease-of-entry of the transaction pool.
var (
	// minEstimation defines a sane minimum fee per byte for transactions.  This
	// will typically be only suggested as a fee in the absence of congestion.
	minEstimation = types.SiacoinPrecision.Div64(100).Div64(1e3)
)

// Variables related to propagating transactions through the network.
var (
	// relayTransactionSetTimeout establishes the timeout for a relay
	// transaction set call.
	relayTransactionSetTimeout = build.Select(build.Var{
		Standard: 3 * time.Minute,
		Testnet:  3 * time.Minute,
		Dev:      20 * time.Second,
		Testing:  3 * time.Second,
	}).(time.Duration)

	// MaxTransactionAge determines the maximum age of a transaction (in block
	// height) allowed before the transaction is pruned from the transaction
	// pool.
	MaxTransactionAge = build.Select(build.Var{
		Standard: types.BlockHeight(24),
		Testnet:  types.BlockHeight(24),
		Dev:      types.BlockHeight(12),
		Testing:  types.BlockHeight(5),
	}).(types.BlockHeight)
)
