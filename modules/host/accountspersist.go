package host

import (
	"fmt"
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
	// accountSize is the fixed account size in bytes
	accountSize = 1 << 7 // 128 bytes

	// accountsFilename is the filename of the file that holds the accounts
	accountsFilename = "accounts.txt"

	// fingerprintSize is the fixed fingerprint size in bytes
	fingerprintSize = 1 << 5 // 32 bytes

	// filenames for fingerprint buckets
	fingerprintsCurrFilename = "fingerprintsbucket_current.db"
	fingerprintsNxtFilename  = "fingerprintsbucket_next.db"

	// bucketBlockRange defines the range of blocks a fingerprint bucket spans
	bucketBlockRange = 20

	// sectorSize is (traditionally) the size of a sector on disk in bytes.
	// It is used to perform a sanity check that verifies if this is a multiple
	// of the account size
	sectorSize = 512
)

var (
	// accountMetadata contains the header and version specifiers that identify
	// the accounts persist file.
	accountMetadata = persist.FixedMetadata{
		Header:  types.Specifier{'A', 'c', 'c', 'o', 'u', 'n', 't', 's'},
		Version: types.Specifier{'1', '.', '4', '.', '2'},
	}

	// fingerprintsMetadata contains the header and version specifiers that
	// identify the fingerprints persist file.
	fingerprintsMetadata = persist.FixedMetadata{
		Header:  types.Specifier{'F', 'i', 'n', 'g', 'e', 'r', 'P', 'r', 'i', 'n', 't', 's'},
		Version: types.Specifier{'1', '.', '4', '.', '2'},
	}
)

type (
	// accountsPersister is a subsystem that will persist data for the account
	// manager. This includes all ephemeral account data and the fingerprints of
	// the withdrawal messages.
	accountsPersister struct {
		accounts     modules.File
		fingerprints *fingerprintManager
		indexLocks   map[uint32]*indexLock

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
		current     modules.File
		currentPath string
		next        modules.File
		nextPath    string
	}
)

// newAccountsPersister returns a new account persister
func (h *Host) newAccountsPersister(am *accountManager) (_ *accountsPersister, err error) {
	if sectorSize%accountSize != 0 {
		h.log.Critical(errors.New("Sanity check failure: we expected the sector size to be a multiple of the account size to ensure persisting an account never crosses the sector boundary on disk."))
	}

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
	if ap.fingerprints, err = ap.newFingerprintManager(); err != nil {
		return nil, err // already has context
	}

	return ap, nil
}

// newFingerprintManager will create a fingerprint manager, this manager uses
// two files to store the fingerprints on disk
func (ap *accountsPersister) newFingerprintManager() (_ *fingerprintManager, err error) {
	fm := &fingerprintManager{}

	fm.currentPath = filepath.Join(ap.h.persistDir, fingerprintsCurrFilename)
	if fm.current, err = ap.openFingerprintBucket(fm.currentPath); err != nil {
		return nil, errors.AddContext(err, fmt.Sprintf("could not open fingerprint bucket, path %s", fm.currentPath))
	}

	fm.nextPath = filepath.Join(ap.h.persistDir, fingerprintsNxtFilename)
	if fm.next, err = ap.openFingerprintBucket(fm.nextPath); err != nil {
		return nil, errors.AddContext(err, fmt.Sprintf("could not open fingerprint bucket, path %s", fm.nextPath))
	}

	return fm, nil
}

// callSaveAccount will persist the given account data at given index
func (ap *accountsPersister) callSaveAccount(data *accountData, index uint32) error {
	ap.managedLockIndex(index)
	defer ap.managedUnlockIndex(index)

	// Get the account data bytes
	accBytes, err := data.bytes()
	if err != nil {
		return errors.AddContext(err, "save account failed, account could not be encoded")
	}

	// Write the data to disk
	return errors.AddContext(writeAtAndSync(ap.accounts, accBytes, location(index)), "failed to save account")
}

// callBatchDeleteAccount will overwrite the accounts at given indexes with
// zero-bytes. Effectively deleting it.
func (ap *accountsPersister) callBatchDeleteAccount(indexes []uint32) (deleted []uint32, err error) {
	results := make([]error, len(indexes))
	zeroBytes := make([]byte, accountSize)

	// Overwrite the accounts with 0 bytes in parallel
	var wg sync.WaitGroup
	for n, index := range indexes {
		wg.Add(1)
		go func(n int, index uint32) {
			defer wg.Done()
			ap.managedLockIndex(index)
			defer ap.managedUnlockIndex(index)
			results[n] = writeAtAndSync(ap.accounts, zeroBytes, location(index))
		}(n, index)
	}
	wg.Wait()

	// Collect the indexes of all accounts that were successfully deleted,
	// compose the errors of the failures.
	for n, rErr := range results {
		if rErr != nil {
			err = errors.Compose(err, rErr)
			continue
		}
		deleted = append(deleted, indexes[n])
	}
	err = errors.AddContext(err, "batch delete account failed")
	return
}

