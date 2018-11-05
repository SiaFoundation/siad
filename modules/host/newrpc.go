package host

import (
	"errors"
	"net"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// managedRPCLoopSettings writes an RPC response containing the host's
// settings.
func (h *Host) managedRPCLoopSettings(conn net.Conn) error {
	conn.SetDeadline(time.Now().Add(modules.NegotiateSettingsTime))

	h.mu.Lock()
	secretKey := h.secretKey
	hes := h.externalSettings()
	h.mu.Unlock()

	// Construct and send the response.
	sig := crypto.SignHash(crypto.HashObject(hes), secretKey)
	resp := modules.LoopSettingsResponse{
		Settings:  hes,
		Signature: sig[:],
	}
	if err := modules.WriteRPCResponse(conn, resp, nil); err != nil {
		return err
	}
	return nil
}

// managedRPCLoopRecentRevision writes an RPC response containing the most
// recent revision of the requested contract.
func (h *Host) managedRPCLoopRecentRevision(conn net.Conn, so *storageObligation, challenge [16]byte) error {
	conn.SetDeadline(time.Now().Add(modules.NegotiateRecentRevisionTime))

	// Read the request.
	var req modules.LoopRecentRevisionRequest
	if err := encoding.NewDecoder(conn).Decode(&req); err != nil {
		// Reading may have failed due to a closed connection; regardless, it
		// doesn't hurt to try and tell the renter about it.
		modules.WriteRPCResponse(conn, nil, err)
		return err
	}

	// Fetch the revision and signatures.
	txn := so.RevisionTransactionSet[len(so.RevisionTransactionSet)-1]
	rev := txn.FileContractRevisions[0]
	var sigs []types.TransactionSignature
	for _, sig := range txn.TransactionSignatures {
		// The transaction may have additional signatures that are only
		// relevant to the host.
		if sig.ParentID == crypto.Hash(rev.ParentID) {
			sigs = append(sigs, sig)
		}
	}

	// Validate the renter's signature.
	hash := crypto.HashAll(modules.RPCChallengePrefix, challenge)
	var renterPK crypto.PublicKey
	var renterSig crypto.Signature
	copy(renterPK[:], rev.UnlockConditions.PublicKeys[0].Key)
	copy(renterSig[:], req.Signature)
	if crypto.VerifyHash(hash, renterPK, renterSig) != nil {
		err := errors.New("challenge signature is invalid")
		modules.WriteRPCResponse(conn, nil, err)
		return err
	}

	// Write the response.
	resp := modules.LoopRecentRevisionResponse{
		Revision:   rev,
		Signatures: sigs,
	}
	if err := modules.WriteRPCResponse(conn, resp, nil); err != nil {
		return err
	}
	return nil
}

// managedRPCLoopUpload reads an upload request and responds with a signature
// for the new revision.
func (h *Host) managedRPCLoopUpload(conn net.Conn, so *storageObligation) error {
	conn.SetDeadline(time.Now().Add(modules.NegotiateFileContractRevisionTime))

	// Read the request.
	var req modules.LoopUploadRequest
	if err := encoding.NewDecoder(conn).Decode(&req); err != nil {
		// Reading may have failed due to a closed connection; regardless, it
		// doesn't hurt to try and tell the renter about it.
		modules.WriteRPCResponse(conn, nil, err)
		return err
	}

	// Perform some basic input validation.
	if uint64(len(req.Data)) != modules.SectorSize {
		modules.WriteRPCResponse(conn, nil, errBadSectorSize)
		return errBadSectorSize
	}

	// Read some internal fields for later.
	h.mu.RLock()
	blockHeight := h.blockHeight
	secretKey := h.secretKey
	settings := h.externalSettings()
	h.mu.RUnlock()
	currentRevision := so.RevisionTransactionSet[len(so.RevisionTransactionSet)-1].FileContractRevisions[0]

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
	blocksRemaining := so.proofDeadline() - blockHeight
	blockBytesCurrency := types.NewCurrency64(uint64(blocksRemaining)).Mul64(modules.SectorSize)
	bandwidthRevenue := settings.UploadBandwidthPrice.Mul64(modules.SectorSize)
	storageRevenue := settings.StoragePrice.Mul(blockBytesCurrency)
	newCollateral := settings.Collateral.Mul(blockBytesCurrency)
	newRoot := crypto.MerkleRoot(req.Data)
	so.SectorRoots = append(so.SectorRoots, newRoot)
	newRevision.NewFileMerkleRoot = cachedMerkleRoot(so.SectorRoots)
	newRevenue := storageRevenue.Add(bandwidthRevenue)
	if err := verifyRevision(*so, newRevision, blockHeight, newRevenue, newCollateral); err != nil {
		modules.WriteRPCResponse(conn, nil, err)
		return extendErr("unable to verify updated contract: ", err)
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
		modules.WriteRPCResponse(conn, nil, err)
		return extendErr("failed to create revision signature: ", err)
	}

	// Update the storage obligation.
	so.PotentialStorageRevenue = so.PotentialStorageRevenue.Add(storageRevenue)
	so.RiskedCollateral = so.RiskedCollateral.Add(newCollateral)
	so.PotentialUploadRevenue = so.PotentialUploadRevenue.Add(bandwidthRevenue)
	so.RevisionTransactionSet = []types.Transaction{txn}
	h.mu.Lock()
	err = h.modifyStorageObligation(*so, nil, []crypto.Hash{newRoot}, [][]byte{req.Data})
	h.mu.Unlock()
	if err != nil {
		modules.WriteRPCResponse(conn, nil, err)
		return extendErr("failed to modify storage obligation: ", err)
	}

	// Send the response.
	resp := modules.LoopUploadResponse{
		Signature: txn.TransactionSignatures[1].Signature,
	}
	if err := modules.WriteRPCResponse(conn, resp, nil); err != nil {
		return err
	}
	return nil
}

// managedRPCLoopDownload writes an RPC response containing the requested data
// (along with signatures and an optional Merkle proof).
func (h *Host) managedRPCLoopDownload(conn net.Conn, so *storageObligation) error {
	conn.SetDeadline(time.Now().Add(modules.NegotiateDownloadTime))

	// Read the request.
	var req modules.LoopDownloadRequest
	if err := encoding.NewDecoder(conn).Decode(&req); err != nil {
		// Reading may have failed due to a closed connection; regardless, it
		// doesn't hurt to try and tell the renter about it.
		modules.WriteRPCResponse(conn, nil, err)
		return err
	}

	// Read some internal fields for later.
	h.mu.RLock()
	blockHeight := h.blockHeight
	secretKey := h.secretKey
	settings := h.externalSettings()
	h.mu.RUnlock()
	currentRevision := so.RevisionTransactionSet[len(so.RevisionTransactionSet)-1].FileContractRevisions[0]

	// Validate the request.
	var err error
	if uint64(req.Offset)+uint64(req.Length) > modules.SectorSize {
		err = errRequestOutOfBounds
	} else if req.Length == 0 {
		err = errors.New("length cannot be zero")
	} else if req.MerkleProof && (req.Offset%crypto.SegmentSize != 0 || req.Length%crypto.SegmentSize != 0) {
		err = errors.New("offset and length must be multiples of SegmentSize when requesting a Merkle proof")
	} else if len(req.NewValidProofValues) != len(currentRevision.NewValidProofOutputs) {
		err = errors.New("wrong number of valid proof values")
	} else if len(req.NewMissedProofValues) != len(currentRevision.NewMissedProofOutputs) {
		err = errors.New("wrong number of missed proof values")
	}
	if err != nil {
		modules.WriteRPCResponse(conn, nil, err)
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

	expectedTransfer := settings.DownloadBandwidthPrice.Mul64(uint64(req.Length))
	err = verifyPaymentRevision(currentRevision, newRevision, blockHeight, expectedTransfer)
	if err != nil {
		modules.WriteRPCResponse(conn, nil, err)
		return extendErr("payment validation failed: ", err)
	}

	// Fetch the requested data.
	sectorData, err := h.ReadSector(req.MerkleRoot)
	if err != nil {
		modules.WriteRPCResponse(conn, nil, err)
		return extendErr("failed to load sector: ", ErrorInternal(err.Error()))
	}
	data := sectorData[req.Offset : req.Offset+req.Length]

	// Construct the Merkle proof, if requested.
	var proof []crypto.Hash
	if req.MerkleProof {
		proofStart := int(req.Offset) / crypto.SegmentSize
		proofEnd := int(req.Offset+req.Length) / crypto.SegmentSize
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
		modules.WriteRPCResponse(conn, nil, err)
		return extendErr("failed to create revision signature: ", err)
	}

	// Update the storage obligation.
	paymentTransfer := currentRevision.NewValidProofOutputs[0].Value.Sub(newRevision.NewValidProofOutputs[0].Value)
	so.PotentialDownloadRevenue = so.PotentialDownloadRevenue.Add(paymentTransfer)
	so.RevisionTransactionSet = []types.Transaction{txn}
	h.mu.Lock()
	err = h.modifyStorageObligation(*so, nil, nil, nil)
	h.mu.Unlock()
	if err != nil {
		modules.WriteRPCResponse(conn, nil, err)
		return extendErr("failed to modify storage obligation: ", err)
	}

	// send the response
	resp := modules.LoopDownloadResponse{
		Signature:   txn.TransactionSignatures[1].Signature,
		Data:        data,
		MerkleProof: proof,
	}
	if err := modules.WriteRPCResponse(conn, resp, nil); err != nil {
		return err
	}
	return nil
}

// managedRPCLoopSectorRoots writes an RPC response containing the requested
// contract roots (along with signatures and a Merkle proof).
func (h *Host) managedRPCLoopSectorRoots(conn net.Conn, so *storageObligation) error {
	conn.SetDeadline(time.Now().Add(modules.NegotiateDownloadTime))

	// Read the request.
	var req modules.LoopSectorRootsRequest
	if err := encoding.NewDecoder(conn).Decode(&req); err != nil {
		// Reading may have failed due to a closed connection; regardless, it
		// doesn't hurt to try and tell the renter about it.
		modules.WriteRPCResponse(conn, nil, err)
		return err
	}
	err := errors.New("Merkle proofs are not implemented")
	modules.WriteRPCResponse(conn, nil, err)
	return err

	// Read some internal fields for later.
	h.mu.RLock()
	blockHeight := h.blockHeight
	secretKey := h.secretKey
	settings := h.externalSettings()
	h.mu.RUnlock()
	currentRevision := so.RevisionTransactionSet[len(so.RevisionTransactionSet)-1].FileContractRevisions[0]

	// Validate the request.
	if req.NumRoots > settings.MaxDownloadBatchSize/crypto.HashSize {
		err = errLargeDownloadBatch
	}
	if req.RootOffset > uint64(len(so.SectorRoots)) || req.RootOffset+req.NumRoots > uint64(len(so.SectorRoots)) {
		err = errRequestOutOfBounds
	} else if len(req.NewValidProofValues) != len(currentRevision.NewValidProofOutputs) {
		err = errors.New("wrong number of valid proof values")
	} else if len(req.NewMissedProofValues) != len(currentRevision.NewMissedProofOutputs) {
		err = errors.New("wrong number of missed proof values")
	}
	if err != nil {
		modules.WriteRPCResponse(conn, nil, err)
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
		modules.WriteRPCResponse(conn, nil, err)
		return extendErr("payment validation failed: ", err)
	}

	contractRoots := so.SectorRoots[req.RootOffset:][:req.NumRoots]

	// Sign the new revision.
	renterSig := types.TransactionSignature{
		ParentID:       crypto.Hash(newRevision.ParentID),
		CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
		PublicKeyIndex: 0,
		Signature:      req.Signature,
	}
	txn, err := createRevisionSignature(newRevision, renterSig, secretKey, blockHeight)
	if err != nil {
		modules.WriteRPCResponse(conn, nil, err)
		return extendErr("failed to create revision signature: ", err)
	}

	// Update the storage obligation.
	paymentTransfer := currentRevision.NewValidProofOutputs[0].Value.Sub(newRevision.NewValidProofOutputs[0].Value)
	so.PotentialDownloadRevenue = so.PotentialDownloadRevenue.Add(paymentTransfer)
	so.RevisionTransactionSet = []types.Transaction{txn}
	h.mu.Lock()
	err = h.modifyStorageObligation(*so, nil, nil, nil)
	h.mu.Unlock()
	if err != nil {
		modules.WriteRPCResponse(conn, nil, err)
		return extendErr("failed to modify storage obligation: ", err)
	}

	// send the response
	resp := modules.LoopSectorRootsResponse{
		Signature:   txn.TransactionSignatures[1].Signature,
		SectorRoots: contractRoots,
		MerkleProof: nil,
	}
	if err := modules.WriteRPCResponse(conn, resp, nil); err != nil {
		return err
	}
	return nil
}
