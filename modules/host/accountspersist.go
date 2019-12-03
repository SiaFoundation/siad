package host

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
)

const (
	// accountsOffset is the offset at which accounts are written to disk, this
	// offset ensures that an account never cross the 512 bytes boundary
	// (traditionally the size of a sector on disk) when it is written at its
	// location. Seeing as the metadata is 32 bytes and the accounts are 128
	// bytes in size, we need to offset the first account by 96 bytes.
	accountsOffset = 96

	// accountSize is the fixed account size in bytes
	accountSize = 1 << 7 // 128 bytes

	// accountsFilename is the filename of the file that holds the accounts
	accountsFilename = "accounts.txt"

	// fingerprintSize is the fixed fingerprint size in bytes
	fingerprintSize = 1 << 6 // 64 bytes

	// filenames for fingerprint buckets
	fingerprintsCurrFilename = "fingerprints_curr.db"
	fingerprintsNxtFilename  = "fingerprints_nxt.db"

	// bucketBlockRange defines the range of blocks a fingerprint bucket spans
	bucketBlockRange = 20
)

var (
	// accountMetadata contains the header and version strings that identify the
	// accounts persist file.
	accountMetadata = persist.FixedMetadata{
		Header:  types.Specifier{'A', 'c', 'c', 'o', 'u', 'n', 't', 's'},
		Version: types.Specifier{'1', '.', '4', '.', '2'},
	}

	// fingerprintsMetadata contains the header and version strings that
	// identify the fingerprints persist file.
	fingerprintsMetadata = persist.FixedMetadata{
		Header:  types.Specifier{'F', 'i', 'n', 'g', 'e', 'r', 'P', 'r', 'i', 'n', 't', 's'},
		Version: types.Specifier{'1', '.', '4', '.', '2'},
	}
)

type (
	// accountsPersister is a subsystem that will persist data for the account
	// manager. This includes all ephemeral account data and fingerprints.
	accountsPersister struct {
		accounts                 modules.File
		staticFingerprintManager *fingerprintManager

		indexLocks map[uint32]*indexLock

		mu sync.Mutex
		h  *Host
	}

	// accountsPersisterData contains all accounts data and fingerprints
	accountsPersisterData struct {
		accounts     map[string]*account
		fingerprints map[crypto.Hash]struct{}
	}

	// accountData contains all data persisted for a single ephemeral account
	accountData struct {
		Id          types.SiaPublicKey
		Balance     types.Currency
		LastTxnTime int64
	}

	// indexLock contains a lock plus a count of the number of threads currently
	// waiting to access the lock.
	indexLock struct {
		waiting int
		mu      sync.Mutex
	}

	// fingerprintManager is a helper struct that wraps both buckets and
	// specifies the threshold blockheight at which they need to rotate
	fingerprintManager struct {
		bucketBlockRange int
		current          modules.File
		currentPath      string
		next             modules.File
		nextPath         string
	}
)

// newAccountsPersister returns a new account persister
func (h *Host) newAccountsPersister(am *accountManager) (_ *accountsPersister, err error) {
	ap := &accountsPersister{
		indexLocks: make(map[uint32]*indexLock),
		h:          h,
	}

	// Open the accounts file
	path := filepath.Join(h.persistDir, accountsFilename)
	if ap.accounts, err = ap.openAccountsFile(path); err != nil {
		return nil, errors.AddContext(err, "could not open accounts file")
	}

	// Create the fingerprint manager
	if ap.staticFingerprintManager, err = ap.newFingerprintManager(bucketBlockRange); err != nil {
		return nil, err // already has context
	}

	return ap, nil
}

// newFingerprintManager will create a fingerprint manager, this manager uses
// two files to store the fingerprints on disk
func (ap *accountsPersister) newFingerprintManager(bucketBlockRange int) (_ *fingerprintManager, err error) {
	fm := &fingerprintManager{bucketBlockRange: bucketBlockRange}

	fm.currentPath = filepath.Join(ap.h.persistDir, fingerprintsCurrFilename)
	if fm.current, err = ap.openFingerprintBucket(fm.currentPath); err != nil {
		return nil, errors.AddContext(err, "could not open fingerprint bucket")
	}

	fm.nextPath = filepath.Join(ap.h.persistDir, fingerprintsNxtFilename)
	if fm.next, err = ap.openFingerprintBucket(fm.nextPath); err != nil {
		return nil, errors.AddContext(err, "could not open fingerprint bucket")
	}

	return fm, nil
}

// callSaveAccount will persist the given account data at given index
func (ap *accountsPersister) callSaveAccount(data *accountData, index uint32) error {
	ap.managedLockIndex(index)
	defer ap.managedUnlockIndex(index)

	// Get the account data bytes (performs sanity check)
	accBytes, err := data.bytes()
	if err != nil {
		return err
	}

	// Write the data to disk
	return errors.AddContext(writeAtAndSync(ap.accounts, accBytes, location(index)), "failed to save account")
}

