package proto

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/encoding"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

const (
	updateNameInsertContract = "insertContract"
	updateNameSetHeader      = "setHeader"
	updateNameSetRoot        = "setRoot"

	// decodeMaxSizeMultiplier is multiplied with the size of an encoded object
	// to allocated a bit of extra space for decoding.
	decodeMaxSizeMultiplier = 3
)

// updateInsertContract is an update that inserts a contract into the
// contractset with the given header and roots.
type updateInsertContract struct {
	Header contractHeader
	Roots  []crypto.Hash
}

// updateSetHeader is an update that updates the header of the filecontract with
// the given id.
type updateSetHeader struct {
	ID     types.FileContractID
	Header contractHeader
}

// updateSetRoot is an update which updates the sector root at the given index
// of a filecontract with the specified id.
type updateSetRoot struct {
	ID    types.FileContractID
	Root  crypto.Hash
	Index int
}

// contractHeader holds all the information about a contract apart from the
// sector roots themselves.
type contractHeader struct {
	// transaction is the signed transaction containing the most recent
	// revision of the file contract.
	Transaction types.Transaction

	// secretKey is the key used by the renter to sign the file contract
	// transaction.
	SecretKey crypto.SecretKey

	// Same as modules.RenterContract.
	StartHeight         types.BlockHeight
	DownloadSpending    types.Currency
	FundAccountSpending types.Currency
	MaintenanceSpending modules.MaintenanceSpending
	StorageSpending     types.Currency
	UploadSpending      types.Currency
	TotalCost           types.Currency
	ContractFee         types.Currency
	TxnFee              types.Currency
	SiafundFee          types.Currency
	Utility             modules.ContractUtility
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

// copyTransaction creates a deep copy of the txn struct.
func (h *contractHeader) copyTransaction() (txn types.Transaction) {
	encoding.Unmarshal(encoding.Marshal(h.Transaction), &txn)
	return
}

// LastRevision returns the last revision of the contract.
func (h *contractHeader) LastRevision() types.FileContractRevision {
	return h.Transaction.FileContractRevisions[0]
}

// ID returns the contract's ID.
func (h *contractHeader) ID() types.FileContractID {
	return h.LastRevision().ID()
}

// HostPublicKey returns the host's public key from the last contract revision.
func (h *contractHeader) HostPublicKey() types.SiaPublicKey {
	return h.LastRevision().HostPublicKey()
}

// RenterFunds returns the remaining renter funds as per the last contract
// revision.
func (h *contractHeader) RenterFunds() types.Currency {
	return h.LastRevision().ValidRenterPayout()
}

// EndHeight returns the block height of the last constract revision.
func (h *contractHeader) EndHeight() types.BlockHeight {
	return h.LastRevision().EndHeight()
}

// unappliedWalTxn is a wrapper around writeaheadlog.Transaction that guarantees
// we only call `SignalUpdatesApplied` once.
type unappliedWalTxn struct {
	err  error
	once sync.Once
	*writeaheadlog.Transaction
}

// newUnappliedWalTxn wraps a `writeaheadlog.Transaction` in an unappliedWalTxn.
func newUnappliedWalTxn(t *writeaheadlog.Transaction) *unappliedWalTxn {
	return &unappliedWalTxn{
		Transaction: t,
	}
}

// SignalUpdatesApplied calls `SignalUpdatesApplied` on the wrapped wal. It will
// do so only once.
func (t *unappliedWalTxn) SignalUpdatesApplied() error {
	t.once.Do(func() {
		t.err = t.Transaction.SignalUpdatesApplied()
	})
	return t.err
}

// newWalTxn creates a new wal transaction and automatically wraps it in an
// unappliedWalTxn.
func (c *SafeContract) newWalTxn(updates []writeaheadlog.Update) (*unappliedWalTxn, error) {
	wtxn, err := c.staticWal.NewTransaction(updates)
	if err != nil {
		return nil, err
	}
	return newUnappliedWalTxn(wtxn), nil
}

// A SafeContract contains the most recent revision transaction negotiated
// with a host, and the secret key used to sign it.
type SafeContract struct {
	header contractHeader

	// merkleRoots are the sector roots covered by this contract.
	merkleRoots *merkleRoots

	// unappliedTxns are the transactions that were written to the WAL but not
	// applied to the contract file.
	unappliedTxns []*unappliedWalTxn

	staticHeaderFile *os.File
	staticWal        *writeaheadlog.WAL
	mu               sync.Mutex

	staticRC *refCounter

	// revisionMu serializes revisions to the contract. It is acquired by
	// (ContractSet).Acquire and released by (ContractSet).Return. When holding
	// revisionMu, it is still necessary to lock mu when modifying fields
	// of the SafeContract.
	revisionMu sync.Mutex
}

// CommitPaymentIntent will commit the intent to pay a host for an rpc by
// committing the signed txn in the contract's header.
func (c *SafeContract) CommitPaymentIntent(t *unappliedWalTxn, signedTxn types.Transaction, amount types.Currency, details modules.SpendingDetails) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// construct new header
	newHeader := c.header
	newHeader.Transaction = signedTxn
	newHeader.FundAccountSpending = newHeader.FundAccountSpending.Add(details.FundAccountSpending)
	newHeader.MaintenanceSpending = newHeader.MaintenanceSpending.Add(details.MaintenanceSpending)

	if err := c.applySetHeader(newHeader); err != nil {
		return err
	}
	if err := c.staticHeaderFile.Sync(); err != nil {
		return err
	}
	if err := t.SignalUpdatesApplied(); err != nil {
		return err
	}
	return c.clearUnappliedTxns()
}

