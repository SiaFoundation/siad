package proto

import (
	"bytes"
	"crypto/cipher"
	"encoding/json"
	"io"
	"math/bits"
	"net"
	"sort"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/ratelimit"
)

// A Session is an ongoing exchange of RPCs via the renter-host protocol.
//
// TODO: The session type needs access to a logger. Probably the renter logger.
type Session struct {
	aead        cipher.AEAD
	challenge   [16]byte
	closeChan   chan struct{}
	conn        net.Conn
	contractID  types.FileContractID
	contractSet *ContractSet
	deps        modules.Dependencies
	hdb         hostDB
	height      types.BlockHeight
	host        modules.HostDBEntry
	once        sync.Once
}

// writeRequest sends an encrypted RPC request to the host.
func (s *Session) writeRequest(rpcID types.Specifier, req interface{}) error {
	return modules.WriteRPCRequest(s.conn, s.aead, rpcID, req)
}

// writeResponse writes an encrypted RPC response to the host.
func (s *Session) writeResponse(resp interface{}, err error) error {
	return modules.WriteRPCResponse(s.conn, s.aead, resp, err)
}

// readResponse reads an encrypted RPC response from the host.
func (s *Session) readResponse(resp interface{}, maxLen uint64) error {
	return modules.ReadRPCResponse(s.conn, s.aead, resp, maxLen)
}

// call is a helper method that calls writeRequest followed by readResponse.
func (s *Session) call(rpcID types.Specifier, req, resp interface{}, maxLen uint64) error {
	if err := s.writeRequest(rpcID, req); err != nil {
		return err
	}
	return s.readResponse(resp, maxLen)
}

// Lock calls the Lock RPC, locking the supplied contract and returning its
// most recent revision.
func (s *Session) Lock(id types.FileContractID, secretKey crypto.SecretKey) (types.FileContractRevision, []types.TransactionSignature, error) {
	sig := crypto.SignHash(crypto.HashAll(modules.RPCChallengePrefix, s.challenge), secretKey)
	req := modules.LoopLockRequest{
		ContractID: id,
		Signature:  sig[:],
		Timeout:    defaultContractLockTimeout,
	}

	timeoutDur := time.Duration(defaultContractLockTimeout) * time.Millisecond
	extendDeadline(s.conn, modules.NegotiateSettingsTime+timeoutDur)
	var resp modules.LoopLockResponse
	if err := s.call(modules.RPCLoopLock, req, &resp, modules.RPCMinLen); err != nil {
		return types.FileContractRevision{}, nil, err
	}
	// Unconditionally update the challenge.
	s.challenge = resp.NewChallenge

	if !resp.Acquired {
		return resp.Revision, resp.Signatures, errors.New("contract is locked by another party")
	}
	// Set the new Session contract.
	s.contractID = id
	// Verify the public keys in the claimed revision.
	expectedUnlockConditions := types.UnlockConditions{
		PublicKeys: []types.SiaPublicKey{
			types.Ed25519PublicKey(secretKey.PublicKey()),
			s.host.PublicKey,
		},
		SignaturesRequired: 2,
	}
	if resp.Revision.UnlockConditions.UnlockHash() != expectedUnlockConditions.UnlockHash() {
		return resp.Revision, resp.Signatures, errors.New("host's claimed revision has wrong unlock conditions")
	}
	// Verify the claimed signatures.
	if err := modules.VerifyFileContractRevisionTransactionSignatures(resp.Revision, resp.Signatures, s.height); err != nil {
		return resp.Revision, resp.Signatures, err
	}
	return resp.Revision, resp.Signatures, nil
}

// Unlock calls the Unlock RPC, unlocking the currently-locked contract.
func (s *Session) Unlock() error {
	if s.contractID == (types.FileContractID{}) {
		return errors.New("no contract locked")
	}
	extendDeadline(s.conn, modules.NegotiateSettingsTime)
	return s.writeRequest(modules.RPCLoopUnlock, nil)
}

// Settings calls the Settings RPC, returning the host's reported settings.
func (s *Session) Settings() (modules.HostExternalSettings, error) {
	extendDeadline(s.conn, modules.NegotiateSettingsTime)
	var resp modules.LoopSettingsResponse
	if err := s.call(modules.RPCLoopSettings, nil, &resp, modules.RPCMinLen); err != nil {
		return modules.HostExternalSettings{}, err
	}
	if err := json.Unmarshal(resp.Settings, &s.host.HostExternalSettings); err != nil {
		return modules.HostExternalSettings{}, err
	}
	return s.host.HostExternalSettings, nil
}

// Append calls the Write RPC with a single Append action, returning the
// updated contract and the Merkle root of the appended sector.
func (s *Session) Append(data []byte) (_ modules.RenterContract, _ crypto.Hash, err error) {
	rc, err := s.Write([]modules.LoopWriteAction{{Type: modules.WriteActionAppend, Data: data}})
	return rc, crypto.MerkleRoot(data), err
}

