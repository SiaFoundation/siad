package contractor

import (
	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/types"

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
		Testing:  types.BlockHeight(100),
	}).(types.BlockHeight)
)

var (
	errAlreadyWatchingContract      = errors.New("Watchdog already watching contract with this ID")
	errEmptyFormationTransactionSet = errors.New("formation transaction set is empty")

	errNonSiacoinInputsInFormationTxn = errors.New("Non-Siacoin inputs in formation transaction")
	errNotAcceptedByTpool             = errors.New("transaction set not accepted by memory pool")

	errTxnNotInSet = errors.New("Transaction not in set; cannot remove from set.")
)
