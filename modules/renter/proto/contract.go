package proto

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

const (
	// contractHeaderSize is the maximum amount of space that the non-Merkle-root
	// portion of a contract can consume.
	contractHeaderSize = writeaheadlog.MaxPayloadSize // TODO: test this

	updateNameSetHeader = "setHeader"
	updateNameSetRoot   = "setRoot"
)

type updateSetHeader struct {
	ID     types.FileContractID
	Header contractHeader
}

type updateSetRoot struct {
	ID    types.FileContractID
	Root  crypto.Hash
	Index int
}

type contractHeader struct {
	// transaction is the signed transaction containing the most recent
	// revision of the file contract.
	Transaction types.Transaction

	// secretKey is the key used by the renter to sign the file contract
	// transaction.
	SecretKey crypto.SecretKey

	// Same as modules.RenterContract.
	StartHeight      types.BlockHeight
	DownloadSpending types.Currency
	StorageSpending  types.Currency
	UploadSpending   types.Currency
	TotalCost        types.Currency
	ContractFee      types.Currency
	TxnFee           types.Currency
	SiafundFee       types.Currency
	Utility          modules.ContractUtility
}

// validate returns an error if the contractHeader is invalid.
func (h *contractHeader) validate() error {
	if len(h.Transaction.FileContractRevisions) == 0 {
		return errors.New("no file contract revisions")
	}
	if len(h.Transaction.FileContractRevisions[0].NewValidProofOutputs) == 0 {
		return errors.New("not enough valid proof outputs")
	}
	if len(h.Transaction.FileContractRevisions[0].UnlockConditions.PublicKeys) != 2 {
		return errors.New("wrong number of pubkeys")
	}
	return nil
}

func (h *contractHeader) copyTransaction() (txn types.Transaction) {
	encoding.Unmarshal(encoding.Marshal(h.Transaction), &txn)
	return
}

func (h *contractHeader) LastRevision() types.FileContractRevision {
	return h.Transaction.FileContractRevisions[0]
}

func (h *contractHeader) ID() types.FileContractID {
	return h.LastRevision().ID()
}

func (h *contractHeader) HostPublicKey() types.SiaPublicKey {
	return h.LastRevision().HostPublicKey()
}

func (h *contractHeader) RenterFunds() types.Currency {
	return h.LastRevision().RenterFunds()
}

func (h *contractHeader) EndHeight() types.BlockHeight {
	return h.LastRevision().EndHeight()
}

// A SafeContract contains the most recent revision transaction negotiated
// with a host, and the secret key used to sign it.
type SafeContract struct {
	header contractHeader

	// merkleRoots are the sector roots covered by this contract.
	merkleRoots *merkleRoots

	// unappliedTxns are the transactions that were written to the WAL but not
	// applied to the contract file.
	unappliedTxns []*writeaheadlog.Transaction

	headerFile *fileSection
	wal        *writeaheadlog.WAL
	mu         sync.Mutex

	// revisionMu serializes revisions to the contract. It is acquired by
	// (ContractSet).Acquire and released by (ContractSet).Return. When holding
	// revisionMu, it is still necessary to lock mu when modifying fields of the
	// SafeContract.
	revisionMu sync.Mutex
}

// Metadata returns the metadata of a renter contract
func (c *SafeContract) Metadata() modules.RenterContract {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := c.header
	return modules.RenterContract{
		ID:               h.ID(),
		Transaction:      h.copyTransaction(),
		HostPublicKey:    h.HostPublicKey(),
		StartHeight:      h.StartHeight,
		EndHeight:        h.EndHeight(),
		RenterFunds:      h.RenterFunds(),
		DownloadSpending: h.DownloadSpending,
		StorageSpending:  h.StorageSpending,
		UploadSpending:   h.UploadSpending,
		TotalCost:        h.TotalCost,
		ContractFee:      h.ContractFee,
		TxnFee:           h.TxnFee,
		SiafundFee:       h.SiafundFee,
		Utility:          h.Utility,
	}
}

