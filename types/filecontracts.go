package types

// filecontracts.go contains the basic structs and helper functions for file
// contracts.

import (
	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/crypto"
)

var (
	// ProofMissed indicates that a StorageProof was missed, which means that
	// no valid proof was submitted within the proof window.
	ProofMissed ProofStatus = false
	// ProofValid indicates that a valid StorageProof was submitted within the
	// proof window.
	ProofValid ProofStatus = true

	// ErrRevisionCostTooHigh indicates that a new revision can't be created
	// because the cost is higher than the available funds.
	ErrRevisionCostTooHigh = errors.New("Can't create new revision with this cost. Not enough funds remaining to cover it")

	// ErrRevisionCollateralTooLow indicates that a new revision can't be created
	// because the available collateral is too low.
	ErrRevisionCollateralTooLow = errors.New("Can't create new revision with this collateral. Not enough funds remaining to cover it")

	// ErrMissingVoidOutput is the error returned when the void output of a
	// contract or revision is accessed that no longer has one.
	ErrMissingVoidOutput = errors.New("void output is missing")

	// ErrRevisionNotIncremented is returned if the revision number wasn't
	// incremented when creating a new revision.
	ErrRevisionNotIncremented = errors.New("revision number was not incremented")
)

type (
	// A FileContract is a public record of a storage agreement between a "host"
	// and a "renter." It mandates that a host must submit a storage proof to the
	// network, proving that they still possess the file they have agreed to
	// store.
	//
	// The party must submit the storage proof in a block that is between
	// 'WindowStart' and 'WindowEnd'. Upon submitting the proof, the outputs
	// for 'ValidProofOutputs' are created. If the party does not submit a
	// storage proof by 'WindowEnd', then the outputs for 'MissedProofOutputs'
	// are created instead. The sum of 'MissedProofOutputs' and the sum of
	// 'ValidProofOutputs' must equal 'Payout' minus the siafund fee. This fee
	// is sent to the siafund pool, which is a set of siacoins only spendable
	// by siafund owners.
	//
	// Under normal circumstances, the payout will be funded by both the host and
	// the renter, which gives the host incentive not to lose the file. The
	// 'ValidProofUnlockHash' will typically be spendable by host, and the
	// 'MissedProofUnlockHash' will either by spendable by the renter or by
	// nobody (the ZeroUnlockHash).
	//
	// A contract can be terminated early by submitting a FileContractTermination
	// whose UnlockConditions hash to 'TerminationHash'.
	FileContract struct {
		FileSize           uint64          `json:"filesize"`
		FileMerkleRoot     crypto.Hash     `json:"filemerkleroot"`
		WindowStart        BlockHeight     `json:"windowstart"`
		WindowEnd          BlockHeight     `json:"windowend"`
		Payout             Currency        `json:"payout"`
		ValidProofOutputs  []SiacoinOutput `json:"validproofoutputs"`
		MissedProofOutputs []SiacoinOutput `json:"missedproofoutputs"`
		UnlockHash         UnlockHash      `json:"unlockhash"`
		RevisionNumber     uint64          `json:"revisionnumber"`
	}

	// A FileContractRevision revises an existing file contract. The ParentID
	// points to the file contract that is being revised. The UnlockConditions
	// are the conditions under which the revision is valid, and must match the
	// UnlockHash of the parent file contract. The Payout of the file contract
	// cannot be changed, but all other fields are allowed to be changed. The
	// sum of the outputs must match the original payout (taking into account
	// the fee for the proof payouts.) A revision number is included. When
	// getting accepted, the revision number of the revision must be higher
	// than any previously seen revision number for that file contract.
	//
	// FileContractRevisions enable trust-free modifications to existing file
	// contracts.
	FileContractRevision struct {
		ParentID          FileContractID   `json:"parentid"`
		UnlockConditions  UnlockConditions `json:"unlockconditions"`
		NewRevisionNumber uint64           `json:"newrevisionnumber"`

		NewFileSize           uint64          `json:"newfilesize"`
		NewFileMerkleRoot     crypto.Hash     `json:"newfilemerkleroot"`
		NewWindowStart        BlockHeight     `json:"newwindowstart"`
		NewWindowEnd          BlockHeight     `json:"newwindowend"`
		NewValidProofOutputs  []SiacoinOutput `json:"newvalidproofoutputs"`
		NewMissedProofOutputs []SiacoinOutput `json:"newmissedproofoutputs"`
		NewUnlockHash         UnlockHash      `json:"newunlockhash"`
	}

	// A StorageProof fulfills a FileContract. The proof contains a specific
	// segment of the file, along with a set of hashes from the file's Merkle
	// tree. In combination, these can be used to prove that the segment came
	// from the file. To prevent abuse, the segment must be chosen randomly, so
	// the ID of block 'WindowStart' - 1 is used as a seed value; see
	// StorageProofSegment for the exact implementation.
	//
	// A transaction with a StorageProof cannot have any SiacoinOutputs,
	// SiafundOutputs, or FileContracts. This is because a mundane reorg can
	// invalidate the proof, and with it the rest of the transaction.
	StorageProof struct {
		ParentID FileContractID           `json:"parentid"`
		Segment  [crypto.SegmentSize]byte `json:"segment"`
		HashSet  []crypto.Hash            `json:"hashset"`
	}

	// ProofStatus indicates whether a StorageProof was valid (true) or missed (false).
	ProofStatus bool
)