// Replace calls the Write RPC with a series of actions that replace the sector
// at the specified index with data, returning the updated contract and the
// Merkle root of the new sector.
func (s *Session) Replace(data []byte, sectorIndex uint64, trim bool) (_ modules.RenterContract, _ crypto.Hash, err error) {
	sc, haveContract := s.contractSet.Acquire(s.contractID)
	if !haveContract {
		return modules.RenterContract{}, crypto.Hash{}, errors.New("contract not present in contract set")
	}
	defer s.contractSet.Return(sc)
	// get current number of sectors
	numSectors := sc.header.LastRevision().NewFileSize / modules.SectorSize
	actions := []modules.LoopWriteAction{
		// append the new sector
		{Type: modules.WriteActionAppend, Data: data},
		// swap the new sector with the old sector
		{Type: modules.WriteActionSwap, A: 0, B: numSectors},
	}
	if trim {
		// delete the old sector
		actions = append(actions, modules.LoopWriteAction{Type: modules.WriteActionTrim, A: 1})
	}

	rc, err := s.write(sc, actions)
	return rc, crypto.MerkleRoot(data), err
}

// Write implements the Write RPC, except for ActionUpdate. A Merkle proof is
// always requested.
func (s *Session) Write(actions []modules.LoopWriteAction) (_ modules.RenterContract, err error) {
	sc, haveContract := s.contractSet.Acquire(s.contractID)
	if !haveContract {
		return modules.RenterContract{}, errors.New("contract not present in contract set")
	}
	defer s.contractSet.Return(sc)
	return s.write(sc, actions)
}

