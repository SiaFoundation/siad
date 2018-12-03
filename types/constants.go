package types

// constants.go contains the Sia constants. Depending on which build tags are
// used, the constants will be initialized to different values.
//
// CONTRIBUTE: We don't have way to check that the non-test constants are all
// sane, plus we have no coverage for them.

import (
	"math"
	"math/big"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
)

var (
	// ASICHardforkHeight is the height at which the hardfork targeting
	// selected ASICs was activated.
	ASICHardforkHeight BlockHeight

	// ASICHardforkTotalTarget is the initial target after the ASIC hardfork.
	// The actual target at ASICHardforkHeight is replaced with this value in
	// order to prevent intolerably slow block times post-fork.
	ASICHardforkTotalTarget Target

	// ASICHardforkTotalTime is the initial total time after the ASIC
	// hardfork. The actual total time at ASICHardforkHeight is replaced with
	// this value in order to prevent intolerably slow block times post-fork.
	ASICHardforkTotalTime int64

	// ASICHardforkFactor is the factor by which the hashrate of targeted
	// ASICs will be reduced.
	ASICHardforkFactor = uint64(1009)

	// ASICHardforkReplayProtectionPrefix is a byte that prefixes
	// SiacoinInputs and SiafundInputs when calculating SigHashes to protect
	// against replay attacks.
	ASICHardforkReplayProtectionPrefix = []byte{0}

	// BlockFrequency is the desired number of seconds that
	// should elapse, on average, between successive Blocks.
	BlockFrequency BlockHeight
	// BlockSizeLimit is the maximum size of a binary-encoded Block
	// that is permitted by the consensus rules.
	BlockSizeLimit = uint64(2e6)
	// BlocksPerHour is the number of blocks expected to be mined per hour.
	BlocksPerHour = uint64(6)
	// BlocksPerDay is the number of blocks expected to be mined per day.
	BlocksPerDay = 24 * BlocksPerHour
	// BlocksPerWeek is the number of blocks expected to be mined per week.
	BlocksPerWeek = 7 * BlocksPerDay
	// BlocksPerMonth is the number of blocks expected to be mined per month.
	BlocksPerMonth = 30 * BlocksPerDay
	// BlocksPerYear is the number of blocks expected to be mined per year.
	BlocksPerYear = 365 * BlocksPerDay

	// EndOfTime is value to be used when a date in the future is needed for
	// validation
	EndOfTime = time.Unix(0, math.MaxInt64)

	// ExtremeFutureThreshold is a temporal limit beyond which Blocks are
	// discarded by the consensus rules. When incoming Blocks are processed, their
	// Timestamp is allowed to exceed the processor's current time by a small amount.
	// But if the Timestamp is further into the future than ExtremeFutureThreshold,
	// the Block is immediately discarded.
	ExtremeFutureThreshold Timestamp
	// FutureThreshold is a temporal limit beyond which Blocks are
	// discarded by the consensus rules. When incoming Blocks are processed, their
	// Timestamp is allowed to exceed the processor's current time by no more than
	// FutureThreshold. If the excess duration is larger than FutureThreshold, but
	// smaller than ExtremeFutureThreshold, the Block may be held in memory until
	// the Block's Timestamp exceeds the current time by less than FutureThreshold.
	FutureThreshold Timestamp
	// GenesisBlock is the first block of the block chain
	GenesisBlock Block

	// GenesisID is used in many places. Calculating it once saves lots of
	// redundant computation.
	GenesisID BlockID

	// GenesisSiafundAllocation is the set of SiafundOutputs created in the Genesis
	// block.
	GenesisSiafundAllocation []SiafundOutput
	// GenesisTimestamp is the timestamp when genesis block was mined
	GenesisTimestamp Timestamp
	// InitialCoinbase is the coinbase reward of the Genesis block.
	InitialCoinbase = uint64(300e3)
	// MaturityDelay specifies the number of blocks that a maturity-required output
	// is required to be on hold before it can be spent on the blockchain.
	// Outputs are maturity-required if they are highly likely to be altered or
	// invalidated in the event of a small reorg. One example is the block reward,
	// as a small reorg may invalidate the block reward. Another example is a siafund
	// payout, as a tiny reorg may change the value of the payout, and thus invalidate
	// any transactions spending the payout. File contract payouts also are subject to
	// a maturity delay.
	MaturityDelay BlockHeight
	// MaxTargetAdjustmentDown restrict how much the block difficulty is allowed to
	// change in a single step, which is important to limit the effect of difficulty
	// raising and lowering attacks.
	MaxTargetAdjustmentDown *big.Rat
	// MaxTargetAdjustmentUp restrict how much the block difficulty is allowed to
	// change in a single step, which is important to limit the effect of difficulty
	// raising and lowering attacks.
	MaxTargetAdjustmentUp *big.Rat
	// MedianTimestampWindow tells us how many blocks to look back when calculating
	// the median timestamp over the previous n blocks. The timestamp of a block is
	// not allowed to be less than or equal to the median timestamp of the previous n
	// blocks, where for Sia this number is typically 11.
	MedianTimestampWindow = uint64(11)
	// MinimumCoinbase is the minimum coinbase reward for a block.
	// The coinbase decreases in each block after the Genesis block,
	// but it will not decrease past MinimumCoinbase.
	MinimumCoinbase uint64

	// Oak hardfork constants. Oak is the name of the difficulty algorithm for
	// Sia following a hardfork at block 135e3.

	// OakDecayDenom is the denominator for how much the total timestamp is decayed
	// each step.
	OakDecayDenom int64
	// OakDecayNum is the numerator for how much the total timestamp is decayed each
	// step.
	OakDecayNum int64
	// OakHardforkBlock is the height at which the hardfork to switch to the oak
	// difficulty adjustment algorithm is triggered.
	OakHardforkBlock BlockHeight
	// OakHardforkFixBlock is the height at which the hardfork to switch from the broken
	// oak difficulty adjustment algorithm to the fixed oak difficulty adjustment
	// algorithm is triggered.
	OakHardforkFixBlock BlockHeight
	// OakHardforkTxnSizeLimit is the maximum size allowed for a transaction, a change
	// which I believe was implemented simultaneously with the oak hardfork.
	OakHardforkTxnSizeLimit = uint64(64e3) // 64 KB
	// OakMaxBlockShift is the maximum number of seconds that the oak algorithm will shift
	// the difficulty.
	OakMaxBlockShift int64
	// OakMaxDrop is the drop is the maximum amount that the difficulty will drop each block.
	OakMaxDrop *big.Rat
	// OakMaxRise is the maximum amount that the difficulty will rise each block.
	OakMaxRise *big.Rat

	// RootDepth is the cumulative target of all blocks. The root depth is essentially
	// the maximum possible target, there have been no blocks yet, so there is no
	// cumulated difficulty yet.
	RootDepth = Target{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255}
	// RootTarget is the target for the genesis block - basically how much work needs
	// to be done in order to mine the first block. The difficulty adjustment algorithm
	// takes over from there.
	RootTarget Target
	// SiacoinPrecision is the number of base units in a siacoin. The Sia network has a very
	// large number of base units. We call 10^24 of these a siacoin.
	//
	// The base unit for Bitcoin is called a satoshi. We call 10^8 satoshis a bitcoin,
	// even though the code itself only ever works with satoshis.
	SiacoinPrecision = NewCurrency(new(big.Int).Exp(big.NewInt(10), big.NewInt(24), nil))
	// SiafundCount is the total number of Siafunds in existence.
	SiafundCount = NewCurrency64(10000)
	// SiafundPortion is the percentage of siacoins that is taxed from FileContracts.
	SiafundPortion = big.NewRat(39, 1000)
	// TargetWindow is the number of blocks to look backwards when determining how much
	// time has passed vs. how many blocks have been created. It's only used in the old,
	// broken difficulty adjustment algorithm.
	TargetWindow BlockHeight
)