// UpdateUtility updates the utility field of a contract.
func (c *SafeContract) UpdateUtility(utility modules.ContractUtility) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Construct new header
	newHeader := c.header
	newHeader.Utility = utility

	// Record the intent to change the header in the wal.
	t, err := c.wal.NewTransaction([]writeaheadlog.Update{
		c.makeUpdateSetHeader(newHeader),
	})
	if err != nil {
		return err
	}
	// Signal that the setup is completed.
	if err := <-t.SignalSetupComplete(); err != nil {
		return err
	}
	// Apply the change.
	if err := c.applySetHeader(newHeader); err != nil {
		return err
	}
	// Sync the change to disk.
	if err := c.headerFile.Sync(); err != nil {
		return err
	}
	// Signal that the update has been applied.
	if err := t.SignalUpdatesApplied(); err != nil {
		return err
	}
	return nil
}

// Utility returns the contract utility for the contract.
func (c *SafeContract) Utility() modules.ContractUtility {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.header.Utility
}

func (c *SafeContract) makeUpdateSetHeader(h contractHeader) writeaheadlog.Update {
	id := c.header.ID()
	return writeaheadlog.Update{
		Name: updateNameSetHeader,
		Instructions: encoding.Marshal(updateSetHeader{
			ID:     id,
			Header: h,
		}),
	}
}

func (c *SafeContract) makeUpdateSetRoot(root crypto.Hash, index int) writeaheadlog.Update {
	id := c.header.ID()
	return writeaheadlog.Update{
		Name: updateNameSetRoot,
		Instructions: encoding.Marshal(updateSetRoot{
			ID:    id,
			Root:  root,
			Index: index,
		}),
	}
}

func (c *SafeContract) applySetHeader(h contractHeader) error {
	if build.DEBUG {
		// read the existing header on disk, to make sure we aren't overwriting
		// it with an older revision
		var oldHeader contractHeader
		headerBytes := make([]byte, contractHeaderSize)
		if _, err := c.headerFile.ReadAt(headerBytes, 0); err == nil {
			if err := encoding.Unmarshal(headerBytes, &oldHeader); err == nil {
				if oldHeader.LastRevision().NewRevisionNumber > h.LastRevision().NewRevisionNumber {
					build.Critical("overwriting a newer revision:", oldHeader.LastRevision().NewRevisionNumber, h.LastRevision().NewRevisionNumber)
				}
			}
		}
	}

	headerBytes := make([]byte, contractHeaderSize)
	copy(headerBytes, encoding.Marshal(h))
	if _, err := c.headerFile.WriteAt(headerBytes, 0); err != nil {
		return err
	}
	c.header = h
	return nil
}

func (c *SafeContract) applySetRoot(root crypto.Hash, index int) error {
	return c.merkleRoots.insert(index, root)
}

func (c *SafeContract) managedRecordUploadIntent(rev types.FileContractRevision, root crypto.Hash, storageCost, bandwidthCost types.Currency) (*writeaheadlog.Transaction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// construct new header
	// NOTE: this header will not include the host signature
	newHeader := c.header
	newHeader.Transaction.FileContractRevisions = []types.FileContractRevision{rev}
	newHeader.Transaction.TransactionSignatures = nil
	newHeader.StorageSpending = newHeader.StorageSpending.Add(storageCost)
	newHeader.UploadSpending = newHeader.UploadSpending.Add(bandwidthCost)

	t, err := c.wal.NewTransaction([]writeaheadlog.Update{
		c.makeUpdateSetHeader(newHeader),
		c.makeUpdateSetRoot(root, c.merkleRoots.len()),
	})
	if err != nil {
		return nil, err
	}
	if err := <-t.SignalSetupComplete(); err != nil {
		return nil, err
	}
	c.unappliedTxns = append(c.unappliedTxns, t)
	return t, nil
}

func (c *SafeContract) managedCommitUpload(t *writeaheadlog.Transaction, signedTxn types.Transaction, root crypto.Hash, storageCost, bandwidthCost types.Currency) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// construct new header
	newHeader := c.header
	newHeader.Transaction = signedTxn
	newHeader.StorageSpending = newHeader.StorageSpending.Add(storageCost)
	newHeader.UploadSpending = newHeader.UploadSpending.Add(bandwidthCost)

	if err := c.applySetHeader(newHeader); err != nil {
		return err
	}
	if err := c.applySetRoot(root, c.merkleRoots.len()); err != nil {
		return err
	}
	if err := c.headerFile.Sync(); err != nil {
		return err
	}
	if err := t.SignalUpdatesApplied(); err != nil {
		return err
	}
	c.unappliedTxns = nil
	return nil
}