// clearUnappliedTxns marks all unapplied transactions as completed without
// applying them.
func (c *SafeContract) clearUnappliedTxns() error {
	for len(c.unappliedTxns) > 0 {
		// Fetch next txn.
		txn := c.unappliedTxns[0]

		// Mark it as applied.
		err := txn.SignalUpdatesApplied()
		if err != nil {
			return err
		}

		// Remove it from the contract. In case we crash, we at least won't
		// start at the beginning again.
		c.unappliedTxns = c.unappliedTxns[1:]
	}

	// Set the slice to nil to free memory.
	c.unappliedTxns = nil
	return nil
}

// LastRevision returns the most recent revision
func (c *SafeContract) LastRevision() types.FileContractRevision {
	c.mu.Lock()
	h := c.header
	c.mu.Unlock()
	return h.LastRevision()
}

// Metadata returns the metadata of a renter contract
func (c *SafeContract) Metadata() modules.RenterContract {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := c.header
	return modules.RenterContract{
		ID:                  h.ID(),
		Transaction:         h.copyTransaction(),
		HostPublicKey:       h.HostPublicKey(),
		StartHeight:         h.StartHeight,
		EndHeight:           h.EndHeight(),
		RenterFunds:         h.RenterFunds(),
		DownloadSpending:    h.DownloadSpending,
		FundAccountSpending: h.FundAccountSpending,
		MaintenanceSpending: h.MaintenanceSpending,
		StorageSpending:     h.StorageSpending,
		UploadSpending:      h.UploadSpending,
		TotalCost:           h.TotalCost,
		ContractFee:         h.ContractFee,
		TxnFee:              h.TxnFee,
		SiafundFee:          h.SiafundFee,
		Utility:             h.Utility,
	}
}

// PublicKey returns the public key capable of verifying the renter's signature
// on a contract.
func (c *SafeContract) PublicKey() crypto.PublicKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.header.SecretKey.PublicKey()
}