// callDeleteAccount will delete the account on disk by writing empty data to it
func (ap *accountsPersister) callDeleteAccount(index uint32) error {
	ap.managedLockIndex(index)
	defer ap.managedUnlockIndex(index)

	// Overwrite the account with 0 bytes
	accBytes := make([]byte, accountSize)
	return errors.AddContext(writeAtAndSync(ap.accounts, accBytes, location(index)), "failed to delete account")
}

// callSaveFingerprint writes the fingerprint to disk
func (ap *accountsPersister) callSaveFingerprint(fp crypto.Hash, expiry, currentBlockHeight types.BlockHeight) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	// Write fingerprint to disk
	return errors.AddContext(ap.staticFingerprintManager.save(fp, expiry, currentBlockHeight), "could not save fingerprint")
}

// callLoadData loads the accounts data from disk and returns it
func (ap *accountsPersister) callLoadData() (*accountsPersisterData, error) {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	// Load accounts
	accounts := make(map[string]*account)
	if err := ap.loadAccounts(ap.accounts, accounts); err != nil {
		return nil, err
	}

	// Load all fingerprints
	fingerprints := make(map[crypto.Hash]struct{})
	if err := ap.loadFingerprints(ap.staticFingerprintManager.currentPath, fingerprints); err != nil {
		return nil, err
	}
	if err := ap.loadFingerprints(ap.staticFingerprintManager.nextPath, fingerprints); err != nil {
		return nil, err
	}

	return &accountsPersisterData{accounts: accounts, fingerprints: fingerprints}, nil
}

// callRotateFingerprintBuckets will rotate the fingerprint bucket files, but
// only if the current block height exceeds the current bucket's threshold
func (ap *accountsPersister) callRotateFingerprintBuckets(currentBlockHeight types.BlockHeight) (err error) {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	fm := ap.staticFingerprintManager
	threshold := calculateExpiryThreshold(currentBlockHeight, fm.bucketBlockRange)

	// If the current blockheihgt is less than the calculated threshold, we do
	// not need to rotate the bucket files on the file system
	if currentBlockHeight < threshold {
		return nil
	}

	// Close the current fingerprint bucket
	if err = syncAndClose(fm.current); err != nil {
		ap.h.log.Println("ERROR: could not close current fingerprint bucket")
		return err
	}

	// Swap the fingerprints by renaming the next bucket
	if err = os.Rename(fm.nextPath, fm.currentPath); err != nil {
		ap.h.log.Println("ERROR: could not rename next fingerprint bucket")
		return err
	}
	fm.current = fm.next

	// Create a new next bucket
	fm.next, err = ap.openFingerprintBucket(fm.nextPath)
	if err != nil {
		ap.h.log.Println("ERROR: could not close current fingerprint bucket")
		return err
	}

	return nil
}

// managedLockAccount grabs a lock on an (account) index.
func (ap *accountsPersister) managedLockIndex(index uint32) {
	ap.mu.Lock()
	il, exists := ap.indexLocks[index]
	if exists {
		il.waiting++
	} else {
		il = &indexLock{
			waiting: 1,
		}
		ap.indexLocks[index] = il
	}
	ap.mu.Unlock()

	// Block until the index is available.
	il.mu.Lock()
}

// managedLockAccount releases a lock on an (account) index.
func (ap *accountsPersister) managedUnlockIndex(index uint32) {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	// Release the lock on the index.
	il, exists := ap.indexLocks[index]
	if !exists {
		ap.h.log.Critical("Unlock of an account index that is not locked.")
		return
	}
	il.waiting--
	il.mu.Unlock()

	// If nobody else is trying to lock the index, perform garbage collection.
	if il.waiting == 0 {
		delete(ap.indexLocks, index)
	}
}

// openAccountsFile is a helper method to open the accounts file with
// appropriate metadata header and flags
func (ap *accountsPersister) openAccountsFile(path string) (modules.File, error) {
	// open file in read-write mode and create if it does not exist yet
	return ap.openFileWithMetadata(path, os.O_RDWR|os.O_CREATE, accountMetadata)
}

// openFingerprintBucket is a helper method to open a fingerprint bucket with
// appropriate metadata header and flags
func (ap *accountsPersister) openFingerprintBucket(path string) (modules.File, error) {
	// open file in append-only mode and create if it does not exist yet
	return ap.openFileWithMetadata(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, fingerprintsMetadata)

}

// openFileWithMetadata will open the file at given path. If the file did not
// exist prior to calling this method, it will write the metadata header to it.
func (ap *accountsPersister) openFileWithMetadata(path string, flags int, metadata persist.FixedMetadata) (modules.File, error) {
	_, statErr := os.Stat(path)

	// Open the file, create it if it does not exist yet
	file, err := ap.h.dependencies.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, err
	}

	// If it did not exist prior to calling this method, write header metadata.
	// Otherwise verify the metadata header.
	if os.IsNotExist(statErr) {
		_, err := file.Write(encoding.Marshal(metadata))
		if err != nil {
			return nil, err
		}
	} else {
		_, err := persist.VerifyMetadataHeader(file, metadata)
		if err != nil {
			return nil, err
		}
	}

	return file, nil
}

// close will cleanly shutdown the account persister's open file handles
func (ap *accountsPersister) close() error {
	return errors.Compose(
		ap.staticFingerprintManager.close(),
		syncAndClose(ap.accounts),
	)
}