var (
	// TaxHardforkHeight is the height at which the tax hardfork occurred.
	TaxHardforkHeight = build.Select(build.Var{
		Dev:      BlockHeight(10),
		Standard: BlockHeight(21e3),
		Testing:  BlockHeight(10),
	}).(BlockHeight)
)

// init checks which build constant is in place and initializes the variables
// accordingly.
func init() {
	if build.Release == "dev" {
		// 'dev' settings are for small developer testnets, usually on the same
		// computer. Settings are slow enough that a small team of developers
		// can coordinate their actions over a the developer testnets, but fast
		// enough that there isn't much time wasted on waiting for things to
		// happen.
		ASICHardforkHeight = 20
		ASICHardforkTotalTarget = Target{0, 0, 0, 8}
		ASICHardforkTotalTime = 800

		BlockFrequency = 12                      // 12 seconds: slow enough for developers to see ~each block, fast enough that blocks don't waste time.
		MaturityDelay = 10                       // 60 seconds before a delayed output matures.
		GenesisTimestamp = Timestamp(1424139000) // Change as necessary.
		RootTarget = Target{0, 0, 2}             // Standard developer CPUs will be able to mine blocks with the race library activated.

		TargetWindow = 20                              // Difficulty is adjusted based on prior 20 blocks.
		MaxTargetAdjustmentUp = big.NewRat(120, 100)   // Difficulty adjusts quickly.
		MaxTargetAdjustmentDown = big.NewRat(100, 120) // Difficulty adjusts quickly.
		FutureThreshold = 2 * 60                       // 2 minutes.
		ExtremeFutureThreshold = 4 * 60                // 4 minutes.

		MinimumCoinbase = 30e3

		OakHardforkBlock = 100
		OakHardforkFixBlock = 105
		OakDecayNum = 985
		OakDecayDenom = 1000
		OakMaxBlockShift = 3
		OakMaxRise = big.NewRat(102, 100)
		OakMaxDrop = big.NewRat(100, 102)

		GenesisSiafundAllocation = []SiafundOutput{
			{
				Value:      NewCurrency64(2000),
				UnlockHash: UnlockHashFromString("d6a6c5a41dc935ec6aef0a9e7f83148a3fdde61062f7204dd244740cf1591bdfc10dca990dd5"),
			},
			{
				Value:      NewCurrency64(7000),
				UnlockHash: UnlockHashFromString("d1f6e43cf84ef26e0908e3f8e1d8a3348e5d2fb067298950d408843af1bd021154874ee3dca0"),
			},
			{
				Value:      NewCurrency64(1000),
				UnlockHash: UnlockConditions{}.UnlockHash(),
			},
		}
	} else if build.Release == "testing" {
		// 'testing' settings are for automatic testing, and create much faster
		// environments than a human can interact with.
		ASICHardforkHeight = 5
		ASICHardforkTotalTarget = Target{255, 255}
		ASICHardforkTotalTime = 10e3

		BlockFrequency = 1 // As fast as possible
		MaturityDelay = 3
		GenesisTimestamp = CurrentTimestamp() - 1e6
		RootTarget = Target{128} // Takes an expected 2 hashes; very fast for testing but still probes 'bad hash' code.

		// A restrictive difficulty clamp prevents the difficulty from climbing
		// during testing, as the resolution on the difficulty adjustment is
		// only 1 second and testing mining should be happening substantially
		// faster than that.
		TargetWindow = 200
		MaxTargetAdjustmentUp = big.NewRat(10001, 10000)
		MaxTargetAdjustmentDown = big.NewRat(9999, 10000)
		FutureThreshold = 3        // 3 seconds
		ExtremeFutureThreshold = 6 // 6 seconds

		MinimumCoinbase = 299990 // Minimum coinbase is hit after 10 blocks to make testing minimum-coinbase code easier.

		// Do not let the difficulty change rapidly - blocks will be getting
		// mined far faster than the difficulty can adjust to.
		OakHardforkBlock = 20
		OakHardforkFixBlock = 23
		OakDecayNum = 9999
		OakDecayDenom = 10e3
		OakMaxBlockShift = 3
		OakMaxRise = big.NewRat(10001, 10e3)
		OakMaxDrop = big.NewRat(10e3, 10001)

		GenesisSiafundAllocation = []SiafundOutput{
			{
				Value:      NewCurrency64(2000),
				UnlockHash: UnlockHashFromString("d6a6c5a41dc935ec6aef0a9e7f83148a3fdde61062f7204dd244740cf1591bdfc10dca990dd5"),
			},
			{
				Value:      NewCurrency64(7000),
				UnlockHash: UnlockHashFromString("d1f6e43cf84ef26e0908e3f8e1d8a3348e5d2fb067298950d408843af1bd021154874ee3dca0"),
			},
			{
				Value:      NewCurrency64(1000),
				UnlockHash: UnlockConditions{}.UnlockHash(),
			},
		}
	} else if build.Release == "standard" {
		// 'standard' settings are for the full network. They are slow enough
		// that the network is secure in a real-world byzantine environment.

		// A total time of 120,000 is chosen because that represents the total
		// time elapsed at a perfect equilibrium, indicating a visible average
		// block time that perfectly aligns with what is expected. A total
		// target of 67 leading zeroes is chosen because that aligns with the
		// amount of hashrate that we expect to be on the network after the
		// hardfork.
		ASICHardforkHeight = 179000
		ASICHardforkTotalTarget = Target{0, 0, 0, 0, 0, 0, 0, 0, 32}
		ASICHardforkTotalTime = 120e3

		// A block time of 1 block per 10 minutes is chosen to follow Bitcoin's
		// example. The security lost by lowering the block time is not
		// insignificant, and the convenience gained by lowering the blocktime
		// even down to 90 seconds is not significant. I do feel that 10
		// minutes could even be too short, but it has worked well for Bitcoin.
		BlockFrequency = 600

		// Payouts take 1 day to mature. This is to prevent a class of double
		// spending attacks parties unintentionally spend coins that will stop
		// existing after a blockchain reorganization. There are multiple
		// classes of payouts in Sia that depend on a previous block - if that
		// block changes, then the output changes and the previously existing
		// output ceases to exist. This delay stops both unintentional double
		// spending and stops a small set of long-range mining attacks.
		MaturityDelay = 144

		// The genesis timestamp is set to June 6th, because that is when the
		// 100-block developer premine started. The trailing zeroes are a
		// bonus, and make the timestamp easier to memorize.
		GenesisTimestamp = Timestamp(1433600000) // June 6th, 2015 @ 2:13pm UTC.

		// The RootTarget was set such that the developers could reasonable
		// premine 100 blocks in a day. It was known to the developers at launch
		// this this was at least one and perhaps two orders of magnitude too
		// small.
		RootTarget = Target{0, 0, 0, 0, 32}

		// When the difficulty is adjusted, it is adjusted by looking at the
		// timestamp of the 1000th previous block. This minimizes the abilities
		// of miners to attack the network using rogue timestamps.
		TargetWindow = 1e3

		// The difficulty adjustment is clamped to 2.5x every 500 blocks. This
		// corresponds to 6.25x every 2 weeks, which can be compared to
		// Bitcoin's clamp of 4x every 2 weeks. The difficulty clamp is
		// primarily to stop difficulty raising attacks. Sia's safety margin is
		// similar to Bitcoin's despite the looser clamp because Sia's
		// difficulty is adjusted four times as often. This does result in
		// greater difficulty oscillation, a tradeoff that was chosen to be
		// acceptable due to Sia's more vulnerable position as an altcoin.
		MaxTargetAdjustmentUp = big.NewRat(25, 10)
		MaxTargetAdjustmentDown = big.NewRat(10, 25)

		// Blocks will not be accepted if their timestamp is more than 3 hours
		// into the future, but will be accepted as soon as they are no longer
		// 3 hours into the future. Blocks that are greater than 5 hours into
		// the future are rejected outright, as it is assumed that by the time
		// 2 hours have passed, those blocks will no longer be on the longest
		// chain. Blocks cannot be kept forever because this opens a DoS
		// vector.
		FutureThreshold = 3 * 60 * 60        // 3 hours.
		ExtremeFutureThreshold = 5 * 60 * 60 // 5 hours.

		// The minimum coinbase is set to 30,000. Because the coinbase
		// decreases by 1 every time, it means that Sia's coinbase will have an
		// increasingly potent dropoff for about 5 years, until inflation more
		// or less permanently settles around 2%.
		MinimumCoinbase = 30e3

		// The oak difficulty adjustment hardfork is set to trigger at block
		// 135,000, which is just under 6 months after the hardfork was first
		// released as beta software to the network. This hopefully gives
		// everyone plenty of time to upgrade and adopt the hardfork, while also
		// being earlier than the most optimistic shipping dates for the miners
		// that would otherwise be very disruptive to the network.
		//
		// There was a bug in the original Oak hardfork that had to be quickly
		// followed up with another fix. The height of that fix is the
		// OakHardforkFixBlock.
		OakHardforkBlock = 135e3
		OakHardforkFixBlock = 139e3

		// The decay is kept at 995/1000, or a decay of about 0.5% each block.
		// This puts the halflife of a block's relevance at about 1 day. This
		// allows the difficulty to adjust rapidly if the hashrate is adjusting
		// rapidly, while still keeping a relatively strong insulation against
		// random variance.
		OakDecayNum = 995
		OakDecayDenom = 1e3

		// The block shift determines the most that the difficulty adjustment
		// algorithm is allowed to shift the target block time. With a block
		// frequency of 600 seconds, the min target block time is 200 seconds,
		// and the max target block time is 1800 seconds.
		OakMaxBlockShift = 3

		// The max rise and max drop for the difficulty is kept at 0.4% per
		// block, which means that in 1008 blocks the difficulty can move a
		// maximum of about 55x. This is significant, and means that dramatic
		// hashrate changes can be responded to quickly, while still forcing an
		// attacker to do a significant amount of work in order to execute a
		// difficulty raising attack, and minimizing the chance that an attacker
		// can get lucky and fake a ton of work.
		OakMaxRise = big.NewRat(1004, 1e3)
		OakMaxDrop = big.NewRat(1e3, 1004)

		GenesisSiafundAllocation = []SiafundOutput{
			{Value: NewCurrency64(2), UnlockHash: UnlockHashFromString("0439e5bc7f14ccf5d3a7e882d040923e45625166dd077b64466bc771791ac6fcec1c01394436")},
			{Value: NewCurrency64(6), UnlockHash: UnlockHashFromString("049e1d2a69772b058a48bebe65724ff3bdf8d0971ebbe994e1e91c9f13e84bf4cbfe00accf31")},
			{Value: NewCurrency64(7), UnlockHash: UnlockHashFromString("080742fa194af76ca24fdc97cae4f10b828a0df8c1a788c5413feaaecdd847e60a82a3240411")},
			{Value: NewCurrency64(8), UnlockHash: UnlockHashFromString("2c6aef338a66f213ccc5f8b2db7a98fb13143420af20049c4921a3a5deb8d9dad0132f50b83e")},
			{Value: NewCurrency64(3), UnlockHash: UnlockHashFromString("2ca31fe94a673784e69f614e9593416ea4d369ad9e1dca2b55d9554b5325cddf0a9dce86b3ed")},
			{Value: NewCurrency64(1), UnlockHash: UnlockHashFromString("33979254c7073b596face3c83e37a5fdeeba1c912f89c80f46c7bb7df368b3f0b3558938b515")},
			{Value: NewCurrency64(10), UnlockHash: UnlockHashFromString("3576fde5fee51c83e99c6c3ac59811a04afc0b3170f04277286272fb0556e975db9d7c89f72a")},
			{Value: NewCurrency64(50), UnlockHash: UnlockHashFromString("38db03321c03a65f8da3ca233cc7db0a97b0e461b085bd21d3ca53c51fd0fec1f15547ae6827")},
			{Value: NewCurrency64(75), UnlockHash: UnlockHashFromString("44be8c5760e89620a1b1cc41e4df57d9865a1938332d486b810c1dca0607320d17e8d839d6dd")},
			{Value: NewCurrency64(10), UnlockHash: UnlockHashFromString("450ec9c85a49f52d9a5ea113c7f1cb380d3f05dc79f5f734c2b5fc4c82067224b122c5e76e6b")},
			{Value: NewCurrency64(10), UnlockHash: UnlockHashFromString("4880fdcfa930011aedcda966c4e02aba5f973be2cb88fbdfa526586e2fd579e07734971fb805")},
			{Value: NewCurrency64(50), UnlockHash: UnlockHashFromString("4882a4e3da1c3c0f3897d4f24d83e8832a3984ad717642b7264f60b2696c1af78e4a9a422fee")},
			{Value: NewCurrency64(10), UnlockHash: UnlockHashFromString("4ad23ae46f45fd7835c36e1a734cd3cac79fcc0e4e5c0e83fa168dec9a2c278716b8262bc763")},
			{Value: NewCurrency64(15), UnlockHash: UnlockHashFromString("55c69a29c474e272ca5ed6935754f7a4c34f3a7b1a214441744fb5f1f1d0d7b84a9dc9c8570f")},
			{Value: NewCurrency64(121), UnlockHash: UnlockHashFromString("57ef537d980e1316cb882ec0cb57e0be4dec7d128edf92461017fc1364455b6f51b1fa676f01")},
			{Value: NewCurrency64(222), UnlockHash: UnlockHashFromString("5bc9650bbc28236fec851f7c61f68c888ff598ae6ff5bc7c157dbbc0cb5cfd392840fc664354")},
			{Value: NewCurrency64(10), UnlockHash: UnlockHashFromString("6ef0eead4e8ab98ab3e3879936842e3ee2cecc23ae6b9c0f8e025d84a33c3259d13856c1e0dd")},
			{Value: NewCurrency64(3), UnlockHash: UnlockHashFromString("723a932c404548b841b2d55e9d2c586a5c1f91c1d7c8d7e9637424c5a0464f99e3239e72af2b")},
			{Value: NewCurrency64(1), UnlockHash: UnlockHashFromString("7b6ae565dcfc32cb26b78598faa7d29bfc66961dbb03b2350b918f21a673fa28af705f308973")},
			{Value: NewCurrency64(5), UnlockHash: UnlockHashFromString("7c65cfaf3277cf1a3e0ff78d96ae49f7ee1c4dffde68a6f47056e350d72d458fb56774b79ac5")},
			{Value: NewCurrency64(10), UnlockHash: UnlockHashFromString("82b8480fe34fd9cd78fe43450a314cc2de1ef23e58b333751ba68c060716deb9a9e0b6e57bff")},
			{Value: NewCurrency64(25), UnlockHash: UnlockHashFromString("8689c6ac60362d0a64805be1e2868f6c1f46bbe436d446e5953940a6997beeb41ade41874fd4")},
			{Value: NewCurrency64(1), UnlockHash: UnlockHashFromString("8ffd76e56db58de05b907ba0cbdd7768ac0d694dabb97a36e5a80682a082b6970c6f75ba9fe1")},
			{Value: NewCurrency64(8), UnlockHash: UnlockHashFromString("936cf91024f96cb8c4d4f178db3f2db8563560cf8260d2fb8809c1a083c6ddb95ff49f2dcc2b")},
			{Value: NewCurrency64(58), UnlockHash: UnlockHashFromString("9b4f591c4547efc6f602c6fe5c3bc0cde59824ba6e7ae9dd4c8f03ee59e7c0170f50b34bd466")},
			{Value: NewCurrency64(2), UnlockHash: UnlockHashFromString("9c204c69d52e42321b5538096ac15091136554b191047d1c4ffc2b53766ecef779841cccf546")},
			{Value: NewCurrency64(23), UnlockHash: UnlockHashFromString("9da98618fe163abc7757c9ee37a8c283581227a82502c6c25dca7492bd116c2c2e5a86444683")},
			{Value: NewCurrency64(10), UnlockHash: UnlockHashFromString("9e336824f2724310a8e6046ff148050eb666a99c90dc6775df083abb7c66502c56b50ade1bbe")},
			{Value: NewCurrency64(1), UnlockHash: UnlockHashFromString("a0af3b21df1e523c226e1ccbf95d0310da0cface8ae7554345bf44c6a0579a449147262278ed")},
			{Value: NewCurrency64(75), UnlockHash: UnlockHashFromString("a35e33dc0e9053703e0a00ada1ead3b0ba5409bdfa6f21e77257644b48d90b1ae624efa81a35")},
			{Value: NewCurrency64(3), UnlockHash: UnlockHashFromString("aa078a74cd1484c5a6fb4b5d45066df4d477ad72221219156fcbcbfd8a681b2401feb5794149")},
			{Value: NewCurrency64(90), UnlockHash: UnlockHashFromString("ad788068ba56978cbf17e7c14df5f368c4379bf36f0f548b94bbad2f68458d2737e27e3ab0f1")},
			{Value: NewCurrency64(20), UnlockHash: UnlockHashFromString("b3b9e4a68b5e0dc1ffe3ae6378696dddf7049bf3e5251a62de0c5b50df213d386b6c17b6b3d1")},
			{Value: NewCurrency64(5), UnlockHash: UnlockHashFromString("c1316714aa87b65595129fc29878a2d0319edcbc724f01833e1b5639f42e40423fad6b983ec8")},
			{Value: NewCurrency64(1), UnlockHash: UnlockHashFromString("c4472dde00150c79c5e065412839137770cda617025b4be7458fdd44f54b0734caecae6c80eb")},
			{Value: NewCurrency64(44), UnlockHash: UnlockHashFromString("c4d6ecd3e3d8987fa402eb0eeb2e8ee753260783d01db3bd3e5881b4779ed661845aa2af4e21")},
			{Value: NewCurrency64(23), UnlockHash: UnlockHashFromString("ce3a7294833157c55612d81a3e4f98af210484a06ce735c8304c7d5e9c552082ac1f789b0e3c")},
			{Value: NewCurrency64(80), UnlockHash: UnlockHashFromString("c867877ec502cb3ff106f5c3dc661b4ae8f9c956cf22331ab497886c7038844822ada408c0a1")},
			{Value: NewCurrency64(2), UnlockHash: UnlockHashFromString("c8f9f5da3afd4cfa587246ef0e02fa7b0ac0c63dbb9bf798a5aec6188e27b177f3bb2c91f98b")},
			{Value: NewCurrency64(1), UnlockHash: UnlockHashFromString("d101c7b8ba39158921fcdbb8822620623ffcfa4f4692a94eb4a11d5d262dafb015701c1f3ad2")},
			{Value: NewCurrency64(2), UnlockHash: UnlockHashFromString("d46be92bb98a4ffd0cedd611dbc6975c518111788b3a42777edc8488036c393a84e5e9d47013")},
			{Value: NewCurrency64(3), UnlockHash: UnlockHashFromString("d6f492adad5021b91d854da7b90126176fb3689669a2781af53f727734012cdeb00112f1695a")},
			{Value: NewCurrency64(1), UnlockHash: UnlockHashFromString("d9daac103586a0e22c8a5d35b53e04d1be1b005d6911a93d62918370793761b8ef4e7df47eb8")},
			{Value: NewCurrency64(1), UnlockHash: UnlockHashFromString("dfa2ac3736c1258ec8d5e630ba91b8ce0fe1a713254626308757cd51bbedb5b4e0474feb510f")},
			{Value: NewCurrency64(1), UnlockHash: UnlockHashFromString("f12e8b29283f2fa983ad7cf6e4d5662c64d93eed859af845e40224ce2ffaf9aacfea794fb954")},
			{Value: NewCurrency64(50), UnlockHash: UnlockHashFromString("f132e5d3422073f17557b4ef4cf60e8169b5996969cbe5ed1782c1aa64c9264785a9b56481f6")},
			{Value: NewCurrency64(8841), UnlockHash: UnlockHashFromString("7d0c44f7664e2d34e53efde0661a6f628ec9264785ae8e3cd7c973e8d190c3c97b5e3ecbc567")},
		}
	}

	// Create the genesis block.
	GenesisBlock = Block{
		Timestamp: GenesisTimestamp,
		Transactions: []Transaction{
			{SiafundOutputs: GenesisSiafundAllocation},
		},
	}
	// Calculate the genesis ID.
	GenesisID = GenesisBlock.ID()
}