func (c *SafeContract) managedRecordDownloadIntent(rev types.FileContractRevision, bandwidthCost types.Currency) (*writeaheadlog.Transaction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// construct new header
	// NOTE: this header will not include the host signature
	newHeader := c.header
	newHeader.Transaction.FileContractRevisions = []types.FileContractRevision{rev}
	newHeader.Transaction.TransactionSignatures = nil
	newHeader.DownloadSpending = newHeader.DownloadSpending.Add(bandwidthCost)

	t, err := c.wal.NewTransaction([]writeaheadlog.Update{
		c.makeUpdateSetHeader(newHeader),
	})
	if err != nil {
		return nil, err
	}
	if err := <-t.SignalSetupComplete(); err != nil {
		return nil, err
	}
	c.unappliedTxns = append(c.unappliedTxns, t)
	return t, nil
}

func (c *SafeContract) managedCommitDownload(t *writeaheadlog.Transaction, signedTxn types.Transaction, bandwidthCost types.Currency) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// construct new header
	newHeader := c.header
	newHeader.Transaction = signedTxn
	newHeader.DownloadSpending = newHeader.DownloadSpending.Add(bandwidthCost)

	if err := c.applySetHeader(newHeader); err != nil {
		return err
	}
	if err := c.headerFile.Sync(); err != nil {
		return err
	}
	if err := t.SignalUpdatesApplied(); err != nil {
		return err
	}
	c.unappliedTxns = nil
	return nil
}

// managedCommitTxns commits the unapplied transactions to the contract file and marks
// the transactions as applied.
func (c *SafeContract) managedCommitTxns() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.unappliedTxns {
		for _, update := range t.Updates {
			switch update.Name {
			case updateNameSetHeader:
				var u updateSetHeader
				if err := unmarshalHeader(update.Instructions, &u); err != nil {
					return err
				}
				if err := c.applySetHeader(u.Header); err != nil {
					return err
				}
			case updateNameSetRoot:
				var u updateSetRoot
				if err := encoding.Unmarshal(update.Instructions, &u); err != nil {
					return err
				}
				if err := c.applySetRoot(u.Root, u.Index); err != nil {
					return err
				}
			}
		}
		if err := c.headerFile.Sync(); err != nil {
			return err
		}
		if err := t.SignalUpdatesApplied(); err != nil {
			return err
		}
	}
	c.unappliedTxns = nil
	return nil
}

