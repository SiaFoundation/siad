package host

import (
	"time"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

var (
	// ErrBadContractOutputCounts is returned if the presented file contract
	// revision has the wrong number of outputs for either the valid or the
	// missed proof outputs.
	ErrBadContractOutputCounts = ErrorCommunication("rejected for having an unexpected number of outputs")

	// ErrBadContractParent is returned when a file contract revision is
	// presented which has a parent id that doesn't match the file contract
	// which is supposed to be getting revised.
	ErrBadContractParent = ErrorCommunication("could not find contract's parent")

	// ErrBadFileMerkleRoot is returned if the renter incorrectly updates the
	// file merkle root during a file contract revision.
	ErrBadFileMerkleRoot = ErrorCommunication("rejected for bad file merkle root")

	// ErrBadFileSize is returned if the renter incorrectly download and
	// changes the file size during a file contract revision.
	ErrBadFileSize = ErrorCommunication("rejected for bad file size")

	// ErrBadModificationIndex is returned if the renter requests a change on a
	// sector root that is not in the file contract.
	ErrBadModificationIndex = ErrorCommunication("renter has made a modification that points to a nonexistent sector")

	// ErrBadParentID is returned if the renter incorrectly download and
	// provides the wrong parent id during a file contract revision.
	ErrBadParentID = ErrorCommunication("rejected for bad parent id")

	// ErrBadPayoutUnlockHashes is returned if the renter incorrectly sets the
	// payout unlock hashes during contract formation.
	ErrBadPayoutUnlockHashes = ErrorCommunication("rejected for bad unlock hashes in the payout")

	// ErrBadRevisionNumber number is returned if the renter incorrectly
	// download and does not increase the revision number during a file
	// contract revision.
	ErrBadRevisionNumber = ErrorCommunication("rejected for bad revision number")

	// ErrBadSectorSize is returned if the renter provides a sector to be
	// inserted that is the wrong size.
	ErrBadSectorSize = ErrorCommunication("renter has provided an incorrectly sized sector")

	// ErrBadUnlockConditions is returned if the renter incorrectly download
	// and does not provide the right unlock conditions in the payment
	// revision.
	ErrBadUnlockConditions = ErrorCommunication("rejected for bad unlock conditions")

	// ErrBadUnlockHash is returned if the renter incorrectly updates the
	// unlock hash during a file contract revision.
	ErrBadUnlockHash = ErrorCommunication("rejected for bad new unlock hash")

	// ErrBadWindowEnd is returned if the renter incorrectly download and
	// changes the window end during a file contract revision.
	ErrBadWindowEnd = ErrorCommunication("rejected for bad new window end")

	// ErrBadWindowStart is returned if the renter incorrectly updates the
	// window start during a file contract revision.
	ErrBadWindowStart = ErrorCommunication("rejected for bad new window start")

	// ErrEarlyWindow is returned if the file contract provided by the renter
	// has a storage proof window that is starting too near in the future.
	ErrEarlyWindow = ErrorCommunication("rejected for a window that starts too soon")

	// ErrEmptyObject is returned if the renter sends an empty or nil object
	// unexpectedly.
	ErrEmptyObject = ErrorCommunication("renter has unexpectedly send an empty/nil object")

	// ErrHighRenterMissedOutput is returned if the renter incorrectly download
	// and deducts an insufficient amount from the renter missed outputs during
	// a file contract revision.
	ErrHighRenterMissedOutput = ErrorCommunication("rejected for high paying renter missed output")

	// ErrHighRenterValidOutput is returned if the renter incorrectly download
	// and deducts an insufficient amount from the renter valid outputs during
	// a file contract revision.
	ErrHighRenterValidOutput = ErrorCommunication("rejected for high paying renter valid output")

	// ErrIllegalOffsetAndLength is returned if the renter tries perform a
	// modify operation that uses a troublesome combination of offset and
	// length.
	ErrIllegalOffsetAndLength = ErrorCommunication("renter is trying to do a modify with an illegal offset and length")

	// ErrInvalidPayoutSums is returned if a revision doesn't sum up to the same
	// total payout as the previous revision or contract.
	ErrInvalidPayoutSums = ErrorCommunication("renter provided a revision with an invalid total payout")

	// ErrLargeSector is returned if the renter sends a RevisionAction that has
	// data which creates a sector that is larger than what the host uses.
	ErrLargeSector = ErrorCommunication("renter has sent a sector that exceeds the host's sector size")

	// ErrLateRevision is returned if the renter is attempting to revise a
	// revision after the revision deadline. The host needs time to submit the
	// final revision to the blockchain to guarantee payment, and therefore
	// will not accept revisions once the window start is too close.
	ErrLateRevision = ErrorCommunication("renter is requesting revision after the revision deadline")

	// ErrLongDuration is returned if the renter proposes a file contract with
	// an expiration that is too far into the future according to the host's
	// settings.
	ErrLongDuration = ErrorCommunication("renter proposed a file contract with a too-long duration")

	// ErrLowHostMissedOutput is returned if the renter incorrectly updates the
	// host missed proof output during a file contract revision.
	ErrLowHostMissedOutput = ErrorCommunication("rejected for low paying host missed output")

	// ErrLowHostValidOutput is returned if the renter incorrectly updates the
	// host valid proof output during a file contract revision.
	ErrLowHostValidOutput = ErrorCommunication("rejected for low paying host valid output")

	// ErrLowTransactionFees is returned if the renter provides a transaction
	// that the host does not feel is able to make it onto the blockchain.
	ErrLowTransactionFees = ErrorCommunication("rejected for including too few transaction fees")

	// ErrLowVoidOutput is returned if the renter has not allocated enough
	// funds to the void output.
	ErrLowVoidOutput = ErrorCommunication("rejected for low value void output")

	// ErrMismatchedHostPayouts is returned if the renter incorrectly sets the
	// host valid and missed payouts to different values during contract
	// formation.
	ErrMismatchedHostPayouts = ErrorCommunication("rejected because host valid and missed payouts are not the same value")

	// ErrNotAcceptingContracts is returned if the host is currently not
	// accepting new contracts.
	ErrNotAcceptingContracts = ErrorCommunication("host is not accepting new contracts")

	// ErrSmallWindow is returned if the renter suggests a storage proof window
	// that is too small.
	ErrSmallWindow = ErrorCommunication("rejected for small window size")

	// ErrUnknownModification is returned if the host receives a modification
	// action from the renter that it does not understand.
	ErrUnknownModification = ErrorCommunication("renter is attempting an action that the host does not understand")

	// ErrValidHostOutputAddressChanged is returned when the host's valid output
	// address changed even though it shouldn't.
	ErrValidHostOutputAddressChanged = ErrorCommunication("valid host output address changed")

	// ErrMissedHostOutputAddressChanged is returned when the host's missed
	// payout address changed even though it shouldn't.
	ErrMissedHostOutputAddressChanged = ErrorCommunication("missed host output address changed")

	// ErrVoidAddressChanged is returned if the void output address changed.
	ErrVoidAddressChanged = ErrorCommunication("lost collateral address was changed")

	// ErrValidRenterPayoutChanged is returned if the renter's valid payout
	// changed even though it shouldn't.
	ErrValidRenterPayoutChanged = ErrorCommunication("valid renter payout changed")

	// ErrMissedRenterPayoutChanged is returned if the renter's missed payout
	// changed even though it shouldn't.
	ErrMissedRenterPayoutChanged = ErrorCommunication("missed renter payout changed")

	// ErrValidHostPayoutChanged is returned if the host's valid payout changed
	// even though it shouldn't.
	ErrValidHostPayoutChanged = ErrorCommunication("valid host payout changed")

	// ErrVoidPayoutChanged is returned if the void payout changed even though
	// it wasn't expected to.
	ErrVoidPayoutChanged = ErrorCommunication("void payout shouldn't change")
)

// finalizeContractArgs are the arguments passed into managedFinalizeContract.
type finalizeContractArgs struct {
	builder                 modules.TransactionBuilder
	renewedSO               *storageObligation
	renterPK                crypto.PublicKey
	renterSignatures        []types.TransactionSignature
	renterRevisionSignature types.TransactionSignature
	initialSectorRoots      []crypto.Hash
	hostCollateral          types.Currency
	hostInitialRevenue      types.Currency
	hostInitialRisk         types.Currency
	contractPrice           types.Currency
}

// createRevisionSignature creates a signature for a file contract revision
// that signs on the file contract revision. The renter should have already
// provided the signature. createRevisionSignature will check to make sure that
// the renter's signature is valid.
func createRevisionSignature(fcr types.FileContractRevision, renterSig types.TransactionSignature, secretKey crypto.SecretKey, blockHeight types.BlockHeight) (types.Transaction, error) {
	hostSig := types.TransactionSignature{
		ParentID:       crypto.Hash(fcr.ParentID),
		PublicKeyIndex: 1,
		CoveredFields: types.CoveredFields{
			FileContractRevisions: []uint64{0},
		},
	}
	txn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{fcr},
		TransactionSignatures: []types.TransactionSignature{renterSig, hostSig},
	}
	sigHash := txn.SigHash(1, blockHeight)
	encodedSig := crypto.SignHash(sigHash, secretKey)
	txn.TransactionSignatures[1].Signature = encodedSig[:]
	err := modules.VerifyFileContractRevisionTransactionSignatures(fcr, txn.TransactionSignatures, blockHeight)
	if err != nil {
		return types.Transaction{}, err
	}
	return txn, nil
}

