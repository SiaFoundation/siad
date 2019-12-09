package types

// signatures.go contains all of the types and functions related to creating
// and verifying transaction signatures. There are a lot of rules surrounding
// the correct use of signatures. Signatures can cover part or all of a
// transaction, can be multiple different algorithms, and must satify a field
// called 'UnlockConditions'.

import (
	"bytes"
	"errors"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
)

var (
	// ErrEntropyKey is the error when a transaction tries to sign an entropy
	// public key
	ErrEntropyKey = errors.New("transaction tries to sign an entropy public key")
	// ErrFrivolousSignature is the error when a transaction contains a frivolous
	// signature
	ErrFrivolousSignature = errors.New("transaction contains a frivolous signature")
	// ErrInvalidPubKeyIndex is the error when a transaction contains a signature
	// that points to a nonexistent public key
	ErrInvalidPubKeyIndex = errors.New("transaction contains a signature that points to a nonexistent public key")
	// ErrInvalidUnlockHashChecksum is the error when the provided unlock hash has
	// an invalid checksum
	ErrInvalidUnlockHashChecksum = errors.New("provided unlock hash has an invalid checksum")
	// ErrMissingSignatures is the error when a transaction has inputs with missing
	// signatures
	ErrMissingSignatures = errors.New("transaction has inputs with missing signatures")
	// ErrPrematureSignature is the error when the timelock on signature has not
	// expired
	ErrPrematureSignature = errors.New("timelock on signature has not expired")
	// ErrPublicKeyOveruse is the error when public key was used multiple times while
	// signing transaction
	ErrPublicKeyOveruse = errors.New("public key was used multiple times while signing transaction")
	// ErrSortedUniqueViolation is the error when a sorted unique violation occurs
	ErrSortedUniqueViolation = errors.New("sorted unique violation")
	// ErrUnlockHashWrongLen is the error when a marshalled unlock hash is the wrong
	// length
	ErrUnlockHashWrongLen = errors.New("marshalled unlock hash is the wrong length")
	// ErrWholeTransactionViolation is the error when there's a covered fields violation
	ErrWholeTransactionViolation = errors.New("covered fields violation")

	// FullCoveredFields is a covered fileds object where the
	// 'WholeTransaction' field has been set to true. The primary purpose of
	// this variable is syntactic sugar.
	FullCoveredFields = CoveredFields{WholeTransaction: true}

	// These Specifiers enumerate the types of signatures that are recognized
	// by this implementation. If a signature's type is unrecognized, the
	// signature is treated as valid. Signatures using the special "entropy"
	// type are always treated as invalid; see Consensus.md for more details.

	// SignatureEd25519 is a specifier for Ed22519
	SignatureEd25519 = NewSpecifier("ed25519")
	// SignatureEntropy is a specifier for entropy
	SignatureEntropy = NewSpecifier("entropy")
)

type (
	// CoveredFields indicates which fields in a transaction have been covered by
	// the signature. (Note that the signature does not sign the fields
	// themselves, but rather their combined hash; see SigHash.) Each slice
	// corresponds to a slice in the Transaction type, indicating which indices of
	// the slice have been signed. The indices must be valid, i.e. within the
	// bounds of the slice. In addition, they must be sorted and unique.
	//
	// As a convenience, a signature of the entire transaction can be indicated by
	// the 'WholeTransaction' field. If 'WholeTransaction' == true, all other
	// fields must be empty (except for the Signatures field, since a signature
	// cannot sign itself).
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

	// A SiaPublicKey is a public key prefixed by a Specifier. The Specifier
	// indicates the algorithm used for signing and verification. Unrecognized
	// algorithms will always verify, which allows new algorithms to be added to
	// the protocol via a soft-fork.
	SiaPublicKey struct {
		Algorithm Specifier `json:"algorithm"`
		Key       []byte    `json:"key"`
	}

	// A TransactionSignature is a signature that is included in the transaction.
	// The signature should correspond to a public key in one of the
	// UnlockConditions of the transaction. This key is specified first by
	// 'ParentID', which specifies the UnlockConditions, and then
	// 'PublicKeyIndex', which indicates the key in the UnlockConditions. There
	// are three types that use UnlockConditions: SiacoinInputs, SiafundInputs,
	// and FileContractTerminations. Each of these types also references a
	// ParentID, and this is the hash that 'ParentID' must match. The 'Timelock'
	// prevents the signature from being used until a certain height.
	// 'CoveredFields' indicates which parts of the transaction are being signed;
	// see CoveredFields.
	TransactionSignature struct {
		ParentID       crypto.Hash   `json:"parentid"`
		PublicKeyIndex uint64        `json:"publickeyindex"`
		Timelock       BlockHeight   `json:"timelock"`
		CoveredFields  CoveredFields `json:"coveredfields"`
		Signature      []byte        `json:"signature"`
	}

	// UnlockConditions are a set of conditions which must be met to execute
	// certain actions, such as spending a SiacoinOutput or terminating a
	// FileContract.
	//
	// The simplest requirement is that the block containing the UnlockConditions
	// must have a height >= 'Timelock'.
	//
	// 'PublicKeys' specifies the set of keys that can be used to satisfy the
	// UnlockConditions; of these, at least 'SignaturesRequired' unique keys must sign
	// the transaction. The keys that do not need to use the same cryptographic
	// algorithm.
	//
	// If 'SignaturesRequired' == 0, the UnlockConditions are effectively "anyone can
	// unlock." If 'SignaturesRequired' > len('PublicKeys'), then the UnlockConditions
	// cannot be fulfilled under any circumstances.
	UnlockConditions struct {
		Timelock           BlockHeight    `json:"timelock"`
		PublicKeys         []SiaPublicKey `json:"publickeys"`
		SignaturesRequired uint64         `json:"signaturesrequired"`
	}

	// Each input has a list of public keys and a required number of signatures.
	// inputSignatures keeps track of which public keys have been used and how many
	// more signatures are needed.
	inputSignatures struct {
		remainingSignatures uint64
		possibleKeys        []SiaPublicKey
		usedKeys            map[uint64]struct{}
		index               int
	}
)

