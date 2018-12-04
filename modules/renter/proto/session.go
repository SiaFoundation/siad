package proto

import (
	"net"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/ratelimit"
)

// A Session is an ongoing exchange of RPCs via the renter-host protocol.
type Session struct {
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

// writeRequest sends an RPC request to the host.
func (s *Session) writeRequest(rpcID types.Specifier, req interface{}) error {
	if req == nil {
		req = struct{}{}
	}
	err := encoding.NewEncoder(s.conn).EncodeAll(rpcID, req)
	if err != nil {
		// The write may have failed because the host rejected our challenge
		// signature and closed the connection. Attempt to read the rejection
		// error and return it instead of the write failure.
		readErr := modules.ReadRPCResponse(s.conn, nil)
		if _, ok := readErr.(*modules.RPCError); ok {
			err = readErr
		}
	}
	return err
}

// Settings calls the Settings RPC, returning the host's reported settings.
func (s *Session) Settings() (modules.HostExternalSettings, error) {
	extendDeadline(s.conn, modules.NegotiateSettingsTime)

	// send Settings RPC request
	if err := s.writeRequest(modules.RPCLoopSettings, nil); err != nil {
		return modules.HostExternalSettings{}, err
	}

	// read the response
	var resp modules.LoopSettingsResponse
	if err := modules.ReadRPCResponse(s.conn, &resp); err != nil {
		return modules.HostExternalSettings{}, err
	}

	// verify signature
	hash := crypto.HashObject(resp.Settings)
	var pk crypto.PublicKey
	copy(pk[:], s.host.PublicKey.Key)
	var sig crypto.Signature
	copy(sig[:], resp.Signature)
	if crypto.VerifyHash(hash, pk, sig) != nil {
		return modules.HostExternalSettings{}, errors.New("host sent invalid signature")
	}

	s.host.HostExternalSettings = resp.Settings
	return resp.Settings, nil
}

// RecentRevision calls the RecentRevision RPC, returning (what the host
// claims is) the most recent revision of the contract.
func (s *Session) RecentRevision() (types.FileContractRevision, []types.TransactionSignature, error) {
	// Acquire the contract.
	sc, haveContract := s.contractSet.Acquire(s.contractID)
	if !haveContract {
		return types.FileContractRevision{}, nil, errors.New("contract not present in contract set")
	}
	defer s.contractSet.Return(sc)

	// send RecentRevision RPC request
	extendDeadline(s.conn, modules.NegotiateRecentRevisionTime)
	if err := s.writeRequest(modules.RPCLoopRecentRevision, nil); err != nil {
		return types.FileContractRevision{}, nil, err
	}

	// read the response
	var resp modules.LoopRecentRevisionResponse
	if err := modules.ReadRPCResponse(s.conn, &resp); err != nil {
		return types.FileContractRevision{}, nil, err
	}

	// Check that the unlock hashes match; if they do not, something is
	// seriously wrong. Otherwise, check that the revision numbers match.
	ourRev := sc.header.LastRevision()
	if resp.Revision.UnlockConditions.UnlockHash() != ourRev.UnlockConditions.UnlockHash() {
		return types.FileContractRevision{}, nil, errors.New("unlock conditions do not match")
	} else if resp.Revision.NewRevisionNumber != ourRev.NewRevisionNumber {
		// If the revision number doesn't match try to commit potential
		// unapplied transactions and check again.
		if err := sc.commitTxns(); err != nil {
			return types.FileContractRevision{}, nil, errors.AddContext(err, "failed to commit transactions")
		}
		ourRev = sc.header.LastRevision()
		if resp.Revision.NewRevisionNumber != ourRev.NewRevisionNumber {
			return types.FileContractRevision{}, nil, &recentRevisionError{ourRev.NewRevisionNumber, resp.Revision.NewRevisionNumber}
		}
	}

	return resp.Revision, resp.Signatures, nil
}

// Upload calls the Upload RPC and transfers the supplied data, returning the
// updated contract and the Merkle root of the sector.
func (s *Session) Upload(data []byte) (_ modules.RenterContract, _ crypto.Hash, err error) {
	// Acquire the contract.
	sc, haveContract := s.contractSet.Acquire(s.contractID)
	if !haveContract {
		return modules.RenterContract{}, crypto.Hash{}, errors.New("contract not present in contract set")
	}
	defer s.contractSet.Return(sc)
	contract := sc.header // for convenience

	// calculate price
	// TODO: height is never updated, so we'll wind up overpaying on long-running uploads
	blockBytes := types.NewCurrency64(modules.SectorSize * uint64(contract.LastRevision().NewWindowEnd-s.height))
	sectorStoragePrice := s.host.StoragePrice.Mul(blockBytes)
	sectorBandwidthPrice := s.host.UploadBandwidthPrice.Mul64(modules.SectorSize)
	sectorCollateral := s.host.Collateral.Mul(blockBytes)

	// to mitigate small errors (e.g. differing block heights), fudge the
	// price and collateral by 0.2%.
	sectorStoragePrice = sectorStoragePrice.MulFloat(1 + hostPriceLeeway)
	sectorBandwidthPrice = sectorBandwidthPrice.MulFloat(1 + hostPriceLeeway)
	sectorCollateral = sectorCollateral.MulFloat(1 - hostPriceLeeway)

	// check that enough funds are available
	sectorPrice := sectorStoragePrice.Add(sectorBandwidthPrice)
	if contract.RenterFunds().Cmp(sectorPrice) < 0 {
		return modules.RenterContract{}, crypto.Hash{}, errors.New("contract has insufficient funds to support upload")
	}
	if contract.LastRevision().NewMissedProofOutputs[1].Value.Cmp(sectorCollateral) < 0 {
		return modules.RenterContract{}, crypto.Hash{}, errors.New("contract has insufficient collateral to support upload")
	}

	// calculate the new Merkle root
	sectorRoot := crypto.MerkleRoot(data)
	merkleRoot := sc.merkleRoots.checkNewRoot(sectorRoot)

	// create the revision and sign it
	rev := newUploadRevision(contract.LastRevision(), merkleRoot, sectorPrice, sectorCollateral)
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

	// create the request
	req := modules.LoopUploadRequest{
		Data:              data,
		NewRevisionNumber: rev.NewRevisionNumber,
		Signature:         sig[:],
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
	walTxn, err := sc.recordUploadIntent(rev, sectorRoot, sectorStoragePrice, sectorBandwidthPrice)
	if err != nil {
		return modules.RenterContract{}, crypto.Hash{}, err
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
		return modules.RenterContract{}, crypto.Hash{}, errors.New("InterruptUploadBeforeSendingRevision disrupt")
	}

	// send upload RPC request
	extendDeadline(s.conn, modules.NegotiateFileContractRevisionTime)
	err = s.writeRequest(modules.RPCLoopUpload, req)
	if err != nil {
		return modules.RenterContract{}, crypto.Hash{}, err
	}

	// read the response
	var resp modules.LoopUploadResponse
	err = modules.ReadRPCResponse(s.conn, &resp)
	if err != nil {
		return modules.RenterContract{}, crypto.Hash{}, err
	}

	// Disrupt here before updating the contract.
	if s.deps.Disrupt("InterruptUploadAfterSendingRevision") {
		return modules.RenterContract{}, crypto.Hash{}, errors.New("InterruptUploadAfterSendingRevision disrupt")
	}

	// add host signature
	txn.TransactionSignatures[1].Signature = resp.Signature

	// update contract
	err = sc.commitUpload(walTxn, txn, sectorRoot, sectorStoragePrice, sectorBandwidthPrice)
	if err != nil {
		return modules.RenterContract{}, crypto.Hash{}, err
	}

	return sc.Metadata(), sectorRoot, nil
}

// Download calls the download RPC and returns the requested data. A Merkle
// proof is always requested.
func (s *Session) Download(root crypto.Hash, offset, length uint32) (_ modules.RenterContract, _ []byte, err error) {
	// Reset deadline when finished.
	defer extendDeadline(s.conn, time.Hour)

	// Sanity-check the request.
	if offset%crypto.SegmentSize != 0 || length%crypto.SegmentSize != 0 {
		return modules.RenterContract{}, nil, errors.New("offset and length must be multiples of SegmentSize when requesting a Merkle proof")
	}

	// Acquire the contract.
	sc, haveContract := s.contractSet.Acquire(s.contractID)
	if !haveContract {
		return modules.RenterContract{}, nil, errors.New("contract not present in contract set")
	}
	defer s.contractSet.Return(sc)
	contract := sc.header // for convenience

	// calculate price
	price := s.host.DownloadBandwidthPrice.Mul64(uint64(length))
	if contract.RenterFunds().Cmp(price) < 0 {
		return modules.RenterContract{}, nil, errors.New("contract has insufficient funds to support download")
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

	// create the request object
	req := modules.LoopDownloadRequest{
		MerkleRoot:  root,
		Offset:      offset,
		Length:      length,
		MerkleProof: true,
	}
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

	// Disrupt before sending the signed revision to the host.
	if s.deps.Disrupt("InterruptDownloadBeforeSendingRevision") {
		return modules.RenterContract{}, nil, errors.New("InterruptDownloadBeforeSendingRevision disrupt")
	}

	// send download RPC request
	extendDeadline(s.conn, modules.NegotiateDownloadTime)
	err = s.writeRequest(modules.RPCLoopDownload, req)
	if err != nil {
		return modules.RenterContract{}, nil, err
	}

	// read the response
	var resp modules.LoopDownloadResponse
	err = modules.ReadRPCResponse(s.conn, &resp)
	if err != nil {
		return modules.RenterContract{}, nil, err
	}

	if len(resp.Data) != int(req.Length) {
		return modules.RenterContract{}, nil, errors.New("host did not send enough sector data")
	} else if req.MerkleProof {
		proofStart := int(req.Offset) / crypto.SegmentSize
		proofEnd := int(req.Offset+req.Length) / crypto.SegmentSize
		if !crypto.VerifyRangeProof(resp.Data, resp.MerkleProof, proofStart, proofEnd, req.MerkleRoot) {
			return modules.RenterContract{}, nil, errors.New("host provided incorrect sector data or Merkle proof")
		}
	} else if crypto.MerkleRoot(resp.Data) != req.MerkleRoot {
		return modules.RenterContract{}, nil, errors.New("host provided incorrect sector data")
	}

	// Disrupt after sending the signed revision to the host.
	if s.deps.Disrupt("InterruptDownloadAfterSendingRevision") {
		return modules.RenterContract{}, nil, errors.New("InterruptDownloadAfterSendingRevision disrupt")
	}

	// add host signature
	txn.TransactionSignatures[1].Signature = resp.Signature

	// update contract and metrics
	if err := sc.commitDownload(walTxn, txn, price); err != nil {
		return modules.RenterContract{}, nil, err
	}

	return sc.Metadata(), resp.Data, nil
}

// SectorRoots calls the contract roots download RPC and returns the requested sector roots. The
// Revision and Signature fields of req are filled in automatically. If a
// Merkle proof is requested, it is verified.
func (s *Session) SectorRoots(req modules.LoopSectorRootsRequest) (_ modules.RenterContract, _ []crypto.Hash, err error) {
	build.Critical("Merkle proofs not implemented")

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
	paidBytes := uint64(req.NumRoots) * crypto.HashSize
	price := s.host.DownloadBandwidthPrice.Mul64(paidBytes)
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
	err = encoding.NewEncoder(s.conn).EncodeAll(modules.RPCLoopSectorRoots, req)
	if err != nil {
		return modules.RenterContract{}, nil, err
	}

	// read the response
	var resp modules.LoopSectorRootsResponse
	err = modules.ReadRPCResponse(s.conn, &resp)
	if err != nil {
		return modules.RenterContract{}, nil, err
	}

	if len(resp.SectorRoots) != int(req.NumRoots) {
		return modules.RenterContract{}, nil, errors.New("host did not send the requested number of sector roots")
	}

	// add host signature
	txn.TransactionSignatures[1].Signature = resp.Signature

	// update contract and metrics
	if err := sc.commitDownload(walTxn, txn, price); err != nil {
		return modules.RenterContract{}, nil, err
	}

	return sc.Metadata(), resp.SectorRoots, nil
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
	contract := sc.header

	// Increase Successful/Failed interactions accordingly
	defer func() {
		if err != nil {
			hdb.IncrementFailedInteractions(contract.HostPublicKey())
			err = errors.Extend(err, modules.ErrHostFault)
		} else {
			hdb.IncrementSuccessfulInteractions(contract.HostPublicKey())
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

	extendDeadline(conn, modules.NegotiateSettingsTime)
	if err := encoding.WriteObject(conn, modules.RPCLoopEnter); err != nil {
		return nil, err
	}

	// perform initial handshake
	req := modules.LoopHandshakeRequest{
		Version:    1,
		Ciphers:    []types.Specifier{modules.CipherPlaintext},
		KeyData:    nil,
		ContractID: id,
	}
	if err := encoding.NewEncoder(conn).Encode(req); err != nil {
		return nil, err
	}
	var resp modules.LoopHandshakeResponse
	if err := modules.ReadRPCResponse(conn, &resp); err != nil {
		return nil, err
	}
	if resp.Cipher != modules.CipherPlaintext {
		return nil, errors.New("host selected unsupported cipher")
	}
	// respond to challenge
	hash := crypto.HashAll(modules.RPCChallengePrefix, resp.Challenge)
	sig := crypto.SignHash(hash, contract.SecretKey)
	cresp := modules.LoopChallengeResponse{
		Signature: sig[:],
	}
	if err := encoding.NewEncoder(conn).Encode(cresp); err != nil {
		return nil, err
	}

	// the host is now ready to accept revisions
	return &Session{
		closeChan:   closeChan,
		conn:        conn,
		contractID:  id,
		contractSet: cs,
		deps:        cs.deps,
		hdb:         hdb,
		height:      currentHeight,
		host:        host,
	}, nil
}