// RecordPaymentIntent will records the changes we are about to make to the
// revision in order to pay a host for an RPC.
func (c *SafeContract) RecordPaymentIntent(rev types.FileContractRevision, amount types.Currency, details modules.SpendingDetails) (*unappliedWalTxn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	newHeader := c.header
	newHeader.Transaction.FileContractRevisions = []types.FileContractRevision{rev}
	newHeader.Transaction.TransactionSignatures = nil
	newHeader.FundAccountSpending = newHeader.FundAccountSpending.Add(details.FundAccountSpending)
	newHeader.MaintenanceSpending = newHeader.MaintenanceSpending.Add(details.MaintenanceSpending)

	t, err := c.newWalTxn([]writeaheadlog.Update{
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

// Sign will sign the given hash using the safecontract's secret key
func (c *SafeContract) Sign(hash crypto.Hash) crypto.Signature {
	c.mu.Lock()
	defer c.mu.Unlock()
	return crypto.SignHash(hash, c.header.SecretKey)
}

// UpdateUtility updates the utility field of a contract.
func (c *SafeContract) UpdateUtility(utility modules.ContractUtility) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Construct new header
	newHeader := c.header
	newHeader.Utility = utility

	// Record the intent to change the header in the wal.
	t, err := c.newWalTxn([]writeaheadlog.Update{
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
	if err := c.staticHeaderFile.Sync(); err != nil {
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

// makeUpdateInsertContract creates a writeaheadlog.Update to insert a new
// contract into the contractset.
func makeUpdateInsertContract(h contractHeader, roots []crypto.Hash) (writeaheadlog.Update, error) {
	// Validate header.
	if err := h.validate(); err != nil {
		return writeaheadlog.Update{}, err
	}
	// Create update.
	return writeaheadlog.Update{
		Name: updateNameInsertContract,
		Instructions: encoding.Marshal(updateInsertContract{
			Header: h,
			Roots:  roots,
		}),
	}, nil
}

// makeUpdateSetHeader creates an update that changes the header.
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

// makeUpdateSetRoot creates an update that sets a given root, existing or not.
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

// makeUpdateRefCounterAppend creates a WAL update that sets a given
// refcounter value. If there is no open refcounter update session this method
// will open one. This update session will be closed when we apply the update.
func (c *SafeContract) makeUpdateRefCounterAppend() (writeaheadlog.Update, error) {
	if build.Release != "testing" {
		return writeaheadlog.Update{}, nil // no update needed
	}
	// TODO This hidden retry is a problem that we need to refactor away, most
	// 	probably by refactoring the entire `contract` workflow. The same applies
	// 	to `applyRefCounterUpdate`.
	u, err := c.staticRC.callAppend()
	// If we don't have an update session open one and try again.
	if errors.Contains(err, ErrUpdateWithoutUpdateSession) {
		if err = c.staticRC.callStartUpdate(); err != nil {
			return writeaheadlog.Update{}, err
		}
		u, err = c.staticRC.callAppend()
	}
	return u, err
}

// applyRefCounterUpdate applies a refcounter WAL update. If there is no open
// update session, it will open one and it will leave it open. This update
// session must be closed by the calling method.
func (c *SafeContract) applyRefCounterUpdate(u writeaheadlog.Update) error {
	if build.Release != "testing" {
		return nil
	}
	err := c.staticRC.callCreateAndApplyTransaction(u)
	// If we don't have an open update session open one and try again.
	if errors.Contains(err, ErrUpdateWithoutUpdateSession) {
		if err = c.staticRC.callStartUpdate(); err != nil {
			return err
		}
		err = c.staticRC.callCreateAndApplyTransaction(u)
	}
	return err
}

// applySetHeader directly makes changes to the contract header on disk without
// going through a WAL transaction.
func (c *SafeContract) applySetHeader(h contractHeader) error {
	if build.DEBUG {
		// read the existing header on disk, to make sure we aren't overwriting
		// it with an older revision
		var oldHeader contractHeader
		_, err := c.staticHeaderFile.Seek(0, io.SeekStart)
		if err != nil {
			build.Critical(err)
		}
		headerBytes, err := ioutil.ReadAll(c.staticHeaderFile)
		if err == nil {
			if err := encoding.Unmarshal(headerBytes, &oldHeader); err == nil {
				if oldHeader.LastRevision().NewRevisionNumber > h.LastRevision().NewRevisionNumber {
					build.Critical("overwriting a newer revision:", oldHeader.LastRevision().NewRevisionNumber, h.LastRevision().NewRevisionNumber)
				}
			}
		}
	}
	headerBytes := encoding.Marshal(h)
	if _, err := c.staticHeaderFile.WriteAt(headerBytes, 0); err != nil {
		return err
	}
	c.header = h
	return nil
}

// applySetRoot directly sets a given root hash at a given index on disk without
// going through a WAL transaction.
func (c *SafeContract) applySetRoot(root crypto.Hash, index int) error {
	return c.merkleRoots.insert(index, root)
}

// managedRecordAppendIntent creates a WAL update that adds a new sector to the
// contract and queues this update for application.
func (c *SafeContract) managedRecordAppendIntent(rev types.FileContractRevision, root crypto.Hash, storageCost, bandwidthCost types.Currency) (*unappliedWalTxn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// construct new header
	// NOTE: this header will not include the host signature
	newHeader := c.header
	newHeader.Transaction.FileContractRevisions = []types.FileContractRevision{rev}
	newHeader.Transaction.TransactionSignatures = nil
	newHeader.StorageSpending = newHeader.StorageSpending.Add(storageCost)
	newHeader.UploadSpending = newHeader.UploadSpending.Add(bandwidthCost)

	updates := []writeaheadlog.Update{
		c.makeUpdateSetHeader(newHeader),
		c.makeUpdateSetRoot(root, c.merkleRoots.len()),
	}
	if build.Release == "testing" {
		rcUpdate, err := c.makeUpdateRefCounterAppend()
		if err != nil {
			return nil, errors.AddContext(err, "failed to create a refcounter update")
		}
		updates = append(updates, rcUpdate)
	}
	t, err := c.newWalTxn(updates)
	if err != nil {
		return nil, err
	}
	if err := <-t.SignalSetupComplete(); err != nil {
		return nil, err
	}
	c.unappliedTxns = append(c.unappliedTxns, t)
	return t, nil
}

// managedCommitAppend ignores the header update in the given transaction and
// instead applies a new one based on the provided signedTxn. This is necessary
// if we run into a desync of contract revisions between renter and host.
func (c *SafeContract) managedCommitAppend(t *unappliedWalTxn, signedTxn types.Transaction, storageCost, bandwidthCost types.Currency) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// construct new header
	newHeader := c.header
	newHeader.Transaction = signedTxn
	newHeader.StorageSpending = newHeader.StorageSpending.Add(storageCost)
	newHeader.UploadSpending = newHeader.UploadSpending.Add(bandwidthCost)

	// we need this declaration so we don't shadow useful variables further down
	var err error
	if err = c.applySetHeader(newHeader); err != nil {
		return err
	}

	// pluck the refcounter and setRoot updates from the WAL txn
	for _, u := range t.Updates {
		switch u.Name {
		case updateNameSetHeader:
			// do nothing - we already applied a new version of this update
		case updateNameSetRoot:
			var sru updateSetRoot
			if err := encoding.Unmarshal(u.Instructions, &sru); err != nil {
				return err
			}
			if err := c.applySetRoot(sru.Root, sru.Index); err != nil {
				return err
			}
		case updateNameRCWriteAt:
			if err = c.applyRefCounterUpdate(u); err != nil {
				return errors.AddContext(err, "failed to apply refcounter update")
			}
			if err = c.staticRC.callUpdateApplied(); err != nil {
				return err
			}
		default:
			build.Critical("unexpected update", u.Name)
		}
	}

	if err = c.staticHeaderFile.Sync(); err != nil {
		return err
	}
	if err = t.SignalUpdatesApplied(); err != nil {
		return err
	}
	if err := c.clearUnappliedTxns(); err != nil {
		return errors.AddContext(err, "failed to clear unapplied txns")
	}
	return nil
}

// managedRecordDownloadIntent creates a WAL update that updates the header with
// the new download costs.
func (c *SafeContract) managedRecordDownloadIntent(rev types.FileContractRevision, bandwidthCost types.Currency) (*unappliedWalTxn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// construct new header
	// NOTE: this header will not include the host signature
	newHeader := c.header
	newHeader.Transaction.FileContractRevisions = []types.FileContractRevision{rev}
	newHeader.Transaction.TransactionSignatures = nil
	newHeader.DownloadSpending = newHeader.DownloadSpending.Add(bandwidthCost)

	t, err := c.newWalTxn([]writeaheadlog.Update{
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

// managedCommitDownload *ignores* all updates in the given transaction and
// instead applies the provided signedTxn. See managedCommitAppend.
func (c *SafeContract) managedCommitDownload(t *unappliedWalTxn, signedTxn types.Transaction, bandwidthCost types.Currency) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// construct new header
	newHeader := c.header
	newHeader.Transaction = signedTxn
	newHeader.DownloadSpending = newHeader.DownloadSpending.Add(bandwidthCost)

	if err := c.applySetHeader(newHeader); err != nil {
		return err
	}
	if err := c.staticHeaderFile.Sync(); err != nil {
		return err
	}
	if err := t.SignalUpdatesApplied(); err != nil {
		return err
	}
	if err := c.clearUnappliedTxns(); err != nil {
		return errors.AddContext(err, "failed to clear unapplied txns")
	}
	return nil
}

// managedRecordClearContractIntent records the changes we are about to make to
// the revision in the WAL of the contract.
func (c *SafeContract) managedRecordClearContractIntent(rev types.FileContractRevision, bandwidthCost types.Currency) (*unappliedWalTxn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// construct new header
	// NOTE: this header will not include the host signature
	newHeader := c.header
	newHeader.Transaction.FileContractRevisions = []types.FileContractRevision{rev}
	newHeader.Transaction.TransactionSignatures = nil
	newHeader.UploadSpending = newHeader.UploadSpending.Add(bandwidthCost)

	t, err := c.newWalTxn([]writeaheadlog.Update{
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

// managedCommitClearContract commits the changes we made to the revision when
// clearing a contract to the WAL of the contract.
func (c *SafeContract) managedCommitClearContract(t *unappliedWalTxn, signedTxn types.Transaction, bandwidthCost types.Currency) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// construct new header
	newHeader := c.header
	newHeader.Transaction = signedTxn
	newHeader.UploadSpending = newHeader.UploadSpending.Add(bandwidthCost)
	newHeader.Utility.GoodForRenew = false
	newHeader.Utility.GoodForUpload = false
	newHeader.Utility.Locked = true

	if err := c.applySetHeader(newHeader); err != nil {
		return err
	}
	if err := c.staticHeaderFile.Sync(); err != nil {
		return err
	}
	if err := t.SignalUpdatesApplied(); err != nil {
		return err
	}
	if err := c.clearUnappliedTxns(); err != nil {
		return errors.AddContext(err, "failed to clear unapplied txns")
	}
	return nil
}

// managedCommitTxns commits the unapplied transactions to the contract file and marks
// the transactions as applied.
func (c *SafeContract) managedCommitTxns() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// We need a way of finding out whether we need to close the refcounter's
	// update session here. This will only be necessary if there are refcounter
	// updates in the queue. We don't want to close the session in other cases
	// because that can close someone else's session.
	rcUpdatesApplied := false
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
			case updateNameRCWriteAt:
				if err := c.applyRefCounterUpdate(update); err != nil {
					return err
				}
				rcUpdatesApplied = true
			}
		}
		if err := c.staticHeaderFile.Sync(); err != nil {
			return err
		}
		if err := t.SignalUpdatesApplied(); err != nil {
			return err
		}
	}
	if rcUpdatesApplied {
		if err := c.staticRC.callUpdateApplied(); err != nil {
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
// managedSyncRevision uses the host's revision. Alongside a possible error this
// function returns a boolean that indicates whether a resync was attempted.
func (c *SafeContract) managedSyncRevision(rev types.FileContractRevision, sigs []types.TransactionSignature) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Our current revision should always be signed. If it isn't, we have no
	// choice but to accept the host's revision.
	if len(c.header.Transaction.TransactionSignatures) == 0 {
		c.header.Transaction.FileContractRevisions = []types.FileContractRevision{rev}
		c.header.Transaction.TransactionSignatures = sigs
		return nil
	}

	ourRev := c.header.LastRevision()

	// If the revision number and Merkle root match, we don't need to do
	// anything.
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
				if err := c.staticHeaderFile.Sync(); err != nil {
					return err
				}
				// drop all unapplied transactions
				if err := c.clearUnappliedTxns(); err != nil {
					return errors.AddContext(err, "failed to clear unapplied txns")
				}
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
	if err := c.clearUnappliedTxns(); err != nil {
		return errors.AddContext(err, "failed to clear unapplied txns")
	}
	return nil
}

// managedInsertContract inserts a contract into the set in an ACID fashion
// using the set's WAL.
func (cs *ContractSet) managedInsertContract(h contractHeader, roots []crypto.Hash) (modules.RenterContract, error) {
	insertUpdate, err := makeUpdateInsertContract(h, roots)
	if err != nil {
		return modules.RenterContract{}, err
	}
	txn, err := cs.staticWal.NewTransaction([]writeaheadlog.Update{insertUpdate})
	if err != nil {
		return modules.RenterContract{}, err
	}
	err = <-txn.SignalSetupComplete()
	if err != nil {
		return modules.RenterContract{}, err
	}
	rc, err := cs.managedApplyInsertContractUpdate(insertUpdate)
	if err != nil {
		return modules.RenterContract{}, err
	}
	err = txn.SignalUpdatesApplied()
	if err != nil {
		return modules.RenterContract{}, err
	}
	return rc, nil
}

// managedApplyInsertContractUpdate applies the update to insert a contract into
// a set. This will overwrite existing contracts of the same name to make sure
// the update is idempotent.
func (cs *ContractSet) managedApplyInsertContractUpdate(update writeaheadlog.Update) (modules.RenterContract, error) {
	// Sanity check update.
	if update.Name != updateNameInsertContract {
		return modules.RenterContract{}, fmt.Errorf("can't call managedApplyInsertContractUpdate on update of type '%v'", update.Name)
	}
	// Decode update.
	var insertUpdate updateInsertContract
	if err := encoding.UnmarshalAll(update.Instructions, &insertUpdate); err != nil {
		return modules.RenterContract{}, err
	}
	h := insertUpdate.Header
	roots := insertUpdate.Roots
	// Validate header.
	if err := h.validate(); err != nil {
		return modules.RenterContract{}, err
	}
	headerFilePath := filepath.Join(cs.staticDir, h.ID().String()+contractHeaderExtension)
	rootsFilePath := filepath.Join(cs.staticDir, h.ID().String()+contractRootsExtension)
	rcFilePath := filepath.Join(cs.staticDir, h.ID().String()+refCounterExtension)
	// create the files.
	headerFile, err := os.OpenFile(headerFilePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, modules.DefaultFilePerm)
	if err != nil {
		return modules.RenterContract{}, err
	}
	rootsFile, err := os.OpenFile(rootsFilePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, modules.DefaultFilePerm)
	if err != nil {
		return modules.RenterContract{}, err
	}
	// write header
	if _, err := headerFile.Write(encoding.Marshal(h)); err != nil {
		return modules.RenterContract{}, err
	}
	// Interrupt if necessary.
	if cs.staticDeps.Disrupt("InterruptContractInsertion") {
		return modules.RenterContract{}, errors.New("interrupted")
	}
	// write roots
	merkleRoots := newMerkleRoots(rootsFile)
	for _, root := range roots {
		if err := merkleRoots.push(root); err != nil {
			return modules.RenterContract{}, err
		}
	}
	// sync both files
	if err := headerFile.Sync(); err != nil {
		return modules.RenterContract{}, err
	}
	if err := rootsFile.Sync(); err != nil {
		return modules.RenterContract{}, err
	}
	var rc *refCounter
	if build.Release == "testing" {
		rc, err = newRefCounter(rcFilePath, uint64(len(roots)), cs.staticWal)
		if err != nil {
			return modules.RenterContract{}, errors.AddContext(err, "failed to create a refcounter")
		}
	}
	sc := &SafeContract{
		header:           h,
		merkleRoots:      merkleRoots,
		staticHeaderFile: headerFile,
		staticWal:        cs.staticWal,
		staticRC:         rc,
	}
	// Compatv144 fix missing void output.
	cs.mu.Lock()
	if _, exists := cs.contracts[sc.header.ID()]; exists {
		build.Critical("trying to overwrite existing contract")
	}
	cs.contracts[sc.header.ID()] = sc
	cs.pubKeys[h.HostPublicKey().String()] = sc.header.ID()
	cs.mu.Unlock()
	return sc.Metadata(), nil
}

// loadSafeContractHeader will load a contract from disk, checking for legacy
// encodings if initial attempts fail.
func loadSafeContractHeader(f io.ReadSeeker, decodeMaxSize int) (contractHeader, error) {
	var header contractHeader
	err := encoding.NewDecoder(f, decodeMaxSize).Decode(&header)
	if err != nil {
		// Unable to decode the old header, try a new decode. Seek the file back
		// to the beginning.
		var v1412DecodeErr error
		_, seekErr := f.Seek(0, 0)
		if seekErr != nil {
			return contractHeader{}, errors.AddContext(errors.Compose(err, seekErr), "unable to reset file when attempting legacy decode")
		}
		header, v1412DecodeErr = contractHeaderDecodeV1412ToV1420(f, decodeMaxSize)
		if v1412DecodeErr != nil {
			return contractHeader{}, errors.AddContext(errors.Compose(err, v1412DecodeErr), "unable to decode contract header")
		}
	}
	if err := header.validate(); err != nil {
		return contractHeader{}, errors.AddContext(err, "unable to validate contract header")
	}

	return header, nil
}

// loadSafeContract loads a contract from disk and adds it to the contractset
// if it is valid.
func (cs *ContractSet) loadSafeContract(headerFileName, rootsFileName, refCountFileName string, walTxns []*writeaheadlog.Transaction) (err error) {
	headerFile, err := os.OpenFile(headerFileName, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return err
	}
	rootsFile, err := os.OpenFile(rootsFileName, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Compose(err, headerFile.Close(), rootsFile.Close())
		}
	}()
	headerStat, err := headerFile.Stat()
	if err != nil {
		return err
	}
	header, err := loadSafeContractHeader(headerFile, int(headerStat.Size())*decodeMaxSizeMultiplier)
	if err != nil {
		return errors.AddContext(err, "unable to load contract header")
	}

	// read merkleRoots
	merkleRoots, applyTxns, err := loadExistingMerkleRoots(rootsFile)
	if err != nil {
		return errors.AddContext(err, "unable to load the merkle roots of the contract")
	}
	// add relevant unapplied transactions
	var unappliedTxns []*unappliedWalTxn
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
			unappliedTxns = append(unappliedTxns, newUnappliedWalTxn(t))
		}
	}
	var rc *refCounter
	if build.Release == "testing" {
		// load the reference counter or create a new one if it doesn't exist
		rc, err = loadRefCounter(refCountFileName, cs.staticWal)
		if errors.Contains(err, ErrRefCounterNotExist) {
			rc, err = newRefCounter(refCountFileName, uint64(merkleRoots.numMerkleRoots), cs.staticWal)
		}
		if err != nil {
			return errors.AddContext(err, "failed to load or create a refcounter")
		}
	}
	// add to set
	sc := &SafeContract{
		header:           header,
		merkleRoots:      merkleRoots,
		unappliedTxns:    unappliedTxns,
		staticHeaderFile: headerFile,
		staticWal:        cs.staticWal,
		staticRC:         rc,
	}

	// apply the wal txns if necessary.
	if applyTxns {
		if err := sc.managedCommitTxns(); err != nil {
			return errors.AddContext(err, "unable to commit the wal transactions during contractset recovery")
		}
	}
	if _, exists := cs.contracts[sc.header.ID()]; exists {
		build.Critical("trying to overwrite existing contract")
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
			_, err = sc.managedRecordAppendIntent(cr.Revision, root, types.ZeroCurrency, types.ZeroCurrency)
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
	return c.LastRevision.ValidRenterPayout()
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
	// Try unmarshalling the header.
	if err := encoding.Unmarshal(b, u); err != nil {
		// Try unmarshalling the update
		v132Err := updateSetHeaderUnmarshalV132ToV1420(b, u)
		if v132Err != nil {
			return errors.AddContext(errors.Compose(err, v132Err), "unable to unmarshal update set header")
		}
	}
	return nil
}
