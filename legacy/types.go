package legacy

import (
	cTypes "go.sia.tech/core/types"
	"go.sia.tech/siad/types"
)

// Timestamp is a Unix timestamp in seconds.
type Timestamp types.Timestamp

type (
	FileContractRevision types.FileContractRevision
	SiacoinInput         types.SiacoinOutput
	SiacoinOutput        types.SiacoinOutput
	SiafundInput         types.SiafundInput
	StorageProof         types.StorageProof
	TransactionSignature types.TransactionSignature
)

func ConvertFileContractRevisions(fcrs []cTypes.FileContractRevision) []FileContractRevision {
	return nil // TODO: implement
}

func ConvertSiacoinInputs(scis []cTypes.SiacoinInput) []SiacoinInput {
	return nil // TODO: implement
}

func ConvertSiafundInputs(sfis []cTypes.SiafundInput) []SiafundInput {
	return nil // TODO: implement
}

func ConvertStorageProofs(sps []cTypes.StorageProof) []StorageProof {
	return nil // TODO: implement
}

func ConvertTransactionSignatures(tss []cTypes.TransactionSignature) []TransactionSignature {
	return nil // TODO: implement
}
