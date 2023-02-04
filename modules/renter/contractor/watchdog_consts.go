package contractor

import (
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/types"

	"gitlab.com/NebulousLabs/errors"
)

const (
	// If the watchdog sees one of its contractor's file contracts appear in a
	// reverted block, it will begin watching for it again with some flexibility
	// for when it appears in the future.
	reorgLeeway = 24
)

var (
	// waitTime is the number of blocks the watchdog will wait to see a
	// pendingContract onchain before double-spending it.
	waitTime = build.Select(build.Var{
		Dev:      types.BlockHeight(100),
		Standard: types.BlockHeight(288),
		Testnet:  types.BlockHeight(288),
		Testing:  types.BlockHeight(100),
	}).(types.BlockHeight)
)

var (
	errAlreadyWatchingContract = errors.New("Watchdog already watching contract with this ID")
	errTxnNotInSet             = errors.New("Transaction not in set; cannot remove from set.")
)