// ID returns the contract's ID.
func (fcr FileContractRevision) ID() FileContractID {
	return fcr.ParentID
}

// HostPublicKey returns the public key of the contract's host. This method
// will panic if called on an incomplete revision.
func (fcr FileContractRevision) HostPublicKey() SiaPublicKey {
	return fcr.UnlockConditions.PublicKeys[1]
}

// PaymentRevision returns a copy of the revision with incremented revision
// number where the given amount has moved from the renter to the host (for
// valid outputs) and from renter to the void (for missed outputs).
func (fcr FileContractRevision) PaymentRevision(amount Currency) (FileContractRevision, error) {
	rev := fcr

	// need to manually copy slice memory
	rev.NewValidProofOutputs = append([]SiacoinOutput{}, fcr.NewValidProofOutputs...)
	rev.NewMissedProofOutputs = append([]SiacoinOutput{}, fcr.NewMissedProofOutputs...)

	// Check that there are enough funds to pay this cost.
	if fcr.ValidRenterPayout().Cmp(amount) < 0 {
		return FileContractRevision{}, errors.AddContext(ErrRevisionCostTooHigh, "valid proof output smaller than cost")
	}
	if fcr.MissedRenterOutput().Value.Cmp(amount) < 0 {
		return FileContractRevision{}, errors.AddContext(ErrRevisionCostTooHigh, "missed proof output smaller than cost")
	}

	// move valid payout from renter to host
	rev.SetValidRenterPayout(fcr.ValidRenterPayout().Sub(amount))
	rev.SetValidHostPayout(fcr.ValidHostPayout().Add(amount))

	// move missed payout from renter to void
	rev.SetMissedRenterPayout(fcr.MissedRenterOutput().Value.Sub(amount))
	void, err := fcr.MissedVoidOutput()
	if err != nil {
		return FileContractRevision{}, err
	}
	rev.SetMissedVoidPayout(void.Value.Add(amount))

	// increment revision number
	rev.NewRevisionNumber++
	return rev, nil
}