// managedSyncRevision checks whether rev accords with the SafeContract's most
// recent revision; if it does not, managedSyncRevision attempts to synchronize
// with rev by committing any uncommitted WAL transactions. If the revisions
// still do not match, and the host's revision is ahead of the renter's,
// managedSyncRevision uses the host's revision.
func (c *SafeContract) managedSyncRevision(rev types.FileContractRevision, sigs []types.TransactionSignature) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Our current revision should always be signed. If it isn't, we have no
	// choice but to accept the host's revision.
	if len(c.header.Transaction.TransactionSignatures) == 0 {
		c.header.Transaction.FileContractRevisions[0] = rev
		c.header.Transaction.TransactionSignatures = sigs
		return nil
	}

	ourRev := c.header.LastRevision()

	// If the revision number and Merkle root match, we don't need to do anything.
	if rev.NewRevisionNumber == ourRev.NewRevisionNumber && rev.NewFileMerkleRoot == ourRev.NewFileMerkleRoot {
		// If any other fields mismatch, it must be our fault, since we signed
		// the revision reported by the host. So, to ensure things are
		// consistent, we blindly overwrite our revision with the host's.
		c.header.Transaction.FileContractRevisions[0] = rev
		c.header.Transaction.TransactionSignatures = sigs
		return nil
	}

	// The host should never report a lower revision number than ours. If they
	// do, it may mean they are intentionally (and maliciously) trying to
	// "rewind" the contract to an earlier state. Even if the host does not have
	// ill intent, this would mean that they failed to commit one or more
	// revisions to durable storage, which reflects very poorly on them.
	if rev.NewRevisionNumber < ourRev.NewRevisionNumber {
		return &revisionNumberMismatchError{ourRev.NewRevisionNumber, rev.NewRevisionNumber}
	}

	// At this point, we know that either the host's revision number is above
	// ours, or their Merkle root differs. Search our unapplied WAL transactions
	// for one that might synchronize us with the host.
	for _, t := range c.unappliedTxns {
		for _, update := range t.Updates {
			if update.Name == updateNameSetHeader {
				var u updateSetHeader
				if err := unmarshalHeader(update.Instructions, &u); err != nil {
					return err
				}
				unappliedRev := u.Header.LastRevision()
				if unappliedRev.NewRevisionNumber != rev.NewRevisionNumber || unappliedRev.NewFileMerkleRoot != rev.NewFileMerkleRoot {
					continue
				}
				// found a matching header, but it still won't have the host's
				// signatures, since those aren't added until the transaction is
				// committed. Add the signatures supplied by the host and commit
				// the header.
				u.Header.Transaction.TransactionSignatures = sigs
				if err := c.applySetHeader(u.Header); err != nil {
					return err
				}
				if err := c.headerFile.Sync(); err != nil {
					return err
				}
				// drop all unapplied transactions
				for _, t := range c.unappliedTxns {
					if err := t.SignalUpdatesApplied(); err != nil {
						return err
					}
				}
				c.unappliedTxns = nil
				return nil
			}
		}
	}

	// The host's revision is still different, and we have no unapplied
	// transactions containing their revision. At this point, the best we can do
	// is accept their revision. This isn't ideal, but at least there's no
	// security risk, since we *did* sign the revision that the host is
	// claiming. Worst case, certain contract metadata (e.g. UploadSpending)
	// will be incorrect.
	c.header.Transaction.FileContractRevisions[0] = rev
	c.header.Transaction.TransactionSignatures = sigs
	// Drop the WAL transactions, since they can't conceivably help us.
	for _, t := range c.unappliedTxns {
		if err := t.SignalUpdatesApplied(); err != nil {
			return err
		}
	}
	c.unappliedTxns = nil
	return nil
}

func (cs *ContractSet) managedInsertContract(h contractHeader, roots []crypto.Hash) (modules.RenterContract, error) {
	if err := h.validate(); err != nil {
		return modules.RenterContract{}, err
	}
	f, err := os.Create(filepath.Join(cs.dir, h.ID().String()+contractExtension))
	if err != nil {
		return modules.RenterContract{}, err
	}
	// create fileSections
	headerSection := newFileSection(f, 0, contractHeaderSize)
	rootsSection := newFileSection(f, contractHeaderSize, -1)
	// write header
	if _, err := headerSection.WriteAt(encoding.Marshal(h), 0); err != nil {
		return modules.RenterContract{}, err
	}
	// write roots
	merkleRoots := newMerkleRoots(rootsSection)
	for _, root := range roots {
		if err := merkleRoots.push(root); err != nil {
			return modules.RenterContract{}, err
		}
	}
	if err := f.Sync(); err != nil {
		return modules.RenterContract{}, err
	}
	sc := &SafeContract{
		header:      h,
		merkleRoots: merkleRoots,
		headerFile:  headerSection,
		wal:         cs.wal,
	}
	cs.mu.Lock()
	cs.contracts[sc.header.ID()] = sc
	cs.pubKeys[h.HostPublicKey().String()] = sc.header.ID()
	cs.mu.Unlock()
	return sc.Metadata(), nil
}