// loadAccounts will read the given file and load the accounts into the map
func (ap *accountsPersister) loadAccounts(file modules.File, m map[string]*account) error {
	bytes, err := ioutil.ReadFile(file.Name())
	if err != nil {
		return errors.AddContext(err, "could not read accounts file")
	}
	nBytes := int64(len(bytes))

	var index uint32
	for ; ; index++ {
		accLocation := location(index)
		if accLocation >= nBytes {
			break
		}
		accBytes := bytes[accLocation : accLocation+accountSize]

		var data accountData
		if err := encoding.Unmarshal(accBytes, &data); err != nil {
			ap.h.log.Critical(errors.New("could not decode account data"))
			continue // not much we can do here besides log this critical event
		}

		// deleted accounts will decode into an account with 0 as LastTxnTime,
		// we want to skip those so that index becomes free and eventually gets
		// overwritten
		if data.LastTxnTime > 0 {
			account := data.account(index)
			m[account.id] = account
		}
	}

	return nil
}

// loadFingerprints will read the file at given path and load the fingerprints
// into the map
func (ap *accountsPersister) loadFingerprints(path string, m map[crypto.Hash]struct{}) error {
	bytes, err := ioutil.ReadFile(path)
	if err != nil {
		return errors.AddContext(err, "could not read fingerprints file")
	}

	for i := persist.FixedMetadataSize; i < len(bytes); i += fingerprintSize {
		var fp crypto.Hash
		if err := encoding.Unmarshal(bytes[i:i+fingerprintSize], &fp); err != nil {
			ap.h.log.Critical(errors.New("could not decode account data"))
			continue // not much we can do here besides log this critical event
		}
		m[fp] = struct{}{}
	}

	return nil
}

// accountData transforms the account into an accountData struct which will
// contain all data we persist to disk
func (a *account) accountData() *accountData {
	spk := types.SiaPublicKey{}
	spk.LoadString(a.id)
	return &accountData{
		Id:          spk,
		Balance:     a.balance,
		LastTxnTime: a.lastTxnTime,
	}
}

// account transforms the accountData we loaded from disk into an account we
// keep in memory
func (a *accountData) account(index uint32) *account {
	return &account{
		id:           a.Id.String(),
		balance:      a.Balance,
		lastTxnTime:  a.LastTxnTime,
		index:        index,
		blockedCalls: make(blockedCallHeap, 0),
	}
}

// bytes returns the account data as an array of bytes.
func (a *accountData) bytes() ([]byte, error) {
	// Sanity check, ensure the encoded account data is not too large
	accMar := encoding.Marshal(*a)
	if len(accMar) > accountSize {
		build.Critical(errors.New("ephemeral account size is larger than the expected size"))
		return nil, ErrAccountPersist
	}

	// copy bytes into an array of appropriate size
	accBytes := make([]byte, accountSize)
	copy(accBytes, accMar)
	return accBytes, nil
}

// save will persist the given fingerprint into the appropriate bucket
func (fm *fingerprintManager) save(fp crypto.Hash, expiry, currentBlockHeight types.BlockHeight) (err error) {
	// Sanity check, ensure the encoded fingerprint is not too large
	fpMar := encoding.Marshal(fp)
	if len(fpMar) > fingerprintSize {
		build.Critical(errors.New("fingerprint size is larger than the expected size"))
		return ErrAccountPersist
	}

	// copy bytes into an array of appropriate size
	fpBytes := make([]byte, fingerprintSize)
	copy(fpBytes, fpMar)

	// write into bucket depending on it's expiry
	if expiry < calculateExpiryThreshold(currentBlockHeight, fm.bucketBlockRange) {
		return writeAndSync(fm.current, fpBytes)
	}
	return writeAndSync(fm.next, fpBytes)
}

// close will close all open files
func (fm *fingerprintManager) close() error {
	return errors.Compose(
		syncAndClose(fm.current),
		syncAndClose(fm.next),
	)
}

// calculateExpiryThreshold will calculate the appropriate threshold given the
// current blockheight and the bucket block range
func calculateExpiryThreshold(currentBlockHeight types.BlockHeight, bucketBlockRange int) types.BlockHeight {
	cbh := uint64(currentBlockHeight)
	bbr := uint64(bucketBlockRange)
	threshold := cbh + (bbr - (cbh % bbr))
	return types.BlockHeight(threshold)
}

// writeAndSync will write the given bytes to the file and call sync
func writeAndSync(file modules.File, b []byte) error {
	if _, err := file.Write(b); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return nil
}

// writeAndSync will write the given bytes to the file at given location and
// call sync
func writeAtAndSync(file modules.File, b []byte, location int64) error {
	if _, err := file.WriteAt(b, location); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return nil
}

// syncAndClose will sync and close the given file
func syncAndClose(file modules.File) error {
	return errors.Compose(
		file.Sync(),
		file.Close(),
	)
}

// location is a helper method that returns the location of the account at given
// index
func location(index uint32) int64 {
	return persist.FixedMetadataSize + accountsOffset + int64(uint64(index)*accountSize)
}