// EAFundRevision returns a copy of the revision with incremented revision
// number where the given amount has moved from renter to the host. This is
// similar to PaymentRevision but instead of moving the missed renter payout to
// the void it is moved to the host. That's because the money used to fund an EA
// should always go to the host. A contract might only be used for
// downloading/uploading so the merkle root might never change. Which means a
// storage proof can't be submitted by the host. Once the contract is used for
// uploading using the MDM, a separate revision will be created to move the
// money from the missed host output to the void.
func (fcr FileContractRevision) EAFundRevision(amount Currency) (FileContractRevision, error) {
	rev := fcr

	// need to manually copy slice memory
	rev.NewValidProofOutputs = append([]SiacoinOutput{}, fcr.NewValidProofOutputs...)
	rev.NewMissedProofOutputs = append([]SiacoinOutput{}, fcr.NewMissedProofOutputs...)

	// Check that there are enough funds to pay this cost.
	if fcr.ValidRenterPayout().Cmp(amount) < 0 {
		return FileContractRevision{}, errors.AddContext(ErrRevisionCostTooHigh, "valid proof output smaller than cost")
	}
	if fcr.MissedRenterOutput().Value.Cmp(amount) < 0 {
		return FileContractRevision{}, errors.AddContext(ErrRevisionCostTooHigh, "missed proof output smaller than cost")
	}

	// move valid payout from renter to host
	rev.SetValidRenterPayout(fcr.ValidRenterPayout().Sub(amount))
	rev.SetValidHostPayout(fcr.ValidHostPayout().Add(amount))

	// move missed payout from renter to host
	rev.SetMissedRenterPayout(fcr.MissedRenterOutput().Value.Sub(amount))
	rev.SetMissedHostPayout(rev.MissedHostPayout().Add(amount))

	// increment revision number
	rev.NewRevisionNumber++
	return rev, nil
}

// ExecuteProgramRevision creates a new ExecuteProgramRevision based off of an
// existing revision. Since the MDM program is already paid for using EAs and EA
// funded money is moved to the host's valid and missed output but not the void,
// this revision moves a certain amount of that money from the missed host
// output to the void for collateral and per-block storage cost in case the host
// can't provide a storage proof.
func (fcr FileContractRevision) ExecuteProgramRevision(revisionNumber uint64, transfer Currency, newRoot crypto.Hash, newSize uint64) (FileContractRevision, error) {
	newRevision := fcr

	// need to manually copy slice memory
	newRevision.NewValidProofOutputs = append([]SiacoinOutput{}, fcr.NewValidProofOutputs...)
	newRevision.NewMissedProofOutputs = append([]SiacoinOutput{}, fcr.NewMissedProofOutputs...)

	// Set the new contract root, revision number and size.
	newRevision.NewFileMerkleRoot = newRoot
	newRevision.NewFileSize = newSize
	newRevision.NewRevisionNumber = revisionNumber

	// sanity check revision number.
	if fcr.NewRevisionNumber >= revisionNumber {
		return FileContractRevision{}, ErrRevisionNotIncremented
	}

	// sanity check transfer.
	if newRevision.MissedHostPayout().Cmp(transfer) < 0 {
		return FileContractRevision{}, ErrRevisionCostTooHigh
	}
	// move money from the host.
	newRevision.SetMissedHostPayout(newRevision.MissedHostPayout().Sub(transfer))

	// move money into void.
	voidPayout, err := newRevision.MissedVoidPayout()
	if err != nil {
		return FileContractRevision{}, errors.AddContext(err, "failed to get void payout")
	}
	err = newRevision.SetMissedVoidPayout(voidPayout.Add(transfer))
	if err != nil {
		return FileContractRevision{}, errors.AddContext(err, "failed to set void payout")
	}

	return newRevision, nil
}

// ToTransaction wraps the revision in a Transaction. Note that the
// PublicKeyIndex is hardcoded at 0 as the renter key is always first, see
// formContract
func (fcr FileContractRevision) ToTransaction() Transaction {
	return Transaction{
		FileContractRevisions: []FileContractRevision{fcr},
		TransactionSignatures: []TransactionSignature{{
			ParentID:       crypto.Hash(fcr.ParentID),
			CoveredFields:  CoveredFields{FileContractRevisions: []uint64{0}},
			PublicKeyIndex: 0,
		}},
	}
}

// EndHeight returns the height at which the host is no longer obligated to
// store the contract data.
func (fcr FileContractRevision) EndHeight() BlockHeight {
	return fcr.NewWindowStart
}