// loadSafeContract loads a contract from disk and adds it to the contractset
// if it is valid.
func (cs *ContractSet) loadSafeContract(filename string, walTxns []*writeaheadlog.Transaction) (err error) {
	f, err := os.OpenFile(filename, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
		}
	}()
	stat, err := f.Stat()
	if err != nil {
		return err
	}

	headerSection := newFileSection(f, 0, contractHeaderSize)
	rootsSection := newFileSection(f, contractHeaderSize, remainingFile)

	// read header
	var header contractHeader
	decodeMaxSize := int(stat.Size() * 3)
	err = encoding.NewDecoder(f, decodeMaxSize).Decode(&header)
	if err != nil {
		// Unable to decode the old header, try a new decode. Seek the file back
		// to the beginning.
		var v1412DecodeErr error
		_, seekErr := f.Seek(0, 0)
		if seekErr != nil {
			return errors.AddContext(errors.Compose(err, seekErr), "unable to reset file when attempting legacy decode")
		}
		header, v1412DecodeErr = contractHeaderDecodeV1412ToV1413(f, decodeMaxSize)
		if v1412DecodeErr != nil {
			return errors.AddContext(errors.Compose(err, v1412DecodeErr), "unable to decode contract header")
		}
	}
	if err := header.validate(); err != nil {
		return errors.AddContext(err, "unable to validate contract header")
	}

	// read merkleRoots
	merkleRoots, applyTxns, err := loadExistingMerkleRoots(rootsSection)
	if err != nil {
		return errors.AddContext(err, "unable to load the merkle roots of the contract")
	}
	// add relevant unapplied transactions
	var unappliedTxns []*writeaheadlog.Transaction
	for _, t := range walTxns {
		// NOTE: we assume here that if any of the updates apply to the
		// contract, the whole transaction applies to the contract.
		if len(t.Updates) == 0 {
			continue
		}
		var id types.FileContractID
		switch update := t.Updates[0]; update.Name {
		case updateNameSetHeader:
			var u updateSetHeader
			if err := unmarshalHeader(update.Instructions, &u); err != nil {
				return errors.AddContext(err, "unable to unmarshal the contract header during wal txn recovery")
			}
			id = u.ID
		case updateNameSetRoot:
			var u updateSetRoot
			if err := encoding.Unmarshal(update.Instructions, &u); err != nil {
				return errors.AddContext(err, "unable to unmarshal the update root set during wal txn recovery")
			}
			id = u.ID
		}
		if id == header.ID() {
			unappliedTxns = append(unappliedTxns, t)
		}
	}
	// add to set
	sc := &SafeContract{
		header:        header,
		merkleRoots:   merkleRoots,
		unappliedTxns: unappliedTxns,
		headerFile:    headerSection,
		wal:           cs.wal,
	}

	// apply the wal txns if necessary.
	if applyTxns {
		if err := sc.managedCommitTxns(); err != nil {
			return errors.AddContext(err, "unable to commit the wal transactions during contractset recovery")
		}
	}
	cs.contracts[sc.header.ID()] = sc
	cs.pubKeys[header.HostPublicKey().String()] = sc.header.ID()
	return nil
}