// Ed25519PublicKey returns pk as a SiaPublicKey, denoting its algorithm as
// Ed25519.
func Ed25519PublicKey(pk crypto.PublicKey) SiaPublicKey {
	return SiaPublicKey{
		Algorithm: SignatureEd25519,
		Key:       pk[:],
	}
}

// Equals compares two SiaPublicKey types for equality
func (x SiaPublicKey) Equals(y SiaPublicKey) bool {
	return x.Algorithm == y.Algorithm && bytes.Equal(x.Key, y.Key)
}

// UnlockHash calculates the root hash of a Merkle tree of the
// UnlockConditions object. The leaves of this tree are formed by taking the
// hash of the timelock, the hash of the public keys (one leaf each), and the
// hash of the number of signatures. The keys are put in the middle because
// Timelock and SignaturesRequired are both low entropy fields; they can be
// protected by having random public keys next to them.
func (uc UnlockConditions) UnlockHash() UnlockHash {
	var buf bytes.Buffer
	e := encoding.NewEncoder(&buf)
	tree := crypto.NewTree()
	e.WriteUint64(uint64(uc.Timelock))
	tree.Push(buf.Bytes())
	buf.Reset()
	for _, key := range uc.PublicKeys {
		key.MarshalSia(e)
		tree.Push(buf.Bytes())
		buf.Reset()
	}
	e.WriteUint64(uc.SignaturesRequired)
	tree.Push(buf.Bytes())
	return UnlockHash(tree.Root())
}

// SigHash returns the hash of the fields in a transaction covered by a given
// signature. See CoveredFields for more details.
func (t Transaction) SigHash(i int, height BlockHeight) (hash crypto.Hash) {
	sig := t.TransactionSignatures[i]
	if sig.CoveredFields.WholeTransaction {
		return t.wholeSigHash(sig, height)
	}
	return t.partialSigHash(sig.CoveredFields, height)
}

// wholeSigHash calculates the hash for a signature that specifies
// WholeTransaction = true.
func (t *Transaction) wholeSigHash(sig TransactionSignature, height BlockHeight) (hash crypto.Hash) {
	h := crypto.NewHash()
	e := encoding.NewEncoder(h)

	e.WriteInt(len((t.SiacoinInputs)))
	for i := range t.SiacoinInputs {
		if height >= ASICHardforkHeight {
			e.Write(ASICHardforkReplayProtectionPrefix)
		}
		t.SiacoinInputs[i].MarshalSia(e)
	}
	e.WriteInt(len((t.SiacoinOutputs)))
	for i := range t.SiacoinOutputs {
		t.SiacoinOutputs[i].MarshalSia(e)
	}
	e.WriteInt(len((t.FileContracts)))
	for i := range t.FileContracts {
		t.FileContracts[i].MarshalSia(e)
	}
	e.WriteInt(len((t.FileContractRevisions)))
	for i := range t.FileContractRevisions {
		t.FileContractRevisions[i].MarshalSia(e)
	}
	e.WriteInt(len((t.StorageProofs)))
	for i := range t.StorageProofs {
		t.StorageProofs[i].MarshalSia(e)
	}
	e.WriteInt(len((t.SiafundInputs)))
	for i := range t.SiafundInputs {
		if height >= ASICHardforkHeight {
			e.Write(ASICHardforkReplayProtectionPrefix)
		}
		t.SiafundInputs[i].MarshalSia(e)
	}
	e.WriteInt(len((t.SiafundOutputs)))
	for i := range t.SiafundOutputs {
		t.SiafundOutputs[i].MarshalSia(e)
	}
	e.WriteInt(len((t.MinerFees)))
	for i := range t.MinerFees {
		t.MinerFees[i].MarshalSia(e)
	}
	e.WriteInt(len((t.ArbitraryData)))
	for i := range t.ArbitraryData {
		e.WritePrefixedBytes(t.ArbitraryData[i])
	}

	h.Write(sig.ParentID[:])
	encoding.WriteUint64(h, sig.PublicKeyIndex)
	encoding.WriteUint64(h, uint64(sig.Timelock))

	for _, i := range sig.CoveredFields.TransactionSignatures {
		t.TransactionSignatures[i].MarshalSia(h)
	}

	h.Sum(hash[:0])
	return
}