func (s *Session) write(sc *SafeContract, actions []modules.LoopWriteAction) (_ modules.RenterContract, err error) {
	contract := sc.header // for convenience

	// calculate price per sector
	blockBytes := types.NewCurrency64(modules.SectorSize * uint64(contract.LastRevision().NewWindowEnd-s.height))
	sectorBandwidthPrice := s.host.UploadBandwidthPrice.Mul64(modules.SectorSize)
	sectorStoragePrice := s.host.StoragePrice.Mul(blockBytes)
	sectorCollateral := s.host.Collateral.Mul(blockBytes)

	// calculate the new Merkle root set and total cost/collateral
	var bandwidthPrice, storagePrice, collateral types.Currency
	newFileSize := contract.LastRevision().NewFileSize
	for _, action := range actions {
		switch action.Type {
		case modules.WriteActionAppend:
			bandwidthPrice = bandwidthPrice.Add(sectorBandwidthPrice)
			newFileSize += modules.SectorSize

		case modules.WriteActionTrim:
			newFileSize -= modules.SectorSize * action.A

		case modules.WriteActionSwap:

		case modules.WriteActionUpdate:
			return modules.RenterContract{}, errors.New("update not supported")

		default:
			build.Critical("unknown action type", action.Type)
		}
	}

	if newFileSize > contract.LastRevision().NewFileSize {
		addedSectors := (newFileSize - contract.LastRevision().NewFileSize) / modules.SectorSize
		storagePrice = sectorStoragePrice.Mul64(addedSectors)
		collateral = sectorCollateral.Mul64(addedSectors)
	}

	// estimate cost of Merkle proof
	proofSize := crypto.HashSize * (128 + len(actions))
	bandwidthPrice = bandwidthPrice.Add(s.host.DownloadBandwidthPrice.Mul64(uint64(proofSize)))

	// to mitigate small errors (e.g. differing block heights), fudge the
	// price and collateral by hostPriceLeeway.
	cost := s.host.BaseRPCPrice.Add(bandwidthPrice).Add(storagePrice).MulFloat(1 + hostPriceLeeway)
	collateral = collateral.MulFloat(1 - hostPriceLeeway)

	// check that enough funds are available
	if contract.RenterFunds().Cmp(cost) < 0 {
		return modules.RenterContract{}, errors.New("contract has insufficient funds to support upload")
	}
	if contract.LastRevision().NewMissedProofOutputs[1].Value.Cmp(collateral) < 0 {
		// The contract doesn't have enough value in it to supply the
		// collateral. Instead of giving up, have the host put up everything
		// that remains, even if that is zero. The renter was aware when the
		// contract was formed that there may not be enough collateral in the
		// contract to cover the full storage needs for the renter, yet the
		// renter formed the contract anyway. Therefore the renter must think
		// that it's sufficient.
		//
		// TODO: log.Debugln here to indicate that the host is having issues
		// supplying collateral.
		//
		// TODO: We may in the future want to have the renter perform a renewal
		// on this contract so that the host can refill the collateral. That's a
		// concern at the renter level though, not at the session level. The
		// session should still be assuming that if the renter is willing to use
		// this contract, the renter is aware that there isn't enough collateral
		// remaining and is happy to use the contract anyway. Therefore this
		// TODO should be moved to a different part of the codebase.
		collateral = contract.LastRevision().NewMissedProofOutputs[1].Value
	}

	// create the revision; we will update the Merkle root later
	rev := newRevision(contract.LastRevision(), cost)
	rev.NewMissedProofOutputs[1].Value = rev.NewMissedProofOutputs[1].Value.Sub(collateral)
	rev.NewMissedProofOutputs[2].Value = rev.NewMissedProofOutputs[2].Value.Add(collateral)
	rev.NewFileSize = newFileSize

	// create the request
	req := modules.LoopWriteRequest{
		Actions:           actions,
		MerkleProof:       true,
		NewRevisionNumber: rev.NewRevisionNumber,
	}
	req.NewValidProofValues = make([]types.Currency, len(rev.NewValidProofOutputs))
	for i, o := range rev.NewValidProofOutputs {
		req.NewValidProofValues[i] = o.Value
	}
	req.NewMissedProofValues = make([]types.Currency, len(rev.NewMissedProofOutputs))
	for i, o := range rev.NewMissedProofOutputs {
		req.NewMissedProofValues[i] = o.Value
	}

	// record the change we are about to make to the contract. If we lose power
	// mid-revision, this allows us to restore either the pre-revision or
	// post-revision contract.
	//
	// TODO: update this for non-local root storage
	walTxn, err := sc.recordUploadIntent(rev, crypto.Hash{}, storagePrice, bandwidthPrice)
	if err != nil {
		return modules.RenterContract{}, err
	}

	defer func() {
		// Increase Successful/Failed interactions accordingly
		if err != nil {
			s.hdb.IncrementFailedInteractions(s.host.PublicKey)
		} else {
			s.hdb.IncrementSuccessfulInteractions(s.host.PublicKey)
		}

		// reset deadline
		extendDeadline(s.conn, time.Hour)
	}()

	// Disrupt here before sending the signed revision to the host.
	if s.deps.Disrupt("InterruptUploadBeforeSendingRevision") {
		return modules.RenterContract{}, errors.New("InterruptUploadBeforeSendingRevision disrupt")
	}

	// send Write RPC request
	extendDeadline(s.conn, modules.NegotiateFileContractRevisionTime)
	if err := s.writeRequest(modules.RPCLoopWrite, req); err != nil {
		return modules.RenterContract{}, err
	}

	// read Merkle proof from host
	var merkleResp modules.LoopWriteMerkleProof
	if err := s.readResponse(&merkleResp, modules.RPCMinLen); err != nil {
		return modules.RenterContract{}, err
	}
	// verify the proof, first by verifying the old Merkle root...
	numSectors := contract.LastRevision().NewFileSize / modules.SectorSize
	proofRanges := calculateProofRanges(actions, numSectors)
	proofHashes := merkleResp.OldSubtreeHashes
	leafHashes := merkleResp.OldLeafHashes
	oldRoot, newRoot := contract.LastRevision().NewFileMerkleRoot, merkleResp.NewMerkleRoot
	if !crypto.VerifyDiffProof(proofRanges, numSectors, proofHashes, leafHashes, oldRoot) {
		return modules.RenterContract{}, errors.New("invalid Merkle proof for old root")
	}
	// ...then by modifying the leaves and verifying the new Merkle root
	leafHashes = modifyLeaves(leafHashes, actions, numSectors)
	proofRanges = modifyProofRanges(proofRanges, actions, numSectors)
	if !crypto.VerifyDiffProof(proofRanges, numSectors, proofHashes, leafHashes, newRoot) {
		return modules.RenterContract{}, errors.New("invalid Merkle proof for new root")
	}

	// update the revision, sign it, and send it
	rev.NewFileMerkleRoot = newRoot
	txn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{rev},
		TransactionSignatures: []types.TransactionSignature{
			{
				ParentID:       crypto.Hash(rev.ParentID),
				CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
				PublicKeyIndex: 0, // renter key is always first -- see formContract
			},
			{
				ParentID:       crypto.Hash(rev.ParentID),
				PublicKeyIndex: 1,
				CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
				Signature:      nil, // to be provided by host
			},
		},
	}
	sig := crypto.SignHash(txn.SigHash(0, s.height), contract.SecretKey)
	txn.TransactionSignatures[0].Signature = sig[:]
	renterSig := modules.LoopWriteResponse{
		Signature: sig[:],
	}
	if err := s.writeResponse(renterSig, nil); err != nil {
		return modules.RenterContract{}, err
	}

	// read the host's signature
	var hostSig modules.LoopWriteResponse
	if err := s.readResponse(&hostSig, modules.RPCMinLen); err != nil {
		return modules.RenterContract{}, err
	}
	txn.TransactionSignatures[1].Signature = hostSig.Signature

	// Disrupt here before updating the contract.
	if s.deps.Disrupt("InterruptUploadAfterSendingRevision") {
		return modules.RenterContract{}, errors.New("InterruptUploadAfterSendingRevision disrupt")
	}

	// update contract
	//
	// TODO: unnecessary?
	err = sc.commitUpload(walTxn, txn, crypto.Hash{}, storagePrice, bandwidthPrice)
	if err != nil {
		return modules.RenterContract{}, err
	}
	return sc.Metadata(), nil
}