// ConvertV130Contract creates a contract file for a v130 contract.
func (cs *ContractSet) ConvertV130Contract(c V130Contract, cr V130CachedRevision) error {
	m, err := cs.managedInsertContract(contractHeader{
		Transaction:      c.LastRevisionTxn,
		SecretKey:        c.SecretKey,
		StartHeight:      c.StartHeight,
		DownloadSpending: c.DownloadSpending,
		StorageSpending:  c.StorageSpending,
		UploadSpending:   c.UploadSpending,
		TotalCost:        c.TotalCost,
		ContractFee:      c.ContractFee,
		TxnFee:           c.TxnFee,
		SiafundFee:       c.SiafundFee,
	}, c.MerkleRoots)
	if err != nil {
		return err
	}
	// if there is a cached revision, store it as an unapplied WAL transaction
	if cr.Revision.NewRevisionNumber != 0 {
		sc, ok := cs.Acquire(m.ID)
		if !ok {
			return errors.New("contract set is missing contract that was just added")
		}
		defer cs.Return(sc)
		if len(cr.MerkleRoots) == sc.merkleRoots.len()+1 {
			root := cr.MerkleRoots[len(cr.MerkleRoots)-1]
			_, err = sc.managedRecordUploadIntent(cr.Revision, root, types.ZeroCurrency, types.ZeroCurrency)
		} else {
			_, err = sc.managedRecordDownloadIntent(cr.Revision, types.ZeroCurrency)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// A V130Contract specifies the v130 contract format.
type V130Contract struct {
	HostPublicKey    types.SiaPublicKey         `json:"hostpublickey"`
	ID               types.FileContractID       `json:"id"`
	LastRevision     types.FileContractRevision `json:"lastrevision"`
	LastRevisionTxn  types.Transaction          `json:"lastrevisiontxn"`
	MerkleRoots      MerkleRootSet              `json:"merkleroots"`
	SecretKey        crypto.SecretKey           `json:"secretkey"`
	StartHeight      types.BlockHeight          `json:"startheight"`
	DownloadSpending types.Currency             `json:"downloadspending"`
	StorageSpending  types.Currency             `json:"storagespending"`
	UploadSpending   types.Currency             `json:"uploadspending"`
	TotalCost        types.Currency             `json:"totalcost"`
	ContractFee      types.Currency             `json:"contractfee"`
	TxnFee           types.Currency             `json:"txnfee"`
	SiafundFee       types.Currency             `json:"siafundfee"`
}

// EndHeight returns the height at which the host is no longer obligated to
// store contract data.
func (c *V130Contract) EndHeight() types.BlockHeight {
	return c.LastRevision.NewWindowStart
}

// RenterFunds returns the funds remaining in the contract's Renter payout as
// of the most recent revision.
func (c *V130Contract) RenterFunds() types.Currency {
	if len(c.LastRevision.NewValidProofOutputs) < 2 {
		return types.ZeroCurrency
	}
	return c.LastRevision.NewValidProofOutputs[0].Value
}

// A V130CachedRevision contains changes that would be applied to a
// RenterContract if a contract revision succeeded.
type V130CachedRevision struct {
	Revision    types.FileContractRevision `json:"revision"`
	MerkleRoots modules.MerkleRootSet      `json:"merkleroots"`
}

// MerkleRootSet is a set of Merkle roots, and gets encoded more efficiently.
type MerkleRootSet []crypto.Hash

// MarshalJSON defines a JSON encoding for a MerkleRootSet.
func (mrs MerkleRootSet) MarshalJSON() ([]byte, error) {
	// Copy the whole array into a giant byte slice and then encode that.
	fullBytes := make([]byte, crypto.HashSize*len(mrs))
	for i := range mrs {
		copy(fullBytes[i*crypto.HashSize:(i+1)*crypto.HashSize], mrs[i][:])
	}
	return json.Marshal(fullBytes)
}

// UnmarshalJSON attempts to decode a MerkleRootSet, falling back on the legacy
// decoding of a []crypto.Hash if that fails.
func (mrs *MerkleRootSet) UnmarshalJSON(b []byte) error {
	// Decode the giant byte slice, and then split it into separate arrays.
	var fullBytes []byte
	err := json.Unmarshal(b, &fullBytes)
	if err != nil {
		// Encoding the byte slice has failed, try decoding it as a []crypto.Hash.
		var hashes []crypto.Hash
		err := json.Unmarshal(b, &hashes)
		if err != nil {
			return err
		}
		*mrs = MerkleRootSet(hashes)
		return nil
	}

	umrs := make(MerkleRootSet, len(fullBytes)/32)
	for i := range umrs {
		copy(umrs[i][:], fullBytes[i*crypto.HashSize:(i+1)*crypto.HashSize])
	}
	*mrs = umrs
	return nil
}

// unmarshalHeader loads the header of a file contract. The load processes
// starts by attempting to load the contract assuming it is the most recent
// version of the contract. If that fails, it'll try increasingly older versions
// of the contract until it either succeeds or it runs out of decoding
// strategies to try.
func unmarshalHeader(b []byte, u *updateSetHeader) error {
	// Try unmarshaling the header.
	if err := encoding.Unmarshal(b, u); err != nil {
		// Try unmarshalling the update
		v132Err := updateSetHeaderUnmarshalV132ToV1413(b, u)
		if v132Err != nil {
			return errors.AddContext(errors.Compose(err, v132Err), "unable to unmarshal update set header")
		}
	}
	return nil
}
