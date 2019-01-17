package proto

import (
	"crypto/cipher"
	"net"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/ratelimit"
)

// A Session is an ongoing exchange of RPCs via the renter-host protocol.
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
	}

	extendDeadline(s.conn, modules.NegotiateSettingsTime) // TODO: should account for lock time
	var resp modules.LoopLockResponse
	if err := s.call(modules.RPCLoopLock, req, &resp, modules.RPCMinLen); err != nil {
		return types.FileContractRevision{}, nil, err
	}

	// Set the Session contract and update the challenge.
	s.contractID = id
	s.challenge = resp.NewChallenge

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
	s.host.HostExternalSettings = resp.Settings
	return resp.Settings, nil
}

// Write calls the Write RPC and transfers the supplied data, returning the
// updated contract and the Merkle root of the sector.
func (s *Session) Write(data []byte) (_ modules.RenterContract, _ crypto.Hash, err error) {
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
	req := modules.LoopWriteRequest{
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
	var resp modules.LoopWriteResponse
	err = s.call(modules.RPCLoopWrite, req, &resp, modules.RPCMinLen)
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

// Read calls the Read RPC and returns the requested data. A Merkle proof is
// always requested.
func (s *Session) Read(root crypto.Hash, offset, length uint32) (_ modules.RenterContract, _ []byte, err error) {
	// Reset deadline when finished.
	defer extendDeadline(s.conn, time.Hour)

	// Sanity-check the request.
	if uint64(offset)+uint64(length) > modules.SectorSize {
		return modules.RenterContract{}, nil, errors.New("illegal offset and/or length")
	} else if offset%crypto.SegmentSize != 0 || length%crypto.SegmentSize != 0 {
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
	sec := modules.LoopReadRequestSection{
		MerkleRoot: root,
		Offset:     offset,
		Length:     length,
	}
	req := modules.LoopReadRequest{
		Sections:    []modules.LoopReadRequestSection{sec},
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
	var resp modules.LoopReadResponse
	err = s.call(modules.RPCLoopRead, req, &resp, modules.RPCMinLen+uint64(sec.Length))
	if err != nil {
		return modules.RenterContract{}, nil, err
	}

	if len(resp.Data) != int(sec.Length) {
		return modules.RenterContract{}, nil, errors.New("host did not send enough sector data")
	} else if req.MerkleProof {
		proofStart := int(sec.Offset) / crypto.SegmentSize
		proofEnd := int(sec.Offset+sec.Length) / crypto.SegmentSize
		if !crypto.VerifyRangeProof(resp.Data, resp.MerkleProof, proofStart, proofEnd, sec.MerkleRoot) {
			return modules.RenterContract{}, nil, errors.New("host provided incorrect sector data or Merkle proof")
		}
	} else if crypto.MerkleRoot(resp.Data) != sec.MerkleRoot {
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
	paidBytes := uint64(req.NumRoots) * crypto.HashSize
	price := s.host.DownloadBandwidthPrice.Mul64(paidBytes)
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
	err = s.call(modules.RPCLoopSectorRoots, req, &resp, sectorRootsRespMaxLen+(req.NumRoots*32))
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
	rev, _, err := s.Lock(id, sc.header.SecretKey)
	if err != nil {
		s.Close()
		return nil, err
	} else if err := sc.syncRevision(rev); err != nil {
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
		return nil, err
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
