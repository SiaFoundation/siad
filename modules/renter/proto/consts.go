package proto

import (
	"time"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

const (
	// contractExtension is the extension given to contract files.
	contractExtension = ".contract"

	// rootsDiskLoadBulkSize is the max number of roots we read from disk at
	// once to avoid using up all the ram.
	rootsDiskLoadBulkSize = 1024 * crypto.HashSize // 32 kib

	// remainingFile is a constant used to indicate that a fileSection can access
	// the whole remaining file instead of being bound to a certain end offset.
	remainingFile = -1

	// keyExchangeMaxLen is the maximum number of bytes the renter will read
	// from the host during the RPC key exchange.
	keyExchangeMaxLen = 256
)

var (
	// The following specifiers are used for deriving different seeds from the
	// wallet seed.
	identifierSeedSpecifier = types.Specifier{'i', 'd', 'e', 'n', 't', 'i', 'f', 'i', 'e', 'r', 's', 'e', 'e', 'd'}
	renterSeedSpecifier     = types.Specifier{'r', 'e', 'n', 't', 'e', 'r'}
	secretKeySeedSpecifier  = types.Specifier{'s', 'e', 'c', 'r', 'e', 't', 'k', 'e', 'y', 's', 'e', 'e', 'd'}
	signingKeySeedSpecifier = types.Specifier{'s', 'i', 'g', 'n', 'i', 'n', 'g', 'k', 'e', 'y', 's', 'e', 'e', 'd'}
)

var (
	// connTimeout determines the number of seconds before a dial-up or
	// revision negotiation times out.
	connTimeout = build.Select(build.Var{
		Dev:      10 * time.Second,
		Standard: 2 * time.Minute,
		Testing:  5 * time.Second,
	}).(time.Duration)

	// defaultContractLockTimeout is the default amount of the time, in
	// milliseconds, that the renter will try to acquire a contract lock for.
	defaultContractLockTimeout = build.Select(build.Var{
		Dev:      uint64(60 * 1000),     // 1 minute
		Standard: uint64(5 * 60 * 1000), // 5 minutes
		Testing:  uint64(25 * 1000),     // 25 seconds
	}).(uint64)

	// ephemeralSeedInterval is the amount of blocks after which we use a new
	// renter seed for creating file contracts.
	ephemeralSeedInterval = build.Select(build.Var{
		Dev:      types.BlockHeight(100),
		Standard: types.BlockHeight(1000),
		Testing:  types.BlockHeight(10),
	}).(types.BlockHeight)

	// hostPriceLeeway is the amount of flexibility we give to hosts when
	// choosing how much to pay for file uploads. If the host does not have the
	// most recent block yet, the host will be expecting a slightly larger
	// payment.
	//
	// TODO: Due to the network connectivity issues that v1.3.0 introduced, we
	// had to increase the amount moderately because hosts would not always be
	// properly connected to the peer network, and so could fall behind on
	// blocks. Once enough of the network has upgraded, we can move the number
	// to '0.003' for 'Standard'.
	hostPriceLeeway = build.Select(build.Var{
		Dev:      0.05,
		Standard: 0.01,
		Testing:  0.002,
	}).(float64)

	// sectorHeight is the height of a Merkle tree that covers a single
	// sector. It is log2(modules.SectorSize / crypto.SegmentSize)
	sectorHeight = func() uint64 {
		height := uint64(0)
		for 1<<height < (modules.SectorSize / crypto.SegmentSize) {
			height++
		}
		return height
	}()
)

var (
	// ErrBadHostVersion indicates that the host is using an older, incompatible
	// version of the renter-host protocol.
	ErrBadHostVersion = errors.New("Bad host version; host does not support required protocols")
)