// Read calls the Read RPC, writing the requested data to w. The RPC can be
// cancelled (with a granularity of one section) via the cancel channel.
func (s *Session) Read(w io.Writer, req modules.LoopReadRequest, cancel <-chan struct{}) (_ modules.RenterContract, err error) {
	// Reset deadline when finished.
	defer extendDeadline(s.conn, time.Hour)

	// Sanity-check the request.
	for _, sec := range req.Sections {
		if uint64(sec.Offset)+uint64(sec.Length) > modules.SectorSize {
			return modules.RenterContract{}, errors.New("illegal offset and/or length")
		}
		if req.MerkleProof {
			if sec.Offset%crypto.SegmentSize != 0 || sec.Length%crypto.SegmentSize != 0 {
				return modules.RenterContract{}, errors.New("offset and length must be multiples of SegmentSize when requesting a Merkle proof")
			}
		}
	}

	// Acquire the contract.
	sc, haveContract := s.contractSet.Acquire(s.contractID)
	if !haveContract {
		return modules.RenterContract{}, errors.New("contract not present in contract set")
	}
	defer s.contractSet.Return(sc)
	contract := sc.header // for convenience

	// calculate estimated bandwidth
	var totalLength uint64
	for _, sec := range req.Sections {
		totalLength += uint64(sec.Length)
	}
	var estProofHashes uint64
	if req.MerkleProof {
		// use the worst-case proof size of 2*tree depth (this occurs when
		// proving across the two leaves in the center of the tree)
		estHashesPerProof := 2 * bits.Len64(modules.SectorSize/crypto.SegmentSize)
		estProofHashes = uint64(len(req.Sections) * estHashesPerProof)
	}
	estBandwidth := totalLength + estProofHashes*crypto.HashSize
	if estBandwidth < modules.RPCMinLen {
		estBandwidth = modules.RPCMinLen
	}
	// calculate sector accesses
	sectorAccesses := make(map[crypto.Hash]struct{})
	for _, sec := range req.Sections {
		sectorAccesses[sec.MerkleRoot] = struct{}{}
	}
	// calculate price
	bandwidthPrice := s.host.DownloadBandwidthPrice.Mul64(estBandwidth)
	sectorAccessPrice := s.host.SectorAccessPrice.Mul64(uint64(len(sectorAccesses)))
	price := s.host.BaseRPCPrice.Add(bandwidthPrice).Add(sectorAccessPrice)
	if contract.RenterFunds().Cmp(price) < 0 {
		return modules.RenterContract{}, errors.New("contract has insufficient funds to support download")
	}
	// To mitigate small errors (e.g. differing block heights), fudge the
	// price and collateral by 0.2%.
	price = price.MulFloat(1 + hostPriceLeeway)

	// create the download revision and sign it
	rev := newDownloadRevision(contract.LastRevision(), price)
	txn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{rev},
		TransactionSignatures: []types.TransactionSignature{
			{
				ParentID:       crypto.Hash(rev.ParentID),
				CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
				PublicKeyIndex: 0, // renter key is always first -- see formContract
			},
			{
				ParentID:       crypto.Hash(rev.ParentID),
				PublicKeyIndex: 1,
				CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
				Signature:      nil, // to be provided by host
			},
		},
	}
	sig := crypto.SignHash(txn.SigHash(0, s.height), contract.SecretKey)
	txn.TransactionSignatures[0].Signature = sig[:]

	req.NewRevisionNumber = rev.NewRevisionNumber
	req.NewValidProofValues = make([]types.Currency, len(rev.NewValidProofOutputs))
	for i, o := range rev.NewValidProofOutputs {
		req.NewValidProofValues[i] = o.Value
	}
	req.NewMissedProofValues = make([]types.Currency, len(rev.NewMissedProofOutputs))
	for i, o := range rev.NewMissedProofOutputs {
		req.NewMissedProofValues[i] = o.Value
	}
	req.Signature = sig[:]

	// record the change we are about to make to the contract. If we lose power
	// mid-revision, this allows us to restore either the pre-revision or
	// post-revision contract.
	walTxn, err := sc.recordDownloadIntent(rev, price)
	if err != nil {
		return modules.RenterContract{}, err
	}

	// Increase Successful/Failed interactions accordingly
	defer func() {
		if err != nil {
			s.hdb.IncrementFailedInteractions(contract.HostPublicKey())
		} else {
			s.hdb.IncrementSuccessfulInteractions(contract.HostPublicKey())
		}
	}()

	// Disrupt before sending the signed revision to the host.
	if s.deps.Disrupt("InterruptDownloadBeforeSendingRevision") {
		return modules.RenterContract{}, errors.New("InterruptDownloadBeforeSendingRevision disrupt")
	}

	// send request
	extendDeadline(s.conn, modules.NegotiateDownloadTime)
	err = s.writeRequest(modules.RPCLoopRead, req)
	if err != nil {
		return modules.RenterContract{}, err
	}

	// spawn a goroutine to handle cancellation
	doneChan := make(chan struct{})
	go func() {
		select {
		case <-cancel:
		case <-doneChan:
		}
		s.writeResponse(modules.RPCLoopReadStop, nil)
	}()
	// ensure we send RPCLoopReadStop before returning
	defer close(doneChan)

	// read responses
	var hostSig []byte
	for _, sec := range req.Sections {
		var resp modules.LoopReadResponse
		err = s.readResponse(&resp, modules.RPCMinLen+uint64(sec.Length))
		if err != nil {
			return modules.RenterContract{}, err
		}
		// The host may have sent data, a signature, or both. If they sent data,
		// validate it.
		if len(resp.Data) > 0 {
			if len(resp.Data) != int(sec.Length) {
				return modules.RenterContract{}, errors.New("host did not send enough sector data")
			}
			if req.MerkleProof {
				proofStart := int(sec.Offset) / crypto.SegmentSize
				proofEnd := int(sec.Offset+sec.Length) / crypto.SegmentSize
				if !crypto.VerifyRangeProof(resp.Data, resp.MerkleProof, proofStart, proofEnd, sec.MerkleRoot) {
					return modules.RenterContract{}, errors.New("host provided incorrect sector data or Merkle proof")
				}
			}
			// write sector data
			if _, err := w.Write(resp.Data); err != nil {
				return modules.RenterContract{}, err
			}
		}
		// If the host sent a signature, exit the loop; they won't be sending
		// any more data
		if len(resp.Signature) > 0 {
			hostSig = resp.Signature
			break
		}
	}
	if hostSig == nil {
		// the host is required to send a signature; if they haven't sent one
		// yet, they should send an empty ReadResponse containing just the
		// signature.
		var resp modules.LoopReadResponse
		err = s.readResponse(&resp, modules.RPCMinLen)
		if err != nil {
			return modules.RenterContract{}, err
		}
		hostSig = resp.Signature
	}
	txn.TransactionSignatures[1].Signature = hostSig

	// Disrupt before committing.
	if s.deps.Disrupt("InterruptDownloadAfterSendingRevision") {
		return modules.RenterContract{}, errors.New("InterruptDownloadAfterSendingRevision disrupt")
	}

	// update contract and metrics
	if err := sc.commitDownload(walTxn, txn, price); err != nil {
		return modules.RenterContract{}, err
	}

	return sc.Metadata(), nil
}