// SetValidRenterPayout sets the value of the renter's valid proof output.
func (fc FileContract) SetValidRenterPayout(value Currency) {
	fc.ValidProofOutputs[0].Value = value
}

// SetValidHostPayout sets the value of the host's valid proof output.
func (fc FileContract) SetValidHostPayout(value Currency) {
	fc.ValidProofOutputs[1].Value = value
}

// SetMissedRenterPayout sets the value of the renter's missed proof output.
func (fc FileContract) SetMissedRenterPayout(value Currency) {
	fc.MissedProofOutputs[0].Value = value
}

// SetMissedHostPayout sets the value of the host's missed proof output.
func (fc FileContract) SetMissedHostPayout(value Currency) {
	fc.MissedProofOutputs[1].Value = value
}

// SetMissedVoidPayout sets the value of the void's missed proof output.
func (fc FileContract) SetMissedVoidPayout(value Currency) error {
	if len(fc.MissedProofOutputs) <= 2 {
		return ErrMissingVoidOutput
	}
	fc.MissedProofOutputs[2].Value = value
	return nil
}

// ValidRenterOutput gets the renter's valid proof output.
func (fc FileContract) ValidRenterOutput() SiacoinOutput {
	return fc.ValidProofOutputs[0]
}

// ValidRenterPayout gets the value of the renter's valid proof output.
func (fc FileContract) ValidRenterPayout() Currency {
	return fc.ValidRenterOutput().Value
}

// ValidHostOutput sets gets host's missed proof output.
func (fc FileContract) ValidHostOutput() SiacoinOutput {
	return fc.ValidProofOutputs[1]
}

// ValidHostPayout gets the value of the host's valid proof output.
func (fc FileContract) ValidHostPayout() Currency {
	return fc.ValidHostOutput().Value
}

// MissedRenterOutput gets the renter's missed proof output.
func (fc FileContract) MissedRenterOutput() SiacoinOutput {
	return fc.MissedProofOutputs[0]
}

// MissedRenterPayout gets the value of the renter's missed proof output.
func (fc FileContract) MissedRenterPayout() Currency {
	return fc.MissedRenterOutput().Value
}

// MissedHostOutput gets the host's missed proof output.
func (fc FileContract) MissedHostOutput() SiacoinOutput {
	return fc.MissedProofOutputs[1]
}

// MissedVoidOutput gets the void's missed proof output.
func (fc FileContract) MissedVoidOutput() (SiacoinOutput, error) {
	if len(fc.MissedProofOutputs) <= 2 {
		return SiacoinOutput{}, ErrMissingVoidOutput
	}
	return fc.MissedProofOutputs[2], nil
}

// TotalPayout returns the sum of each the valid and missed payouts plus the
// payout field of the contract.
func (fc FileContract) TotalPayout() (total, valid, missed Currency) {
	for _, output := range fc.ValidProofOutputs {
		valid = valid.Add(output.Value)
	}
	for _, output := range fc.MissedProofOutputs {
		missed = missed.Add(output.Value)
	}
	return fc.Payout, valid, missed
}

// SetValidRenterPayout sets the renter's valid proof output.
func (fcr FileContractRevision) SetValidRenterPayout(value Currency) {
	fcr.NewValidProofOutputs[0].Value = value
}

// SetValidHostPayout sets the host's valid proof output.
func (fcr FileContractRevision) SetValidHostPayout(value Currency) {
	fcr.NewValidProofOutputs[1].Value = value
}

// SetMissedRenterPayout sets the renter's missed proof output.
func (fcr FileContractRevision) SetMissedRenterPayout(value Currency) {
	fcr.NewMissedProofOutputs[0].Value = value
}

// SetMissedHostPayout sets the host's missed proof output.
func (fcr FileContractRevision) SetMissedHostPayout(value Currency) {
	fcr.NewMissedProofOutputs[1].Value = value
}

