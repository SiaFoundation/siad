package host

import (
	"errors"

	"github.com/coreos/bbolt"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// managedRPCLoopSettings writes an RPC response containing the host's
// settings.
func (h *Host) managedRPCLoopSettings(s *rpcSession) error {
	s.extendDeadline(modules.NegotiateSettingsTime)

	h.mu.Lock()
	hes := h.externalSettings()
	h.mu.Unlock()

	resp := modules.LoopSettingsResponse{
		Settings: hes,
	}
	if err := s.writeResponse(resp); err != nil {
		return err
	}
	return nil
}

// managedRPCLoopRecentRevision writes an RPC response containing the most
// recent revision of the requested contract.
func (h *Host) managedRPCLoopRecentRevision(s *rpcSession) error {
	s.extendDeadline(modules.NegotiateRecentRevisionTime)

	// Fetch the revision and signatures.
	txn := s.so.RevisionTransactionSet[len(s.so.RevisionTransactionSet)-1]
	rev := txn.FileContractRevisions[0]
	var sigs []types.TransactionSignature
	for _, sig := range txn.TransactionSignatures {
		// The transaction may have additional signatures that are only
		// relevant to the host.
		if sig.ParentID == crypto.Hash(rev.ParentID) {
			sigs = append(sigs, sig)
		}
	}

	// Write the response.
	resp := modules.LoopRecentRevisionResponse{
		Revision:   rev,
		Signatures: sigs,
	}
	if err := s.writeResponse(resp); err != nil {
		return err
	}
	return nil
}

// managedRPCLoopWrite reads an upload request and responds with a signature
// for the new revision.
func (h *Host) managedRPCLoopWrite(s *rpcSession) error {
	s.extendDeadline(modules.NegotiateFileContractRevisionTime)
	// Read the request.
	var req modules.LoopWriteRequest
	if err := s.readRequest(&req, uploadReqMaxLen); err != nil {
		// Reading may have failed due to a closed connection; regardless, it
		// doesn't hurt to try and tell the renter about it.
		s.writeError(err)
		return err
	}

	// Perform some basic input validation.
	if uint64(len(req.Data)) != modules.SectorSize {
		s.writeError(errBadSectorSize)
		return errBadSectorSize
	}

	// Read some internal fields for later.
	h.mu.RLock()
	blockHeight := h.blockHeight
	secretKey := h.secretKey
	settings := h.externalSettings()
	h.mu.RUnlock()
	currentRevision := s.so.RevisionTransactionSet[len(s.so.RevisionTransactionSet)-1].FileContractRevisions[0]

	// construct the new revision
	newRevision := currentRevision
	newRevision.NewRevisionNumber = req.NewRevisionNumber
	newRevision.NewFileSize += modules.SectorSize
	newRevision.NewValidProofOutputs = make([]types.SiacoinOutput, len(currentRevision.NewValidProofOutputs))
	for i := range newRevision.NewValidProofOutputs {
		newRevision.NewValidProofOutputs[i] = types.SiacoinOutput{
			Value:      req.NewValidProofValues[i],
			UnlockHash: currentRevision.NewValidProofOutputs[i].UnlockHash,
		}
	}
	newRevision.NewMissedProofOutputs = make([]types.SiacoinOutput, len(currentRevision.NewMissedProofOutputs))
	for i := range newRevision.NewMissedProofOutputs {
		newRevision.NewMissedProofOutputs[i] = types.SiacoinOutput{
			Value:      req.NewMissedProofValues[i],
			UnlockHash: currentRevision.NewMissedProofOutputs[i].UnlockHash,
		}
	}

	// verify the revision and calculate the root of the sector
	blocksRemaining := s.so.proofDeadline() - blockHeight
	blockBytesCurrency := types.NewCurrency64(uint64(blocksRemaining)).Mul64(modules.SectorSize)
	bandwidthRevenue := settings.UploadBandwidthPrice.Mul64(modules.SectorSize)
	storageRevenue := settings.StoragePrice.Mul(blockBytesCurrency)
	newCollateral := settings.Collateral.Mul(blockBytesCurrency)
	newRoot := crypto.MerkleRoot(req.Data)
	s.so.SectorRoots = append(s.so.SectorRoots, newRoot)
	newRevision.NewFileMerkleRoot = cachedMerkleRoot(s.so.SectorRoots)
	newRevenue := storageRevenue.Add(bandwidthRevenue)
	if err := verifyRevision(s.so, newRevision, blockHeight, newRevenue, newCollateral); err != nil {
		s.writeError(err)
		return err
	}

	// Sign the new revision.
	renterSig := types.TransactionSignature{
		ParentID:       crypto.Hash(newRevision.ParentID),
		CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
		PublicKeyIndex: 0,
		Signature:      req.Signature,
	}
	txn, err := createRevisionSignature(newRevision, renterSig, secretKey, blockHeight)
	if err != nil {
		s.writeError(err)
		return err
	}

	// Update the storage obligation.
	s.so.PotentialStorageRevenue = s.so.PotentialStorageRevenue.Add(storageRevenue)
	s.so.RiskedCollateral = s.so.RiskedCollateral.Add(newCollateral)
	s.so.PotentialUploadRevenue = s.so.PotentialUploadRevenue.Add(bandwidthRevenue)
	s.so.RevisionTransactionSet = []types.Transaction{txn}
	h.mu.Lock()
	err = h.modifyStorageObligation(s.so, nil, []crypto.Hash{newRoot}, [][]byte{req.Data})
	h.mu.Unlock()
	if err != nil {
		s.writeError(err)
		return err
	}

	// Send the response.
	resp := modules.LoopWriteResponse{
		Signature: txn.TransactionSignatures[1].Signature,
	}
	if err := s.writeResponse(resp); err != nil {
		return err
	}
	return nil
}

// managedRPCLoopRead writes an RPC response containing the requested data
// (along with signatures and an optional Merkle proof).
func (h *Host) managedRPCLoopRead(s *rpcSession) error {
	s.extendDeadline(modules.NegotiateDownloadTime)

	// Read the request.
	var req modules.LoopReadRequest
	if err := s.readRequest(&req, readReqMaxLen); err != nil {
		// Reading may have failed due to a closed connection; regardless, it
		// doesn't hurt to try and tell the renter about it.
		s.writeError(err)
		return err
	}

	// Read some internal fields for later.
	h.mu.RLock()
	blockHeight := h.blockHeight
	secretKey := h.secretKey
	settings := h.externalSettings()
	h.mu.RUnlock()
	currentRevision := s.so.RevisionTransactionSet[len(s.so.RevisionTransactionSet)-1].FileContractRevisions[0]

	// Validate the request.
	if len(req.Sections) != 1 {
		err := errors.New("invalid number of sections")
		s.writeError(err)
		return err
	}
	sec := req.Sections[0]
	var err error
	if uint64(sec.Offset)+uint64(sec.Length) > modules.SectorSize {
		err = errRequestOutOfBounds
	} else if sec.Length == 0 {
		err = errors.New("length cannot be zero")
	} else if req.MerkleProof && (sec.Offset%crypto.SegmentSize != 0 || sec.Length%crypto.SegmentSize != 0) {
		err = errors.New("offset and length must be multiples of SegmentSize when requesting a Merkle proof")
	} else if len(req.NewValidProofValues) != len(currentRevision.NewValidProofOutputs) {
		err = errors.New("wrong number of valid proof values")
	} else if len(req.NewMissedProofValues) != len(currentRevision.NewMissedProofOutputs) {
		err = errors.New("wrong number of missed proof values")
	}
	if err != nil {
		s.writeError(err)
		return err
	}

	// construct the new revision
	newRevision := currentRevision
	newRevision.NewRevisionNumber = req.NewRevisionNumber
	newRevision.NewValidProofOutputs = make([]types.SiacoinOutput, len(currentRevision.NewValidProofOutputs))
	for i := range newRevision.NewValidProofOutputs {
		newRevision.NewValidProofOutputs[i] = types.SiacoinOutput{
			Value:      req.NewValidProofValues[i],
			UnlockHash: currentRevision.NewValidProofOutputs[i].UnlockHash,
		}
	}
	newRevision.NewMissedProofOutputs = make([]types.SiacoinOutput, len(currentRevision.NewMissedProofOutputs))
	for i := range newRevision.NewMissedProofOutputs {
		newRevision.NewMissedProofOutputs[i] = types.SiacoinOutput{
			Value:      req.NewMissedProofValues[i],
			UnlockHash: currentRevision.NewMissedProofOutputs[i].UnlockHash,
		}
	}

	expectedTransfer := settings.DownloadBandwidthPrice.Mul64(uint64(sec.Length))
	err = verifyPaymentRevision(currentRevision, newRevision, blockHeight, expectedTransfer)
	if err != nil {
		s.writeError(err)
		return err
	}

	// Fetch the requested data.
	sectorData, err := h.ReadSector(sec.MerkleRoot)
	if err != nil {
		s.writeError(err)
		return err
	}
	data := sectorData[sec.Offset : sec.Offset+sec.Length]

	// Construct the Merkle proof, if requested.
	var proof []crypto.Hash
	if req.MerkleProof {
		proofStart := int(sec.Offset) / crypto.SegmentSize
		proofEnd := int(sec.Offset+sec.Length) / crypto.SegmentSize
		proof = crypto.MerkleRangeProof(sectorData, proofStart, proofEnd)
	}

	// Sign the new revision.
	renterSig := types.TransactionSignature{
		ParentID:       crypto.Hash(newRevision.ParentID),
		CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
		PublicKeyIndex: 0,
		Signature:      req.Signature,
	}
	txn, err := createRevisionSignature(newRevision, renterSig, secretKey, blockHeight)
	if err != nil {
		s.writeError(err)
		return err
	}

	// Update the storage obligation.
	paymentTransfer := currentRevision.NewValidProofOutputs[0].Value.Sub(newRevision.NewValidProofOutputs[0].Value)
	s.so.PotentialDownloadRevenue = s.so.PotentialDownloadRevenue.Add(paymentTransfer)
	s.so.RevisionTransactionSet = []types.Transaction{txn}
	h.mu.Lock()
	err = h.modifyStorageObligation(s.so, nil, nil, nil)
	h.mu.Unlock()
	if err != nil {
		s.writeError(err)
		return err
	}

	// send the response
	resp := modules.LoopReadResponse{
		Signature:   txn.TransactionSignatures[1].Signature,
		Data:        data,
		MerkleProof: proof,
	}
	if err := s.writeResponse(resp); err != nil {
		return err
	}
	return nil
}

// managedRPCLoopFormContract handles the contract formation RPC.
func (h *Host) managedRPCLoopFormContract(s *rpcSession) error {
	// NOTE: this RPC contains two request/response exchanges.
	s.extendDeadline(modules.NegotiateFileContractTime)

	// Read the contract request.
	var req modules.LoopFormContractRequest
	if err := s.readRequest(&req, formContractReqMaxLen); err != nil {
		s.writeError(err)
		return err
	}

	h.mu.Lock()
	settings := h.externalSettings()
	h.mu.Unlock()
	if !settings.AcceptingContracts {
		s.writeError(errors.New("host is not accepting new contracts"))
		return nil
	}

	// The host verifies that the file contract coming over the wire is
	// acceptable.
	txnSet := req.Transactions
	var renterPK crypto.PublicKey
	copy(renterPK[:], req.RenterKey.Key)
	if err := h.managedVerifyNewContract(txnSet, renterPK, settings); err != nil {
		s.writeError(err)
		return err
	}
	// The host adds collateral to the transaction.
	txnBuilder, newParents, newInputs, newOutputs, err := h.managedAddCollateral(settings, txnSet)
	if err != nil {
		s.writeError(err)
		return err
	}
	// Send any new inputs and outputs that were added to the transaction.
	resp := modules.LoopContractAdditions{
		Parents: newParents,
		Inputs:  newInputs,
		Outputs: newOutputs,
	}
	if err := s.writeResponse(resp); err != nil {
		return err
	}

	// The renter will now send transaction signatures for the file contract
	// transaction and a signature for the implicit no-op file contract
	// revision.
	var renterSigs modules.LoopContractSignatures
	if err := s.readResponse(&renterSigs, contractSigsRespMaxLen); err != nil {
		s.writeError(err)
		return err
	}

	// The host adds the renter transaction signatures, then signs the
	// transaction and submits it to the blockchain, creating a storage
	// obligation in the process.
	h.mu.RLock()
	hostCollateral := contractCollateral(settings, txnSet[len(txnSet)-1].FileContracts[0])
	h.mu.RUnlock()
	hostTxnSignatures, hostRevisionSignature, newSOID, err := h.managedFinalizeContract(txnBuilder, renterPK, renterSigs.ContractSignatures, renterSigs.RevisionSignature, nil, hostCollateral, types.ZeroCurrency, types.ZeroCurrency, settings)
	if err != nil {
		s.writeError(err)
		return err
	}
	defer h.managedUnlockStorageObligation(newSOID)

	// Send our signatures for the contract transaction and initial revision.
	hostSigs := modules.LoopContractSignatures{
		ContractSignatures: hostTxnSignatures,
		RevisionSignature:  hostRevisionSignature,
	}
	if err := s.writeResponse(hostSigs); err != nil {
		return err
	}

	// Set the storageObligation so that subsequent RPCs can use it.
	h.mu.RLock()
	err = h.db.View(func(tx *bolt.Tx) error {
		s.so, err = getStorageObligation(tx, newSOID)
		return err
	})
	h.mu.RUnlock()

	return nil
}

// managedRPCLoopRenewContract handles the LoopRenewContract RPC.
func (h *Host) managedRPCLoopRenewContract(s *rpcSession) error {
	// NOTE: this RPC contains two request/response exchanges.
	s.extendDeadline(modules.NegotiateRenewContractTime)

	// Read the renewal request.
	var req modules.LoopRenewContractRequest
	if err := s.readRequest(&req, renewContractReqMaxLen); err != nil {
		s.writeError(err)
		return err
	}

	h.mu.Lock()
	settings := h.externalSettings()
	h.mu.Unlock()
	if !settings.AcceptingContracts {
		s.writeError(errors.New("host is not accepting new contracts"))
		return nil
	} else if len(s.so.RevisionTransactionSet) == 0 {
		err := errors.New("no such contract")
		s.writeError(err)
		return err
	}

	// Verify that the transaction coming over the wire is a proper renewal.
	renterSPK := s.so.RevisionTransactionSet[len(s.so.RevisionTransactionSet)-1].FileContractRevisions[0].UnlockConditions.PublicKeys[0]
	var renterPK crypto.PublicKey
	copy(renterPK[:], renterSPK.Key)
	err := h.managedVerifyRenewedContract(s.so, req.Transactions, renterPK)
	if err != nil {
		s.writeError(err)
		return extendErr("verification of renewal failed: ", err)
	}
	txnBuilder, newParents, newInputs, newOutputs, err := h.managedAddRenewCollateral(s.so, settings, req.Transactions)
	if err != nil {
		s.writeError(err)
		return extendErr("failed to add collateral: ", err)
	}
	// Send any new inputs and outputs that were added to the transaction.
	resp := modules.LoopContractAdditions{
		Parents: newParents,
		Inputs:  newInputs,
		Outputs: newOutputs,
	}
	if err := s.writeResponse(resp); err != nil {
		return err
	}

	// The renter will now send transaction signatures for the file contract
	// transaction and a signature for the implicit no-op file contract
	// revision.
	var renterSigs modules.LoopContractSignatures
	if err := s.readResponse(&renterSigs, contractSigsRespMaxLen); err != nil {
		s.writeError(err)
		return err
	}

	// The host adds the renter transaction signatures, then signs the
	// transaction and submits it to the blockchain, creating a storage
	// obligation in the process.
	h.mu.RLock()
	fc := req.Transactions[len(req.Transactions)-1].FileContracts[0]
	renewCollateral := renewContractCollateral(s.so, settings, fc)
	renewRevenue := renewBasePrice(s.so, settings, fc)
	renewRisk := renewBaseCollateral(s.so, settings, fc)
	h.mu.RUnlock()
	hostTxnSignatures, hostRevisionSignature, newSOID, err := h.managedFinalizeContract(txnBuilder, renterPK, renterSigs.ContractSignatures, renterSigs.RevisionSignature, s.so.SectorRoots, renewCollateral, renewRevenue, renewRisk, settings)
	if err != nil {
		s.writeError(err)
		return extendErr("failed to finalize contract: ", err)
	}
	defer h.managedUnlockStorageObligation(newSOID)

	// Send our signatures for the contract transaction and initial revision.
	hostSigs := modules.LoopContractSignatures{
		ContractSignatures: hostTxnSignatures,
		RevisionSignature:  hostRevisionSignature,
	}
	if err := s.writeResponse(hostSigs); err != nil {
		return err
	}

	return nil
}

// managedRPCLoopSectorRoots writes an RPC response containing the requested
// contract roots (along with signatures and a Merkle proof).
func (h *Host) managedRPCLoopSectorRoots(s *rpcSession) error {
	s.extendDeadline(modules.NegotiateDownloadTime)

	// Read the request.
	var req modules.LoopSectorRootsRequest
	if err := s.readRequest(&req, sectorRootsReqMaxLen); err != nil {
		// Reading may have failed due to a closed connection; regardless, it
		// doesn't hurt to try and tell the renter about it.
		s.writeError(err)
		return err
	}

	// Read some internal fields for later.
	h.mu.RLock()
	blockHeight := h.blockHeight
	secretKey := h.secretKey
	settings := h.externalSettings()
	h.mu.RUnlock()
	currentRevision := s.so.RevisionTransactionSet[len(s.so.RevisionTransactionSet)-1].FileContractRevisions[0]

	// Validate the request.
	var err error
	if req.NumRoots > settings.MaxDownloadBatchSize/crypto.HashSize {
		err = errLargeDownloadBatch
	}
	if req.RootOffset > uint64(len(s.so.SectorRoots)) || req.RootOffset+req.NumRoots > uint64(len(s.so.SectorRoots)) {
		err = errRequestOutOfBounds
	} else if len(req.NewValidProofValues) != len(currentRevision.NewValidProofOutputs) {
		err = errors.New("wrong number of valid proof values")
	} else if len(req.NewMissedProofValues) != len(currentRevision.NewMissedProofOutputs) {
		err = errors.New("wrong number of missed proof values")
	}
	if err != nil {
		s.writeError(err)
		return extendErr("download iteration request failed: ", err)
	}

	// construct the new revision
	newRevision := currentRevision
	newRevision.NewRevisionNumber = req.NewRevisionNumber
	newRevision.NewValidProofOutputs = make([]types.SiacoinOutput, len(currentRevision.NewValidProofOutputs))
	for i := range newRevision.NewValidProofOutputs {
		newRevision.NewValidProofOutputs[i] = types.SiacoinOutput{
			Value:      req.NewValidProofValues[i],
			UnlockHash: currentRevision.NewValidProofOutputs[i].UnlockHash,
		}
	}
	newRevision.NewMissedProofOutputs = make([]types.SiacoinOutput, len(currentRevision.NewMissedProofOutputs))
	for i := range newRevision.NewMissedProofOutputs {
		newRevision.NewMissedProofOutputs[i] = types.SiacoinOutput{
			Value:      req.NewMissedProofValues[i],
			UnlockHash: currentRevision.NewMissedProofOutputs[i].UnlockHash,
		}
	}

	expectedTransfer := settings.DownloadBandwidthPrice.Mul64(req.NumRoots).Mul64(crypto.HashSize)
	err = verifyPaymentRevision(currentRevision, newRevision, blockHeight, expectedTransfer)
	if err != nil {
		s.writeError(err)
		return extendErr("payment validation failed: ", err)
	}

	contractRoots := s.so.SectorRoots[req.RootOffset:][:req.NumRoots]

	// Sign the new revision.
	renterSig := types.TransactionSignature{
		ParentID:       crypto.Hash(newRevision.ParentID),
		CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
		PublicKeyIndex: 0,
		Signature:      req.Signature,
	}
	txn, err := createRevisionSignature(newRevision, renterSig, secretKey, blockHeight)
	if err != nil {
		s.writeError(err)
		return extendErr("failed to create revision signature: ", err)
	}

	// Update the storage obligation.
	paymentTransfer := currentRevision.NewValidProofOutputs[0].Value.Sub(newRevision.NewValidProofOutputs[0].Value)
	s.so.PotentialDownloadRevenue = s.so.PotentialDownloadRevenue.Add(paymentTransfer)
	s.so.RevisionTransactionSet = []types.Transaction{txn}
	h.mu.Lock()
	err = h.modifyStorageObligation(s.so, nil, nil, nil)
	h.mu.Unlock()
	if err != nil {
		s.writeError(err)
		return extendErr("failed to modify storage obligation: ", err)
	}

	// Construct the Merkle proof
	proofStart := int(req.RootOffset)
	proofEnd := int(req.RootOffset + req.NumRoots)
	proof := crypto.MerkleSectorRangeProof(s.so.SectorRoots, proofStart, proofEnd)

	// send the response
	resp := modules.LoopSectorRootsResponse{
		Signature:   txn.TransactionSignatures[1].Signature,
		SectorRoots: contractRoots,
		MerkleProof: proof,
	}
	if err := s.writeResponse(resp); err != nil {
		return err
	}
	return nil
}