// ReadSection calls the Read RPC with a single section and returns the
// requested data. A Merkle proof is always requested.
func (s *Session) ReadSection(root crypto.Hash, offset, length uint32) (_ modules.RenterContract, _ []byte, err error) {
	req := modules.LoopReadRequest{
		Sections: []modules.LoopReadRequestSection{{
			MerkleRoot: root,
			Offset:     offset,
			Length:     length,
		}},
		MerkleProof: true,
	}
	var buf bytes.Buffer
	contract, err := s.Read(&buf, req, nil)
	return contract, buf.Bytes(), err
}

// SectorRoots calls the contract roots download RPC and returns the requested sector roots. The
// Revision and Signature fields of req are filled in automatically. If a
// Merkle proof is requested, it is verified.
func (s *Session) SectorRoots(req modules.LoopSectorRootsRequest) (_ modules.RenterContract, _ []crypto.Hash, err error) {
	// Reset deadline when finished.
	defer extendDeadline(s.conn, time.Hour)

	// Acquire the contract.
	sc, haveContract := s.contractSet.Acquire(s.contractID)
	if !haveContract {
		return modules.RenterContract{}, nil, errors.New("contract not present in contract set")
	}
	defer s.contractSet.Return(sc)
	contract := sc.header // for convenience

	// calculate price
	estProofHashes := bits.Len64(contract.LastRevision().NewFileSize / modules.SectorSize)
	estBandwidth := (uint64(estProofHashes) + req.NumRoots) * crypto.HashSize
	if estBandwidth < modules.RPCMinLen {
		estBandwidth = modules.RPCMinLen
	}
	bandwidthPrice := s.host.DownloadBandwidthPrice.Mul64(estBandwidth)
	price := s.host.BaseRPCPrice.Add(bandwidthPrice)
	if contract.RenterFunds().Cmp(price) < 0 {
		return modules.RenterContract{}, nil, errors.New("contract has insufficient funds to support sector roots download")
	}
	// To mitigate small errors (e.g. differing block heights), fudge the
	// price and collateral by 0.2%.
	price = price.MulFloat(1 + hostPriceLeeway)

	// create the download revision and sign it
	rev := newDownloadRevision(contract.LastRevision(), price)
	txn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{rev},
		TransactionSignatures: []types.TransactionSignature{
			{
				ParentID:       crypto.Hash(rev.ParentID),
				CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
				PublicKeyIndex: 0, // renter key is always first -- see formContract
			},
			{
				ParentID:       crypto.Hash(rev.ParentID),
				PublicKeyIndex: 1,
				CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
				Signature:      nil, // to be provided by host
			},
		},
	}
	sig := crypto.SignHash(txn.SigHash(0, s.height), contract.SecretKey)
	txn.TransactionSignatures[0].Signature = sig[:]

	// fill in the missing request fields
	req.NewRevisionNumber = rev.NewRevisionNumber
	req.NewValidProofValues = make([]types.Currency, len(rev.NewValidProofOutputs))
	for i, o := range rev.NewValidProofOutputs {
		req.NewValidProofValues[i] = o.Value
	}
	req.NewMissedProofValues = make([]types.Currency, len(rev.NewMissedProofOutputs))
	for i, o := range rev.NewMissedProofOutputs {
		req.NewMissedProofValues[i] = o.Value
	}
	req.Signature = sig[:]

	// record the change we are about to make to the contract. If we lose power
	// mid-revision, this allows us to restore either the pre-revision or
	// post-revision contract.
	walTxn, err := sc.recordDownloadIntent(rev, price)
	if err != nil {
		return modules.RenterContract{}, nil, err
	}

	// Increase Successful/Failed interactions accordingly
	defer func() {
		if err != nil {
			s.hdb.IncrementFailedInteractions(contract.HostPublicKey())
		} else {
			s.hdb.IncrementSuccessfulInteractions(contract.HostPublicKey())
		}
	}()

	// send SectorRoots RPC request
	extendDeadline(s.conn, modules.NegotiateDownloadTime)
	var resp modules.LoopSectorRootsResponse
	err = s.call(modules.RPCLoopSectorRoots, req, &resp, modules.RPCMinLen+(req.NumRoots*crypto.HashSize))
	if err != nil {
		return modules.RenterContract{}, nil, err
	}
	// verify the response
	if len(resp.SectorRoots) != int(req.NumRoots) {
		return modules.RenterContract{}, nil, errors.New("host did not send the requested number of sector roots")
	}
	proofStart, proofEnd := int(req.RootOffset), int(req.RootOffset+req.NumRoots)
	if !crypto.VerifySectorRangeProof(resp.SectorRoots, resp.MerkleProof, proofStart, proofEnd, rev.NewFileMerkleRoot) {
		return modules.RenterContract{}, nil, errors.New("host provided incorrect sector data or Merkle proof")
	}

	// add host signature
	txn.TransactionSignatures[1].Signature = resp.Signature

	// update contract and metrics
	if err := sc.commitDownload(walTxn, txn, price); err != nil {
		return modules.RenterContract{}, nil, err
	}

	return sc.Metadata(), resp.SectorRoots, nil
}