// SetMissedVoidPayout sets the void's missed proof output.
func (fcr FileContractRevision) SetMissedVoidPayout(value Currency) error {
	if len(fcr.NewMissedProofOutputs) <= 2 {
		return ErrMissingVoidOutput
	}
	fcr.NewMissedProofOutputs[2].Value = value
	return nil
}

// ValidRenterOutput gets the renter's valid proof output.
func (fcr FileContractRevision) ValidRenterOutput() SiacoinOutput {
	return fcr.NewValidProofOutputs[0]
}

// ValidRenterPayout gets the value of the renter's valid proof output.
func (fcr FileContractRevision) ValidRenterPayout() Currency {
	return fcr.ValidRenterOutput().Value
}

// ValidHostOutput sets gets host's missed proof output.
func (fcr FileContractRevision) ValidHostOutput() SiacoinOutput {
	return fcr.NewValidProofOutputs[1]
}

// ValidHostPayout gets the value of the host's valid proof output.
func (fcr FileContractRevision) ValidHostPayout() Currency {
	return fcr.ValidHostOutput().Value
}

// MissedRenterOutput gets the renter's missed proof output.
func (fcr FileContractRevision) MissedRenterOutput() SiacoinOutput {
	return fcr.NewMissedProofOutputs[0]
}

// MissedRenterPayout gets the value of the renter's missed proof output.
func (fcr FileContractRevision) MissedRenterPayout() Currency {
	return fcr.MissedRenterOutput().Value
}

// MissedHostOutput gets the host's missed proof output.
func (fcr FileContractRevision) MissedHostOutput() SiacoinOutput {
	return fcr.NewMissedProofOutputs[1]
}

// MissedHostPayout gets the value of the host's missed proof output.
func (fcr FileContractRevision) MissedHostPayout() Currency {
	return fcr.MissedHostOutput().Value
}

// MissedVoidOutput gets the void's missed proof output.
func (fcr FileContractRevision) MissedVoidOutput() (SiacoinOutput, error) {
	if len(fcr.NewMissedProofOutputs) <= 2 {
		return SiacoinOutput{}, ErrMissingVoidOutput
	}
	return fcr.NewMissedProofOutputs[2], nil
}

// MissedVoidPayout gets the void's missed proof output's value.
func (fcr FileContractRevision) MissedVoidPayout() (Currency, error) {
	sco, err := fcr.MissedVoidOutput()
	if err != nil {
		return Currency{}, err
	}
	return sco.Value, nil
}

// TotalPayout returns the sum of each the valid and missed payouts.
func (fcr FileContractRevision) TotalPayout() (valid, missed Currency) {
	for _, output := range fcr.NewValidProofOutputs {
		valid = valid.Add(output.Value)
	}
	for _, output := range fcr.NewMissedProofOutputs {
		missed = missed.Add(output.Value)
	}
	return
}

// StorageProofOutputID returns the ID of an output created by a file
// contract, given the status of the storage proof. The ID is calculating by
// hashing the concatenation of the StorageProofOutput Specifier, the ID of
// the file contract that the proof is for, a boolean indicating whether the
// proof was valid (true) or missed (false), and the index of the output
// within the file contract.
func (fcid FileContractID) StorageProofOutputID(proofStatus ProofStatus, i uint64) SiacoinOutputID {
	return SiacoinOutputID(crypto.HashAll(
		SpecifierStorageProofOutput,
		fcid,
		proofStatus,
		i,
	))
}

// PostTax returns the amount of currency remaining in a file contract payout
// after tax.
func PostTax(height BlockHeight, payout Currency) Currency {
	return payout.Sub(Tax(height, payout))
}

// Tax returns the amount of Currency that will be taxed from fc.
func Tax(height BlockHeight, payout Currency) Currency {
	// COMPATv0.4.0 - until the first 20,000 blocks have been archived, they
	// will need to be handled in a special way.
	if height < TaxHardforkHeight {
		return payout.MulFloat(0.039).RoundDown(SiafundCount)
	}
	return payout.MulTax().RoundDown(SiafundCount)
}
