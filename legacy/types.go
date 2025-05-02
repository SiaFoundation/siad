package legacy

import (
	"go.sia.tech/siad/crypto"

	cTypes "go.sia.tech/core/types"
	"go.sia.tech/siad/types"
)

// Timestamp is a Unix timestamp in seconds.
type Timestamp types.Timestamp

type (
	// CoveredFields is a list of fields in a transaction covered by a
	// signature.
	CoveredFields struct {
		WholeTransaction      bool     `json:"wholetransaction"`
		SiacoinInputs         []uint64 `json:"siacoininputs"`
		SiacoinOutputs        []uint64 `json:"siacoinoutputs"`
		FileContracts         []uint64 `json:"filecontracts"`
		FileContractRevisions []uint64 `json:"filecontractrevisions"`
		StorageProofs         []uint64 `json:"storageproofs"`
		SiafundInputs         []uint64 `json:"siafundinputs"`
		SiafundOutputs        []uint64 `json:"siafundoutputs"`
		MinerFees             []uint64 `json:"minerfees"`
		ArbitraryData         []uint64 `json:"arbitrarydata"`
		TransactionSignatures []uint64 `json:"transactionsignatures"`
	}

	// SiacoinInput is an input to a transaction that spends siacoins.
	SiacoinInput struct {
		ParentID         cTypes.SiacoinOutputID `json:"parentid"`
		UnlockConditions UnlockConditions       `json:"unlockconditions"`
	}

	// SiacoinOutput is an output of a transaction that spends siacoins.
	SiacoinOutput struct {
		Value      cTypes.Currency `json:"value"`
		UnlockHash cTypes.Address  `json:"unlockhash"`
	}

	// SiafundInput is an input to a transaction that spends siafunds.
	SiafundInput struct {
		ParentID         cTypes.SiafundOutputID `json:"parentid"`
		UnlockConditions UnlockConditions       `json:"unlockconditions"`
		ClaimUnlockHash  cTypes.Address         `json:"claimunlockhash"`
	}

	// StorageProof is a proof that a host has stored a sector for the duration of
	// a contract.
	StorageProof struct {
		ParentID cTypes.FileContractID `json:"parentid"`
		Segment  [64]byte              `json:"segment"`
		HashSet  []cTypes.Hash256      `json:"hashset"`
	}

	// TransactionSignature is a signature for a transaction.
	TransactionSignature struct {
		ParentID       crypto.Hash   `json:"parentid"`
		PublicKeyIndex uint64        `json:"publickeyindex"`
		Timelock       uint64        `json:"timelock"`
		CoveredFields  CoveredFields `json:"coveredfields"`
		Signature      []byte        `json:"signature"`
	}

	// UnlockConditions is a set of conditions that must be met to spend funds
	// from an address.
	UnlockConditions struct {
		Timelock           uint64             `json:"timelock"`
		PublicKeys         []cTypes.UnlockKey `json:"publickeys"`
		SignaturesRequired uint64             `json:"signaturesrequired"`
	}

	// UnlockKey is a key that can be used to unlock a siacoin output.
	UnlockKey struct {
		Algorithm cTypes.Specifier `json:"algorithm"`
		Key       []byte           `json:"key"`
	}
)

// ConvertSiacoinInputs converts a slice of core siacoin inputs to a slice of
// legacy inputs.
func ConvertSiacoinInputs(scis []cTypes.SiacoinInput) []SiacoinInput {
	scisOut := make([]SiacoinInput, len(scis))
	for i, sci := range scis {
		scisOut[i] = SiacoinInput{
			ParentID: sci.ParentID,
			UnlockConditions: UnlockConditions{
				Timelock:           sci.UnlockConditions.Timelock,
				PublicKeys:         sci.UnlockConditions.PublicKeys,
				SignaturesRequired: sci.UnlockConditions.SignaturesRequired,
			},
		}
	}
	return scisOut
}

// ConvertSiafundInputs converts a slice of core siafund inputs to a slice of
// legacy inputs.
func ConvertSiafundInputs(sfis []cTypes.SiafundInput) []SiafundInput {
	sfisOut := make([]SiafundInput, len(sfis))
	for i, sfi := range sfis {
		sfisOut[i] = SiafundInput{
			ParentID: sfi.ParentID,
			UnlockConditions: UnlockConditions{
				Timelock:           sfi.UnlockConditions.Timelock,
				PublicKeys:         sfi.UnlockConditions.PublicKeys,
				SignaturesRequired: sfi.UnlockConditions.SignaturesRequired,
			},
		}
	}
	return sfisOut
}

// ConvertStorageProofs converts a slice of core storage proofs to a slice of
// legacy proofs.
func ConvertStorageProofs(sps []cTypes.StorageProof) []StorageProof {
	spsOut := make([]StorageProof, len(sps))
	for i, sp := range sps {
		spsOut[i] = StorageProof{
			ParentID: sp.ParentID,
			Segment:  sp.Leaf,
			HashSet:  sp.Proof,
		}
	}
	return spsOut
}

func ConvertSiacoinOutputs(scos []cTypes.SiacoinOutput) []SiacoinOutput {
	scosOut := make([]SiacoinOutput, len(scos))
	for i, sco := range scos {
		scosOut[i] = SiacoinOutput{
			Value:      sco.Value,
			UnlockHash: sco.Address,
		}
	}
	return scosOut
}

// ConvertTransactionSignatures converts a slice of core transaction signatures
// to a slice of legacy signatures.
func ConvertTransactionSignatures(sigs []cTypes.TransactionSignature) []TransactionSignature {
	sigsOut := make([]TransactionSignature, len(sigs))
	for i, sig := range sigs {
		sigsOut[i] = TransactionSignature{
			ParentID:       crypto.Hash(sig.ParentID),
			PublicKeyIndex: sig.PublicKeyIndex,
			Timelock:       sig.Timelock,
			CoveredFields:  convertCoveredFields(sig.CoveredFields),
			Signature:      sig.Signature,
		}
	}
	return sigsOut
}

func convertCoveredFields(cFields cTypes.CoveredFields) CoveredFields {
	return CoveredFields{
		WholeTransaction:      cFields.WholeTransaction,
		SiacoinInputs:         cFields.SiacoinInputs,
		SiacoinOutputs:        cFields.SiacoinOutputs,
		FileContracts:         cFields.FileContracts,
		FileContractRevisions: cFields.FileContractRevisions,
		StorageProofs:         cFields.StorageProofs,
		SiafundInputs:         cFields.SiafundInputs,
		SiafundOutputs:        cFields.SiafundOutputs,
		MinerFees:             cFields.MinerFees,
		ArbitraryData:         cFields.ArbitraryData,
		TransactionSignatures: cFields.Signatures,
	}
}