// partialSigHash calculates the hash of the fields of the transaction
// specified in cf.
func (t *Transaction) partialSigHash(cf CoveredFields, height BlockHeight) (hash crypto.Hash) {
	h := crypto.NewHash()

	for _, input := range cf.SiacoinInputs {
		if height >= ASICHardforkHeight {
			h.Write(ASICHardforkReplayProtectionPrefix)
		}
		t.SiacoinInputs[input].MarshalSia(h)
	}
	for _, output := range cf.SiacoinOutputs {
		t.SiacoinOutputs[output].MarshalSia(h)
	}
	for _, contract := range cf.FileContracts {
		t.FileContracts[contract].MarshalSia(h)
	}
	for _, revision := range cf.FileContractRevisions {
		t.FileContractRevisions[revision].MarshalSia(h)
	}
	for _, storageProof := range cf.StorageProofs {
		t.StorageProofs[storageProof].MarshalSia(h)
	}
	for _, siafundInput := range cf.SiafundInputs {
		if height >= ASICHardforkHeight {
			h.Write(ASICHardforkReplayProtectionPrefix)
		}
		t.SiafundInputs[siafundInput].MarshalSia(h)
	}
	for _, siafundOutput := range cf.SiafundOutputs {
		t.SiafundOutputs[siafundOutput].MarshalSia(h)
	}
	for _, minerFee := range cf.MinerFees {
		t.MinerFees[minerFee].MarshalSia(h)
	}
	for _, arbData := range cf.ArbitraryData {
		encoding.WritePrefixedBytes(h, t.ArbitraryData[arbData])
	}
	for _, sig := range cf.TransactionSignatures {
		t.TransactionSignatures[sig].MarshalSia(h)
	}

	h.Sum(hash[:0])
	return
}

// sortedUnique checks that 'elems' is sorted, contains no repeats, and that no
// element is larger than or equal to 'max'.
func sortedUnique(elems []uint64, max int) bool {
	if len(elems) == 0 {
		return true
	}

	biggest := elems[0]
	for _, elem := range elems[1:] {
		if elem <= biggest {
			return false
		}
		biggest = elem
	}
	if biggest >= uint64(max) {
		return false
	}
	return true
}

// validCoveredFields makes sure that all covered fields objects in the
// signatures follow the rules. This means that if 'WholeTransaction' is set to
// true, all fields except for 'Signatures' must be empty. All fields must be
// sorted numerically, and there can be no repeats.
func (t Transaction) validCoveredFields() error {
	for _, sig := range t.TransactionSignatures {
		// convenience variables
		cf := sig.CoveredFields
		fieldMaxs := []struct {
			field []uint64
			max   int
		}{
			{cf.SiacoinInputs, len(t.SiacoinInputs)},
			{cf.SiacoinOutputs, len(t.SiacoinOutputs)},
			{cf.FileContracts, len(t.FileContracts)},
			{cf.FileContractRevisions, len(t.FileContractRevisions)},
			{cf.StorageProofs, len(t.StorageProofs)},
			{cf.SiafundInputs, len(t.SiafundInputs)},
			{cf.SiafundOutputs, len(t.SiafundOutputs)},
			{cf.MinerFees, len(t.MinerFees)},
			{cf.ArbitraryData, len(t.ArbitraryData)},
			{cf.TransactionSignatures, len(t.TransactionSignatures)},
		}

		if cf.WholeTransaction {
			// If WholeTransaction is set, all fields must be
			// empty, except TransactionSignatures.
			for _, fieldMax := range fieldMaxs[:len(fieldMaxs)-1] {
				if len(fieldMax.field) != 0 {
					return ErrWholeTransactionViolation
				}
			}
		} else {
			// If WholeTransaction is not set, at least one field
			// must be non-empty.
			allEmpty := true
			for _, fieldMax := range fieldMaxs {
				if len(fieldMax.field) != 0 {
					allEmpty = false
					break
				}
			}
			if allEmpty {
				return ErrWholeTransactionViolation
			}
		}

		// Check that all fields are sorted, and without repeat values, and
		// that all elements point to objects that exists within the
		// transaction. If there are repeats, it means a transaction is trying
		// to sign the same object twice. This is unncecessary, and opens up a
		// DoS vector where the transaction asks the verifier to verify many GB
		// of data.
		for _, fieldMax := range fieldMaxs {
			if !sortedUnique(fieldMax.field, fieldMax.max) {
				return ErrSortedUniqueViolation
			}
		}
	}

	return nil
}

