package host

import (
	"net"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"

	"github.com/coreos/bbolt"
)

func (h *Host) managedRPCLoopSettings(conn net.Conn) error {
	conn.SetDeadline(time.Now().Add(modules.NegotiateSettingsTime))

	h.mu.Lock()
	secretKey := h.secretKey
	hes := h.externalSettings()
	h.mu.Unlock()

	// Construct and send the response.
	//
	// TODO: we encode the settings twice here. If that becomes a performance
	// issue, optimizing it down to a single encode shouldn't be too
	// difficult; but for simplicity, leave it for now.
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

func (h *Host) managedRPCLoopRecentRevision(conn net.Conn) error {
	conn.SetDeadline(time.Now().Add(modules.NegotiateRecentRevisionTime))

	// Read the request.
	var req modules.LoopRecentRevisionRequest
	if err := encoding.NewDecoder(conn).Decode(&req); err != nil {
		// No sense writing an RPC error here
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

func (h *Host) managedRPCLoopDownload(conn net.Conn) error {
	conn.SetDeadline(time.Now().Add(modules.NegotiateDownloadTime))

	// Read the request.
	var req modules.LoopDownloadRequest
	if err := encoding.NewDecoder(conn).Decode(&req); err != nil {
		// No sense writing an RPC error here
		return err
	}
	fcid := req.Revision.ParentID

	// Lock the storage obligation.
	//
	// TODO: for performance, we will eventually want to keep the obligation
	// locked, instead of locking and unlocking for each iteration.
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
		modules.WriteRPCResponse(conn, nil, errRequestOutOfBounds)
		return extendErr("download iteration request failed: ", errRequestOutOfBounds)
	}
	expectedTransfer := settings.DownloadBandwidthPrice.Mul64(uint64(req.Length))
	err = verifyPaymentRevision(currentRevision, req.Revision, blockHeight, expectedTransfer)
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
	txn, err := createRevisionSignature(req.Revision, req.Signature, secretKey, blockHeight)
	if err != nil {
		modules.WriteRPCResponse(conn, nil, err)
		return extendErr("failed to create revision signature: ", err)
	}

	// Update the storage obligation.
	paymentTransfer := currentRevision.NewValidProofOutputs[0].Value.Sub(req.Revision.NewValidProofOutputs[0].Value)
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
		Signature:   txn.TransactionSignatures[1],
		Data:        data,
		MerkleProof: nil,
	}
	if err := modules.WriteRPCResponse(conn, resp, nil); err != nil {
		return err
	}
	return nil
}