// callSaveFingerprint writes the fingerprint to disk
func (ap *accountsPersister) callSaveFingerprint(fp crypto.Hash, expiry, currentBlockHeight types.BlockHeight) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	return errors.AddContext(ap.fingerprints.save(fp, expiry, currentBlockHeight), "could not save fingerprint")
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

	// Load fingerprints
	fingerprints := make(map[crypto.Hash]struct{})
	if err := errors.Compose(
		ap.loadFingerprints(ap.fingerprints.currentPath, fingerprints),
		ap.loadFingerprints(ap.fingerprints.nextPath, fingerprints),
	); err != nil {
		return nil, err
	}

	return &accountsPersisterData{
		accounts:     accounts,
		fingerprints: fingerprints,
	}, nil
}

// callRotateFingerprintBuckets will rotate the fingerprint bucket files, but
// only if the current block height exceeds the current bucket's threshold
func (ap *accountsPersister) callRotateFingerprintBuckets() (err error) {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	// Close the current fingerprint files, this syncs the files before closing
	fm := ap.fingerprints
	if err = fm.close(); err != nil {
		return errors.AddContext(err, "could not close fingerprint files")
	}

	// Swap the fingerprints by renaming the next bucket
	if err = os.Rename(fm.nextPath, fm.currentPath); err != nil {
		return errors.AddContext(err, "could not rename next fingerprint bucket")
	}

	// Reopen files
	if fm.current, err = ap.openFingerprintBucket(fm.currentPath); err != nil {
		return errors.AddContext(err, fmt.Sprintf("could not open fingerprint bucket, path %s", fm.currentPath))
	}
	if fm.next, err = ap.openFingerprintBucket(fm.nextPath); err != nil {
		return errors.AddContext(err, fmt.Sprintf("could not open fingerprint bucket, path %s", fm.nextPath))
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
		ap.fingerprints.close(),
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
			// not much we can do here besides log this critical event
			ap.h.log.Critical(errors.New("could not decode account data"))
			continue // TODO follow-up host alert (?)
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
			// not much we can do here besides log this critical event
			ap.h.log.Critical(errors.New("could not decode fingerprint data"))
			continue // TODO follow-up host alert (?)
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
		id:                 a.Id.String(),
		balance:            a.Balance,
		lastTxnTime:        a.LastTxnTime,
		index:              index,
		blockedWithdrawals: make(blockedWithdrawalHeap, 0),
	}
}

// bytes returns the account data as an array of bytes.
func (a *accountData) bytes() ([]byte, error) {
	// Encode the account, verify it has the correct size
	accBytes, err := safeEncode(*a, accountSize)
	if err != nil {
		build.Critical(errors.AddContext(err, "unexpected ephemeral account size"))
		return nil, err
	}
	return accBytes, nil
}

// save will persist the given fingerprint into the appropriate bucket
func (fm *fingerprintManager) save(fp crypto.Hash, expiry, currentBlockHeight types.BlockHeight) (err error) {
	// Encode the fingerprint, verify it has the correct size
	fpBytes, err := safeEncode(fp, fingerprintSize)
	if err != nil {
		build.Critical(errors.New("fingerprint size is larger than the expected size"))
		return ErrAccountPersist
	}

	// write into bucket depending on it's expiry
	_, max := currentBucketRange(currentBlockHeight)
	if expiry < max {
		return writeAndSync(fm.current, fpBytes)
	}
	return writeAndSync(fm.next, fpBytes)
}

// close will close the current and next bucket file
func (fm *fingerprintManager) close() error {
	return errors.Compose(
		syncAndClose(fm.current),
		syncAndClose(fm.next),
	)
}

// currentBucketRange will calculate the range (in blockheight) that defines the
// boundaries of the current bucket. This is non-inclusive, so max is outside
// the bucket, [min,max)
func currentBucketRange(currentBlockHeight types.BlockHeight) (min, max types.BlockHeight) {
	cbh := uint64(currentBlockHeight)
	bbr := uint64(bucketBlockRange)
	threshold := cbh + (bbr - (cbh % bbr))
	min = types.BlockHeight(threshold - bucketBlockRange)
	max = types.BlockHeight(threshold)
	return
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

// safeEncode will encode the given object while performing a sanity check on
// the size
func safeEncode(obj interface{}, expectedSize int) ([]byte, error) {
	objMar := encoding.Marshal(obj)
	if len(objMar) > expectedSize {
		return nil, errors.New("encoded object is larger than the expected size")
	}

	// copy bytes into an array of appropriate size
	bytes := make([]byte, expectedSize)
	copy(bytes, objMar)
	return bytes, nil
}

// location is a helper method that returns the location of the account at given
// index
func location(index uint32) int64 {
	// metadataPadding is the amount of bytes we pad the metadata with. We pad
	// metadata to ensure writing an account to disk never crosses the 512 bytes
	// boundary, traditionally the size of a sector on disk. Seeing as the
	// sector size is a multiple of the account size we pad the metadata until
	// it's as large as a single account
	metadataPadding := int64(accountSize - persist.FixedMetadataSize)
	return persist.FixedMetadataSize + metadataPadding + int64(uint64(index)*accountSize)
}