// validSignatures checks the validaty of all signatures in a transaction.
func (t *Transaction) validSignatures(currentHeight BlockHeight) error {
	// Check that all covered fields objects follow the rules.
	err := t.validCoveredFields()
	if err != nil {
		return err
	}

	// Create the inputSignatures object for each input.
	sigMap := make(map[crypto.Hash]*inputSignatures)
	for i, input := range t.SiacoinInputs {
		id := crypto.Hash(input.ParentID)
		_, exists := sigMap[id]
		if exists {
			return ErrDoubleSpend
		}

		sigMap[id] = &inputSignatures{
			remainingSignatures: input.UnlockConditions.SignaturesRequired,
			possibleKeys:        input.UnlockConditions.PublicKeys,
			usedKeys:            make(map[uint64]struct{}),
			index:               i,
		}
	}
	for i, revision := range t.FileContractRevisions {
		id := crypto.Hash(revision.ParentID)
		_, exists := sigMap[id]
		if exists {
			return ErrDoubleSpend
		}

		sigMap[id] = &inputSignatures{
			remainingSignatures: revision.UnlockConditions.SignaturesRequired,
			possibleKeys:        revision.UnlockConditions.PublicKeys,
			usedKeys:            make(map[uint64]struct{}),
			index:               i,
		}
	}
	for i, input := range t.SiafundInputs {
		id := crypto.Hash(input.ParentID)
		_, exists := sigMap[id]
		if exists {
			return ErrDoubleSpend
		}

		sigMap[id] = &inputSignatures{
			remainingSignatures: input.UnlockConditions.SignaturesRequired,
			possibleKeys:        input.UnlockConditions.PublicKeys,
			usedKeys:            make(map[uint64]struct{}),
			index:               i,
		}
	}

	// Check all of the signatures for validity.
	for i, sig := range t.TransactionSignatures {
		// Check that sig corresponds to an entry in sigMap.
		inSig, exists := sigMap[crypto.Hash(sig.ParentID)]
		if !exists || inSig.remainingSignatures == 0 {
			return ErrFrivolousSignature
		}
		// Check that sig's key hasn't already been used.
		_, exists = inSig.usedKeys[sig.PublicKeyIndex]
		if exists {
			return ErrPublicKeyOveruse
		}
		// Check that the public key index refers to an existing public key.
		if sig.PublicKeyIndex >= uint64(len(inSig.possibleKeys)) {
			return ErrInvalidPubKeyIndex
		}
		// Check that the timelock has expired.
		if sig.Timelock > currentHeight {
			return ErrPrematureSignature
		}

		// Check that the signature verifies. Multiple signature schemes are
		// supported.
		publicKey := inSig.possibleKeys[sig.PublicKeyIndex]
		switch publicKey.Algorithm {
		case SignatureEntropy:
			// Entropy cannot ever be used to sign a transaction.
			return ErrEntropyKey

		case SignatureEd25519:
			// Decode the public key and signature.
			var edPK crypto.PublicKey
			copy(edPK[:], publicKey.Key)
			var edSig crypto.Signature
			copy(edSig[:], sig.Signature)

			sigHash := t.SigHash(i, currentHeight)
			err = crypto.VerifyHash(sigHash, edPK, edSig)
			if err != nil {
				return err
			}

		default:
			// If the identifier is not recognized, assume that the signature
			// is valid. This allows more signature types to be added via soft
			// forking.
		}

		inSig.usedKeys[sig.PublicKeyIndex] = struct{}{}
		inSig.remainingSignatures--
	}

	// Check that all inputs have been sufficiently signed.
	for _, reqSigs := range sigMap {
		if reqSigs.remainingSignatures != 0 {
			return ErrMissingSignatures
		}
	}

	return nil
}
