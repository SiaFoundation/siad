package legacy

import (
	"go.sia.tech/siad/crypto"

	cTypes "go.sia.tech/core/types"
	"go.sia.tech/siad/types"
)

// Timestamp is a Unix timestamp in seconds.
type Timestamp types.Timestamp

type (
	// FileContractRevision is a revision of a file contract.
	FileContractRevision types.FileContractRevision

	// SiacoinInput is an input to a transaction that spends siacoins.
	SiacoinInput types.SiacoinInput

	// SiacoinOutput is an output of a transaction that spends siacoins.
	SiacoinOutput types.SiacoinOutput

	// SiafundInput is an input to a transaction that spends siafunds.
	SiafundInput types.SiafundInput

	// StorageProof is a proof that a host has stored a sector for the duration of
	// a contract.
	StorageProof types.StorageProof

	// TransactionSignature is a signature for a transaction.
	TransactionSignature types.TransactionSignature
)

// ConvertFileContractRevisions converts a slice of core revisions to a slice of
// legacy revisions.
func ConvertFileContractRevisions(fcrs []cTypes.FileContractRevision) []FileContractRevision {
	fcrsOut := make([]FileContractRevision, len(fcrs))
	for i, fcr := range fcrs {
		fcrsOut[i] = FileContractRevision{
			ParentID: types.FileContractID(fcr.ParentID),
			UnlockConditions: types.UnlockConditions{
				Timelock:           types.BlockHeight(fcr.UnlockConditions.Timelock),
				PublicKeys:         convertPublicKeys(fcr.UnlockConditions.PublicKeys),
				SignaturesRequired: fcr.UnlockConditions.SignaturesRequired,
			},
			NewRevisionNumber: fcr.RevisionNumber,

			NewFileSize:           fcr.Filesize,
			NewFileMerkleRoot:     crypto.Hash(fcr.FileMerkleRoot),
			NewWindowStart:        types.BlockHeight(fcr.WindowStart),
			NewWindowEnd:          types.BlockHeight(fcr.WindowEnd),
			NewValidProofOutputs:  convertSiacoinOutputs(fcr.ValidProofOutputs),
			NewMissedProofOutputs: convertSiacoinOutputs(fcr.MissedProofOutputs),
			NewUnlockHash:         types.UnlockHash(fcr.UnlockHash),
		}
	}
	return fcrsOut
}

// ConvertSiacoinInputs converts a slice of core siacoin inputs to a slice of
// legacy inputs.
func ConvertSiacoinInputs(scis []cTypes.SiacoinInput) []SiacoinInput {
	scisOut := make([]SiacoinInput, len(scis))
	for i, sci := range scis {
		scisOut[i] = SiacoinInput{
			ParentID: types.SiacoinOutputID(sci.ParentID),
			UnlockConditions: types.UnlockConditions{
				Timelock:           types.BlockHeight(sci.UnlockConditions.Timelock),
				PublicKeys:         convertPublicKeys(sci.UnlockConditions.PublicKeys),
				SignaturesRequired: sci.UnlockConditions.SignaturesRequired,
			},
		}
	}
	return scisOut
}

// ConvertSiacoinOutputs converts a slice of core siacoin outputs to a slice of
// legacy outputs.
func ConvertSiacoinOutputs(scos []cTypes.SiacoinOutput) []SiacoinOutput {
	scosOut := make([]SiacoinOutput, len(scos))
	for i, sco := range scos {
		scosOut[i] = SiacoinOutput{
			Value:      types.NewCurrency(sco.Value.Big()),
			UnlockHash: types.UnlockHash(sco.Address),
		}
	}
	return scosOut
}

// ConvertSiafundInputs converts a slice of core siafund inputs to a slice of
// legacy inputs.
func ConvertSiafundInputs(sfis []cTypes.SiafundInput) []SiafundInput {
	sfisOut := make([]SiafundInput, len(sfis))
	for i, sfi := range sfis {
		sfisOut[i] = SiafundInput{
			ParentID: types.SiafundOutputID(sfi.ParentID),
			UnlockConditions: types.UnlockConditions{
				Timelock:           types.BlockHeight(sfi.UnlockConditions.Timelock),
				PublicKeys:         convertPublicKeys(sfi.UnlockConditions.PublicKeys),
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
			ParentID: types.FileContractID(sp.ParentID),
			Segment:  sp.Leaf,
			HashSet:  convertHashes(sp.Proof),
		}
	}
	return spsOut
}

// ConvertTransactionSignatures converts a slice of core transaction signatures
// to a slice of legacy signatures.
func ConvertTransactionSignatures(sigs []cTypes.TransactionSignature) []TransactionSignature {
	sigsOut := make([]TransactionSignature, len(sigs))
	for i, sig := range sigs {
		sigsOut[i] = TransactionSignature{
			ParentID:       crypto.Hash(sig.ParentID),
			PublicKeyIndex: sig.PublicKeyIndex,
			Timelock:       types.BlockHeight(sig.Timelock),
			CoveredFields:  convertCoveredFields(sig.CoveredFields),
			Signature:      sig.Signature,
		}
	}
	return sigsOut
}

func convertCoveredFields(cFields cTypes.CoveredFields) types.CoveredFields {
	return types.CoveredFields{
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

func convertHashes(hashes []cTypes.Hash256) []crypto.Hash {
	hashesOut := make([]crypto.Hash, len(hashes))
	for i, hash := range hashes {
		hashesOut[i] = crypto.Hash(hash)
	}
	return hashesOut
}

func convertPublicKeys(sks []cTypes.UnlockKey) []types.SiaPublicKey {
	sksOut := make([]types.SiaPublicKey, len(sks))
	for i, uk := range sks {
		switch uk.Algorithm {
		case cTypes.SpecifierEd25519:
			sksOut[i] = types.SiaPublicKey{
				Algorithm: types.SignatureEd25519,
				Key:       uk.Key,
			}
		default:
			panic("unknown key type")
		}
	}
	return sksOut
}

func convertSiacoinOutputs(scos []cTypes.SiacoinOutput) []types.SiacoinOutput {
	scosOut := make([]types.SiacoinOutput, len(scos))
	for i, sco := range scos {
		scosOut[i] = types.SiacoinOutput{
			Value:      types.NewCurrency(sco.Value.Big()),
			UnlockHash: types.UnlockHash(sco.Address),
		}
	}
	return scosOut
}
