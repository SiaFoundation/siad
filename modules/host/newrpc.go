package host

import (
	"errors"
	"net"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"

	"github.com/coreos/bbolt"
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
func (h *Host) managedRPCLoopRecentRevision(conn net.Conn) error {
	conn.SetDeadline(time.Now().Add(modules.NegotiateRecentRevisionTime))

	// Read the request.
	var req modules.LoopRecentRevisionRequest
	if err := encoding.NewDecoder(conn).Decode(&req); err != nil {
		// Reading may have failed due to a closed connection; regardless, it
		// doesn't hurt to try and tell the renter about it.
		modules.WriteRPCResponse(conn, nil, err)
		return err
	}
	fcid := req.ContractID

	// Attempt to lock the storage obligation for the specified contract.
	//
	// TODO: is this necessary? Can we fetch the revision/sigs without locking
	// the obligation?
	if err := h.managedTryLockStorageObligation(fcid); err != nil {
		err = extendErr("could not get "+fcid.String()+" lock: ", ErrorInternal(err.Error()))
		modules.WriteRPCResponse(conn, nil, err)
		return err
	}
	defer h.managedUnlockStorageObligation(fcid)

	// Fetch the storage obligation and extract the revision and signatures.
	var so storageObligation
	h.mu.RLock()
	err := h.db.View(func(tx *bolt.Tx) error {
		var err error
		so, err = getStorageObligation(tx, fcid)
		return err
	})
	h.mu.RUnlock()
	if err != nil {
		err = extendErr("could not fetch "+fcid.String()+": ", ErrorInternal(err.Error()))
		modules.WriteRPCResponse(conn, nil, err)
		return err
	}
	txn := so.RevisionTransactionSet[len(so.RevisionTransactionSet)-1]
	rev := txn.FileContractRevisions[0]
	var sigs []types.TransactionSignature
	for _, sig := range txn.TransactionSignatures {
		// The transaction may have additional signatures that are only
		// relevant to the host.
		//
		// TODO: is this correct?
		if sig.ParentID == crypto.Hash(fcid) {
			sigs = append(sigs, sig)
		}
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

// managedRPCLoopDownload writes an RPC response containing the requested data
// (along with signatures and an optional Merkle proof).
func (h *Host) managedRPCLoopDownload(conn net.Conn) error {
	conn.SetDeadline(time.Now().Add(modules.NegotiateDownloadTime))

	// Read the request.
	var req modules.LoopDownloadRequest
	if err := encoding.NewDecoder(conn).Decode(&req); err != nil {
		// Reading may have failed due to a closed connection; regardless, it
		// doesn't hurt to try and tell the renter about it.
		modules.WriteRPCResponse(conn, nil, err)
		return err
	}
	fcid := req.ContractID

	// Lock the storage obligation.
	err := h.managedTryLockStorageObligation(fcid)
	if err != nil {
		modules.WriteRPCResponse(conn, nil, err)
		return extendErr("could not lock contract "+fcid.String()+": ", err)
	}
	defer h.managedUnlockStorageObligation(fcid)
	var so storageObligation
	h.mu.RLock()
	// Read some internal fields for later.
	blockHeight := h.blockHeight
	secretKey := h.secretKey
	settings := h.externalSettings()
	// Fetch the storage obligation from the db.
	err = h.db.View(func(tx *bolt.Tx) error {
		so, err = getStorageObligation(tx, fcid)
		return err
	})
	h.mu.RUnlock()
	if err != nil {
		modules.WriteRPCResponse(conn, nil, err)
		return extendErr("could not lock contract "+fcid.String()+": ", err)
	}
	currentRevision := so.RevisionTransactionSet[len(so.RevisionTransactionSet)-1].FileContractRevisions[0]

	// Validate the request.
	if uint64(req.Offset)+uint64(req.Length) > modules.SectorSize {
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
	err = h.modifyStorageObligation(so, nil, nil, nil)
	h.mu.Unlock()
	if err != nil {
		modules.WriteRPCResponse(conn, nil, err)
		return extendErr("failed to modify storage obligation: ", err)
	}

	// send the response
	resp := modules.LoopDownloadResponse{
		Signature:   txn.TransactionSignatures[1].Signature,
		Data:        data,
		MerkleProof: nil,
	}
	if err := modules.WriteRPCResponse(conn, resp, nil); err != nil {
		return err
	}
	return nil
}