// RecoverSectorRoots calls the contract roots download RPC and returns the requested sector roots. The
// Revision and Signature fields of req are filled in automatically. If a
// Merkle proof is requested, it is verified.
func (s *Session) RecoverSectorRoots(lastRev types.FileContractRevision, sk crypto.SecretKey) (_ types.Transaction, _ []crypto.Hash, err error) {
	// Calculate total roots we need to fetch.
	numRoots := lastRev.NewFileSize / modules.SectorSize
	if lastRev.NewFileSize%modules.SectorSize != 0 {
		numRoots++
	}
	// Create the request.
	req := modules.LoopSectorRootsRequest{
		RootOffset: 0,
		NumRoots:   numRoots,
	}
	// Reset deadline when finished.
	defer extendDeadline(s.conn, time.Hour)

	// calculate price
	estProofHashes := bits.Len64(lastRev.NewFileSize / modules.SectorSize)
	estBandwidth := (uint64(estProofHashes) + req.NumRoots) * crypto.HashSize
	if estBandwidth < modules.RPCMinLen {
		estBandwidth = modules.RPCMinLen
	}
	bandwidthPrice := s.host.DownloadBandwidthPrice.Mul64(estBandwidth)
	price := s.host.BaseRPCPrice.Add(bandwidthPrice)
	if lastRev.RenterFunds().Cmp(price) < 0 {
		return types.Transaction{}, nil, errors.New("contract has insufficient funds to support sector roots download")
	}
	// To mitigate small errors (e.g. differing block heights), fudge the
	// price and collateral by 0.2%.
	price = price.MulFloat(1 + hostPriceLeeway)

	// create the download revision and sign it
	rev := newDownloadRevision(lastRev, price)
	txn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{rev},
		TransactionSignatures: []types.TransactionSignature{
			{
				ParentID:       crypto.Hash(rev.ParentID),
				CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
				PublicKeyIndex: 0, // renter key is always first -- see formContract
			},
			{
				ParentID:       crypto.Hash(rev.ParentID),
				PublicKeyIndex: 1,
				CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
				Signature:      nil, // to be provided by host
			},
		},
	}
	sig := crypto.SignHash(txn.SigHash(0, s.height), sk)
	txn.TransactionSignatures[0].Signature = sig[:]

	// fill in the missing request fields
	req.NewRevisionNumber = rev.NewRevisionNumber
	req.NewValidProofValues = make([]types.Currency, len(rev.NewValidProofOutputs))
	for i, o := range rev.NewValidProofOutputs {
		req.NewValidProofValues[i] = o.Value
	}
	req.NewMissedProofValues = make([]types.Currency, len(rev.NewMissedProofOutputs))
	for i, o := range rev.NewMissedProofOutputs {
		req.NewMissedProofValues[i] = o.Value
	}
	req.Signature = sig[:]

	// Increase Successful/Failed interactions accordingly
	defer func() {
		if err != nil {
			s.hdb.IncrementFailedInteractions(s.host.PublicKey)
		} else {
			s.hdb.IncrementSuccessfulInteractions(s.host.PublicKey)
		}
	}()

	// send SectorRoots RPC request
	extendDeadline(s.conn, modules.NegotiateDownloadTime)
	var resp modules.LoopSectorRootsResponse
	err = s.call(modules.RPCLoopSectorRoots, req, &resp, modules.RPCMinLen+(req.NumRoots*crypto.HashSize))
	if err != nil {
		return types.Transaction{}, nil, err
	}
	// verify the response
	if len(resp.SectorRoots) != int(req.NumRoots) {
		return types.Transaction{}, nil, errors.New("host did not send the requested number of sector roots")
	}
	proofStart, proofEnd := int(req.RootOffset), int(req.RootOffset+req.NumRoots)
	if !crypto.VerifySectorRangeProof(resp.SectorRoots, resp.MerkleProof, proofStart, proofEnd, rev.NewFileMerkleRoot) {
		return types.Transaction{}, nil, errors.New("host provided incorrect sector data or Merkle proof")
	}

	// add host signature
	txn.TransactionSignatures[1].Signature = resp.Signature
	return txn, resp.SectorRoots, nil
}