// managedFinalizeContract will take a file contract, add the host's
// collateral, and then try submitting the file contract to the transaction
// pool. If there is no error, the completed transaction set will be returned
// to the caller.
func (h *Host) managedFinalizeContract(args finalizeContractArgs) ([]types.TransactionSignature, types.TransactionSignature, types.FileContractID, error) {
	// Extract args
	builder, renterPK, renterSignatures := args.builder, args.renterPK, args.renterSignatures
	renterRevisionSignature, initialSectorRoots, hostCollateral := args.renterRevisionSignature, args.initialSectorRoots, args.hostCollateral
	hostInitialRevenue, hostInitialRisk, contractPrice := args.hostInitialRevenue, args.hostInitialRisk, args.contractPrice

	for _, sig := range renterSignatures {
		builder.AddTransactionSignature(sig)
	}
	fullTxnSet, err := builder.Sign(true)
	if err != nil {
		builder.Drop()
		return nil, types.TransactionSignature{}, types.FileContractID{}, err
	}

	// Verify that the signature for the revision from the renter is correct.
	h.mu.RLock()
	blockHeight := h.blockHeight
	hostSPK := h.publicKey
	hostSK := h.secretKey
	h.mu.RUnlock()
	contractTxn := fullTxnSet[len(fullTxnSet)-1]
	fc := contractTxn.FileContracts[0]
	noOpRevision := types.FileContractRevision{
		ParentID: contractTxn.FileContractID(0),
		UnlockConditions: types.UnlockConditions{
			PublicKeys: []types.SiaPublicKey{
				types.Ed25519PublicKey(renterPK),
				hostSPK,
			},
			SignaturesRequired: 2,
		},
		NewRevisionNumber: fc.RevisionNumber + 1,

		NewFileSize:           fc.FileSize,
		NewFileMerkleRoot:     fc.FileMerkleRoot,
		NewWindowStart:        fc.WindowStart,
		NewWindowEnd:          fc.WindowEnd,
		NewValidProofOutputs:  fc.ValidProofOutputs,
		NewMissedProofOutputs: fc.MissedProofOutputs,
		NewUnlockHash:         fc.UnlockHash,
	}
	// createRevisionSignature will also perform validation on the result,
	// returning an error if the renter provided an incorrect signature.
	revisionTransaction, err := createRevisionSignature(noOpRevision, renterRevisionSignature, hostSK, blockHeight)
	if err != nil {
		return nil, types.TransactionSignature{}, types.FileContractID{}, err
	}

	// Create and add the storage obligation for this file contract.
	fullTxn, _ := builder.View()
	so := storageObligation{
		SectorRoots: initialSectorRoots,

		ContractCost:            contractPrice,
		LockedCollateral:        hostCollateral,
		PotentialStorageRevenue: hostInitialRevenue,
		RiskedCollateral:        hostInitialRisk,

		NegotiationHeight: blockHeight,

		OriginTransactionSet:   fullTxnSet,
		RevisionTransactionSet: []types.Transaction{revisionTransaction},

		h: h,
	}

	// Get a lock on the storage obligation.
	lockErr := h.managedTryLockStorageObligation(so.id(), obligationLockTimeout)
	if lockErr != nil {
		build.Critical("failed to get a lock on a brand new storage obligation")
		return nil, types.TransactionSignature{}, types.FileContractID{}, lockErr
	}
	defer func() {
		if err != nil {
			h.managedUnlockStorageObligation(so.id())
		}
	}()

	// addStorageObligation will submit the transaction to the transaction
	// pool, and will only do so if there was not some error in creating the
	// storage obligation. If the transaction pool returns a consensus
	// conflict, wait 30 seconds and try again.
	err = func() error {
		// Try adding the storage obligation. If there's an error, wait a few
		// seconds and try again. Eventually time out. It should be noted that
		// the storage obligation locking is both crappy and incomplete, and
		// that I'm not sure how this timeout plays with the overall host
		// timeouts.
		//
		// The storage obligation locks should occur at the highest level, not
		// just when the actual modification is happening.
		i := 0
		for {
			if args.renewedSO == nil {
				err = h.managedAddStorageObligation(so)
			} else {
				err = h.managedAddRenewedStorageObligation(*args.renewedSO, so)
			}
			if err == nil {
				return nil
			}
			if err != nil && i > 4 {
				h.log.Println(err)
				builder.Drop()
				return err
			}

			i++
			if build.Release == "standard" || build.Release == "testnet" {
				time.Sleep(time.Second * 15)
			}
		}
	}()
	if err != nil {
		return nil, types.TransactionSignature{}, types.FileContractID{}, err
	}

	// Get the host's transaction signatures from the builder.
	var hostTxnSignatures []types.TransactionSignature
	_, _, _, txnSigIndices := builder.ViewAdded()
	for _, sigIndex := range txnSigIndices {
		hostTxnSignatures = append(hostTxnSignatures, fullTxn.TransactionSignatures[sigIndex])
	}
	return hostTxnSignatures, revisionTransaction.TransactionSignatures[1], so.id(), nil
}
