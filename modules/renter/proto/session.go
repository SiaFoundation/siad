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
	err := modules.WriteRPCRequest(s.conn, s.aead, rpcID, req)
	if err != nil {
		// The write may have failed because the host rejected our challenge
		// signature and closed the connection. Attempt to read the rejection
		// error and return it instead of the write failure.
		readErr := s.readResponse(nil)
		if _, ok := readErr.(*modules.RPCError); ok {
			err = readErr
		}
	}
	return err
}

// readResponse reads an encrypted RPC response from the host.
func (s *Session) readResponse(resp interface{}) error {
	return modules.ReadRPCResponse(s.conn, s.aead, resp, 1e6)
}

// call is a helper method that calls writeRequest followed by readResponse.
func (s *Session) call(rpcID types.Specifier, req, resp interface{}) error {
	if err := s.writeRequest(rpcID, req); err != nil {
		return err
	}
	return s.readResponse(resp)
}

// Settings calls the Settings RPC, returning the host's reported settings.
func (s *Session) Settings() (modules.HostExternalSettings, error) {
	extendDeadline(s.conn, modules.NegotiateSettingsTime)
	var resp modules.LoopSettingsResponse
	if err := s.call(modules.RPCLoopSettings, nil, &resp); err != nil {
		return modules.HostExternalSettings{}, err
	}
	s.host.HostExternalSettings = resp.Settings
	return resp.Settings, nil
}

// VerifyRecentRevision calls the RecentRevision RPC, returning the most recent
// revision known to the host if it matches the one we have stored locally.
// Otherwise an error is returned.
func (s *Session) VerifyRecentRevision() (types.FileContractRevision, []types.TransactionSignature, error) {
	// Get the recent revision from the host.
	rev, sigs, err := s.RecentRevision()
	if err != nil {
		return types.FileContractRevision{}, nil, err
	}

	// Acquire the contract.
	sc, haveContract := s.contractSet.Acquire(s.contractID)
	if !haveContract {
		return types.FileContractRevision{}, nil, errors.New("contract not present in contract set")
	}
	defer s.contractSet.Return(sc)

	// Check that the unlock hashes match; if they do not, something is
	// seriously wrong. Otherwise, check that the revision numbers match.
	ourRev := sc.header.LastRevision()
	if rev.UnlockConditions.UnlockHash() != ourRev.UnlockConditions.UnlockHash() {
		return types.FileContractRevision{}, nil, errors.New("unlock conditions do not match")
	} else if rev.NewRevisionNumber != ourRev.NewRevisionNumber {
		// If the revision number doesn't match try to commit potential
		// unapplied transactions and check again.
		if err := sc.commitTxns(); err != nil {
			return types.FileContractRevision{}, nil, errors.AddContext(err, "failed to commit transactions")
		}
		ourRev = sc.header.LastRevision()
		if rev.NewRevisionNumber != ourRev.NewRevisionNumber {
			return types.FileContractRevision{}, nil, &recentRevisionError{ourRev.NewRevisionNumber, rev.NewRevisionNumber}
		}
	}

	return rev, sigs, nil
}

// RecentRevision calls the RecentRevision RPC, returning (what the host
// claims is) the most recent revision of the contract.
func (s *Session) RecentRevision() (types.FileContractRevision, []types.TransactionSignature, error) {
	extendDeadline(s.conn, modules.NegotiateRecentRevisionTime)
	var resp modules.LoopRecentRevisionResponse
	if err := s.call(modules.RPCLoopRecentRevision, nil, &resp); err != nil {
		return types.FileContractRevision{}, nil, err
	}
	// Create the revision transaction.
	revTxn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{resp.Revision},
		TransactionSignatures: resp.Signatures,
	}
	// Verify the signature on the revision before we use it to recover the
	// roots.
	var hpk crypto.PublicKey
	var sig crypto.Signature
	for i, signature := range revTxn.TransactionSignatures {
		copy(hpk[:], resp.Revision.UnlockConditions.PublicKeys[i].Key)
		copy(sig[:], signature.Signature)
		err := crypto.VerifyHash(revTxn.SigHash(i, s.height), hpk, sig)
		if err != nil {
			return types.FileContractRevision{}, nil, err
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
	var resp modules.LoopUploadResponse
	err = s.call(modules.RPCLoopUpload, req, &resp)
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
	var resp modules.LoopDownloadResponse
	err = s.call(modules.RPCLoopDownload, req, &resp)
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
	err = s.call(modules.RPCLoopSectorRoots, req, &resp)
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
	err = s.call(modules.RPCLoopSectorRoots, req, &resp)
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

	// Verify the signatures on the revision txn before we use it to recover
	// the roots.
	var vHpk crypto.PublicKey
	var vSig crypto.Signature
	for i, signature := range txn.TransactionSignatures {
		copy(vHpk[:], rev.UnlockConditions.PublicKeys[i].Key)
		copy(vSig[:], signature.Signature)
		err := crypto.VerifyHash(txn.SigHash(i, s.height), vHpk, vSig)
		if err != nil {
			return types.Transaction{}, []crypto.Hash{}, err
		}
	}
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
	return cs.managedNewSession(host, id, currentHeight, hdb, sc.header.SecretKey, cancel)
}

// NewSessionWithSecret creates a new session using a contract's secret key
// directly instead of fetching it from the set of known contracts.
func (cs *ContractSet) NewSessionWithSecret(host modules.HostDBEntry, id types.FileContractID, currentHeight types.BlockHeight, hdb hostDB, sk crypto.SecretKey, cancel <-chan struct{}) (_ *Session, err error) {
	return cs.managedNewSession(host, id, currentHeight, hdb, sk, cancel)
}

// managedNewSession initiates the RPC loop with a host and returns a Session.
func (cs *ContractSet) managedNewSession(host modules.HostDBEntry, id types.FileContractID, currentHeight types.BlockHeight, hdb hostDB, sk crypto.SecretKey, cancel <-chan struct{}) (_ *Session, err error) {
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

	aead, err := performSessionHandshake(conn, host.PublicKey, id, sk)
	if err != nil {
		return nil, err
	}

	// the host is now ready to accept revisions
	return &Session{
		aead:        aead,
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