// shutdown terminates the revision loop and signals the goroutine spawned in
// NewSession to return.
func (s *Session) shutdown() {
	extendDeadline(s.conn, modules.NegotiateSettingsTime)
	// don't care about this error
	_ = s.writeRequest(modules.RPCLoopExit, nil)
	close(s.closeChan)
}

// Close cleanly terminates the protocol session with the host and closes the
// connection.
func (s *Session) Close() error {
	// using once ensures that Close is idempotent
	s.once.Do(s.shutdown)
	return s.conn.Close()
}

// NewSession initiates the RPC loop with a host and returns a Session.
func (cs *ContractSet) NewSession(host modules.HostDBEntry, id types.FileContractID, currentHeight types.BlockHeight, hdb hostDB, cancel <-chan struct{}) (_ *Session, err error) {
	sc, ok := cs.Acquire(id)
	if !ok {
		return nil, errors.New("invalid contract")
	}
	defer cs.Return(sc)
	s, err := cs.managedNewSession(host, currentHeight, hdb, cancel)
	if err != nil {
		return nil, err
	}
	// Lock the contract and resynchronize if necessary
	rev, sigs, err := s.Lock(id, sc.header.SecretKey)
	if err != nil {
		s.Close()
		return nil, err
	} else if err := sc.syncRevision(rev, sigs); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// NewRawSession creates a new session unassociated with any contract.
func (cs *ContractSet) NewRawSession(host modules.HostDBEntry, currentHeight types.BlockHeight, hdb hostDB, cancel <-chan struct{}) (_ *Session, err error) {
	return cs.managedNewSession(host, currentHeight, hdb, cancel)
}

// managedNewSession initiates the RPC loop with a host and returns a Session.
func (cs *ContractSet) managedNewSession(host modules.HostDBEntry, currentHeight types.BlockHeight, hdb hostDB, cancel <-chan struct{}) (_ *Session, err error) {
	// Increase Successful/Failed interactions accordingly
	defer func() {
		if err != nil {
			hdb.IncrementFailedInteractions(host.PublicKey)
			err = errors.Extend(err, modules.ErrHostFault)
		} else {
			hdb.IncrementSuccessfulInteractions(host.PublicKey)
		}
	}()

	c, err := (&net.Dialer{
		Cancel:  cancel,
		Timeout: 45 * time.Second, // TODO: Constant
	}).Dial("tcp", string(host.NetAddress))
	if err != nil {
		return nil, err
	}
	conn := ratelimit.NewRLConn(c, cs.rl, cancel)

	closeChan := make(chan struct{})
	go func() {
		select {
		case <-cancel:
			conn.Close()
		case <-closeChan:
		}
	}()

	// Perform the handshake and create the session object.
	aead, challenge, err := performSessionHandshake(conn, host.PublicKey)
	if err != nil {
		conn.Close()
		return nil, errors.AddContext(err, "session handshake failed")
	}
	s := &Session{
		aead:        aead,
		challenge:   challenge.Challenge,
		closeChan:   closeChan,
		conn:        conn,
		contractSet: cs,
		deps:        cs.deps,
		hdb:         hdb,
		height:      currentHeight,
		host:        host,
	}

	return s, nil
}

// calculateProofRanges returns the proof ranges that should be used to verify a
// pre-modification Merkle diff proof for the specified actions.
func calculateProofRanges(actions []modules.LoopWriteAction, oldNumSectors uint64) []crypto.ProofRange {
	newNumSectors := oldNumSectors
	sectorsChanged := make(map[uint64]struct{})
	for _, action := range actions {
		switch action.Type {
		case modules.WriteActionAppend:
			sectorsChanged[newNumSectors] = struct{}{}
			newNumSectors++

		case modules.WriteActionTrim:
			newNumSectors--
			sectorsChanged[newNumSectors] = struct{}{}

		case modules.WriteActionSwap:
			sectorsChanged[action.A] = struct{}{}
			sectorsChanged[action.B] = struct{}{}

		case modules.WriteActionUpdate:
			panic("update not supported")
		}
	}

	oldRanges := make([]crypto.ProofRange, 0, len(sectorsChanged))
	for index := range sectorsChanged {
		if index < oldNumSectors {
			oldRanges = append(oldRanges, crypto.ProofRange{
				Start: index,
				End:   index + 1,
			})
		}
	}
	sort.Slice(oldRanges, func(i, j int) bool {
		return oldRanges[i].Start < oldRanges[j].Start
	})

	return oldRanges
}

// modifyProofRanges modifies the proof ranges produced by calculateProofRanges
// to verify a post-modification Merkle diff proof for the specified actions.
func modifyProofRanges(proofRanges []crypto.ProofRange, actions []modules.LoopWriteAction, numSectors uint64) []crypto.ProofRange {
	for _, action := range actions {
		switch action.Type {
		case modules.WriteActionAppend:
			proofRanges = append(proofRanges, crypto.ProofRange{
				Start: numSectors,
				End:   numSectors + 1,
			})
			numSectors++

		case modules.WriteActionTrim:
			proofRanges = proofRanges[:uint64(len(proofRanges))-action.A]
			numSectors--
		}
	}
	return proofRanges
}

// modifyLeaves modifies the leaf hashes of a Merkle diff proof to verify a
// post-modification Merkle diff proof for the specified actions.
func modifyLeaves(leafHashes []crypto.Hash, actions []modules.LoopWriteAction, numSectors uint64) []crypto.Hash {
	// determine which sector index corresponds to each leaf hash
	var indices []uint64
	for _, action := range actions {
		switch action.Type {
		case modules.WriteActionAppend:
			indices = append(indices, numSectors)
			numSectors++
		case modules.WriteActionTrim:
			for j := uint64(0); j < action.A; j++ {
				indices = append(indices, numSectors)
				numSectors--
			}
		case modules.WriteActionSwap:
			indices = append(indices, action.A, action.B)
		}
	}
	sort.Slice(indices, func(i, j int) bool {
		return indices[i] < indices[j]
	})
	indexMap := make(map[uint64]int, len(leafHashes))
	for i, index := range indices {
		if i > 0 && index == indices[i-1] {
			continue // remove duplicates
		}
		indexMap[index] = i
	}

	for _, action := range actions {
		switch action.Type {
		case modules.WriteActionAppend:
			leafHashes = append(leafHashes, crypto.MerkleRoot(action.Data))

		case modules.WriteActionTrim:
			leafHashes = leafHashes[:uint64(len(leafHashes))-action.A]

		case modules.WriteActionSwap:
			i, j := indexMap[action.A], indexMap[action.B]
			leafHashes[i], leafHashes[j] = leafHashes[j], leafHashes[i]

		case modules.WriteActionUpdate:
			panic("update not supported")
		}
	}
	return leafHashes
}
