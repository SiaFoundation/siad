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

	// bucketBlockRange defines the range of expiry block heights the
	// fingerprints contained in a single bucket can span
	bucketBlockRange = 20

	// sectorSize is (traditionally) the size of a sector on disk in bytes.
	// It is used to perform a sanity check that verifies if this is a multiple
	// of the account size.
	sectorSize = 512
)

var (
	// specifierV1430 is the specifier for version 1.4.3.0
	specifierV1430 = types.NewSpecifier("1.4.3.0")

	// accountMetadata contains the header and version specifiers that identify
	// the accounts persist file.
	accountMetadata = persist.FixedMetadata{
		Header:  types.NewSpecifier("EphemeralAccount"),
		Version: specifierV1430,
	}

	// fingerprintsMetadata contains the header and version specifiers that
	// identify the fingerprints persist file.
	fingerprintsMetadata = persist.FixedMetadata{
		Header:  types.NewSpecifier("Fingerprint"),
		Version: specifierV1430,
	}
)

type (
	// accountsPersister is a subsystem that will persist data for the account
	// manager. This includes all ephemeral account data and the fingerprints of
	// the withdrawal messages.
	accountsPersister struct {
		accounts   modules.File
		indexLocks map[uint32]*indexLock

		staticFingerprintManager *fingerprintManager

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

	// fingerprintManager is used to store fingerprints. It does so by keeping
	// them in two separate buckets, depending on the expiry blockheight of the
	// fingerprint. The underlying files rotate after a certain amount of blocks
	// to ensure the files don't grow too large in size. It has its own mutex to
	// avoid lock contention on the indexLocks.
	fingerprintManager struct {
		staticCurrentPath string
		staticNextPath    string

		current modules.File
		next    modules.File

		staticSaveFingerprintsQueue saveFingerprintsQueue
		wakeChan                    chan struct{}

		mu sync.Mutex
		h  *Host
	}

	// saveFingerprintsQueue wraps a queue of fingerprints that are scheduled to
	// be persisted to disk. It has its own mutex to be able to enqueue a
	// fingerprint with minimal lock contention.
	saveFingerprintsQueue struct {
		queue []fingerprint
		mu    sync.Mutex
	}

	// fingerprint is a helper struct that contains the fingerprint hash and its
	// expiry block height. These objects are enqueued in the save queue and
	// processed by the threadedSaveFingerprintsLoop.
	fingerprint struct {
		hash   crypto.Hash
		expiry types.BlockHeight
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
	fpm, err := ap.newFingerprintManager()
	if err != nil {
		return nil, err
	}
	ap.staticFingerprintManager = fpm

	// Start the save loop
	go fpm.threadedSaveFingerprintsLoop()

	return ap, nil
}

// newFingerprintManager will create a new fingerprint manager, this manager
// uses two files to store the fingerprints on disk.
func (ap *accountsPersister) newFingerprintManager() (_ *fingerprintManager, err error) {
	fm := &fingerprintManager{
		staticCurrentPath:           filepath.Join(ap.h.persistDir, fingerprintsCurrFilename),
		staticNextPath:              filepath.Join(ap.h.persistDir, fingerprintsNxtFilename),
		staticSaveFingerprintsQueue: saveFingerprintsQueue{queue: make([]fingerprint, 0)},
		wakeChan:                    make(chan struct{}, 1),
		h:                           ap.h,
	}

	fm.current, err = ap.openFingerprintBucket(fm.staticCurrentPath)
	if err != nil {
		return nil, errors.AddContext(err, fmt.Sprintf("could not open fingerprint bucket at path %s", fm.staticCurrentPath))
	}

	fm.next, err = ap.openFingerprintBucket(fm.staticNextPath)
	if err != nil {
		return nil, errors.AddContext(err, fmt.Sprintf("could not open fingerprint bucket at path %s", fm.staticNextPath))
	}

	return fm, nil
}

// callLoadData loads all accounts data and fingerprints from disk
func (ap *accountsPersister) callLoadData() (*accountsPersisterData, error) {
	accounts := make(map[string]*account)
	fingerprints := make(map[crypto.Hash]struct{})

	// Load accounts
	err := func() error {
		ap.mu.Lock()
		defer ap.mu.Unlock()
		return ap.loadAccounts(ap.accounts, accounts)
	}()
	if err != nil {
		return nil, err
	}

	// Load fingerprints
	fm := ap.staticFingerprintManager
	err = func() error {
		fm.mu.Lock()
		defer fm.mu.Unlock()
		return errors.Compose(
			ap.loadFingerprints(fm.staticCurrentPath, fingerprints),
			ap.loadFingerprints(fm.staticNextPath, fingerprints),
		)
	}()
	if err != nil {
		return nil, err
	}

	return &accountsPersisterData{
		accounts:     accounts,
		fingerprints: fingerprints,
	}, nil
}

// callSaveAccount will persist the given account data at the location
// corresponding to the given index.
func (ap *accountsPersister) callSaveAccount(data *accountData, index uint32) error {
	ap.managedLockIndex(index)
	defer ap.managedUnlockIndex(index)

	// Get the account data bytes
	accBytes, err := data.bytes()
	if err != nil {
		return errors.AddContext(err, "save account failed, account could not be encoded")
	}

	// Write the data to disk
	_, err = ap.accounts.WriteAt(accBytes, location(index))
	if err != nil {
		panic("Unable to write the ephemeral account to disk.")
	}

	return nil
}

// callQueueSaveFingerprint adds the given fingerprint to the save queue.
func (ap *accountsPersister) callQueueSaveFingerprint(hash crypto.Hash, expiry types.BlockHeight) {
	fm := ap.staticFingerprintManager
	fm.staticSaveFingerprintsQueue.mu.Lock()
	fm.staticSaveFingerprintsQueue.queue = append(fm.staticSaveFingerprintsQueue.queue, fingerprint{hash, expiry})
	fm.staticSaveFingerprintsQueue.mu.Unlock()
	fm.staticWake()
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
			_, results[n] = ap.accounts.WriteAt(zeroBytes, location(index))
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

// callRotateFingerprintBuckets will rotate the fingerprint buckets
func (ap *accountsPersister) callRotateFingerprintBuckets() (err error) {
	fm := ap.staticFingerprintManager
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Close the current fingerprint files, this syncs the files before closing
	if err = fm.close(); err != nil {
		return errors.AddContext(err, "could not close fingerprint files")
	}

	// Swap the fingerprints by renaming the next bucket
	if err = os.Rename(fm.staticNextPath, fm.staticCurrentPath); err != nil {
		return errors.AddContext(err, "could not rename next fingerprint bucket")
	}

	// Reopen files
	fm.current, err = ap.openFingerprintBucket(fm.staticCurrentPath)
	if err != nil {
		return errors.AddContext(err, fmt.Sprintf("could not open fingerprint bucket, path %s", fm.staticCurrentPath))
	}
	fm.next, err = ap.openFingerprintBucket(fm.staticNextPath)
	if err != nil {
		return errors.AddContext(err, fmt.Sprintf("could not open fingerprint bucket, path %s", fm.staticNextPath))
	}

	return nil
}

// managedLockIndex grabs a lock on an (account) index.
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

// managedUnlockIndex releases a lock on an (account) index.
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
			// not much we can do here besides log this critical event
			ap.h.log.Critical(errors.New("could not decode account data"))
			continue // TODO follow-up host alert (?)
		}

		// deleted accounts will decode into an account with 0 as LastTxnTime,
		// we want to skip those so that index becomes free and eventually gets
		// overwritten.
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

// threadedSaveFingerprintsLoop continuously checks if fingerprints got added to
// the queue and will save them. The loop blocks until it receives a message on
// the wakeChan, or until it receives a stop signal.
//
// Note: threadgroup counter must be inside for loop. If not, calling 'Flush'
// on the threadgroup would deadlock.
func (fm *fingerprintManager) threadedSaveFingerprintsLoop() {
	for {
		var workPerformed bool
		func() {
			if err := fm.h.tg.Add(); err != nil {
				return
			}
			defer fm.h.tg.Done()

			fm.staticSaveFingerprintsQueue.mu.Lock()
			if len(fm.staticSaveFingerprintsQueue.queue) == 0 {
				fm.staticSaveFingerprintsQueue.mu.Unlock()
				return
			}

			fp := fm.staticSaveFingerprintsQueue.queue[0]
			fm.staticSaveFingerprintsQueue.queue = fm.staticSaveFingerprintsQueue.queue[1:]
			fm.staticSaveFingerprintsQueue.mu.Unlock()

			err := fm.managedSave(fp)
			if err != nil {
				fm.h.log.Fatal("Could not save fingerprint", err)
			}
			workPerformed = true
		}()

		if workPerformed {
			continue
		}

		select {
		case <-fm.wakeChan:
			continue
		case <-fm.h.tg.StopChan():
			return
		}
	}
}

// managedSave will persist the given fingerprint into the appropriate bucket
func (fm *fingerprintManager) managedSave(fp fingerprint) error {
	// Encode the fingerprint, verify it has the correct size
	fpBytes, err := safeEncode(fp.hash, fingerprintSize)
	if err != nil {
		build.Critical(errors.New("fingerprint size is larger than the expected size"))
		return ErrAccountPersist
	}
	bh := fm.h.BlockHeight()

	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Write into bucket depending on its expiry
	_, max := currentBucketRange(bh)
	if fp.expiry < max {
		_, err := fm.current.Write(fpBytes)
		return err
	}
	_, err = fm.next.Write(fpBytes)
	return err
}

// staticWake is called every time a fingerprint is added to the save queue.
func (fm *fingerprintManager) staticWake() {
	select {
	case fm.wakeChan <- struct{}{}:
	default:
	}
}

// close will safely close the current and next bucket file
func (fm *fingerprintManager) close() error {
	return errors.Compose(
		syncAndClose(fm.current),
		syncAndClose(fm.next),
	)
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

// bytes returns the account data as bytes.
func (a *accountData) bytes() ([]byte, error) {
	// Encode the account, verify it has the correct size
	accBytes, err := safeEncode(*a, accountSize)
	if err != nil {
		build.Critical(errors.AddContext(err, "unexpected ephemeral account size"))
		return nil, err
	}
	return accBytes, nil
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

// location is a helper method that returns the location of the account with
// given index.
func location(index uint32) int64 {
	// metadataPadding is the amount of bytes we pad the metadata with. We pad
	// metadata to ensure writing an account to disk never crosses the 512 bytes
	// boundary, traditionally the size of a sector on disk. Seeing as the
	// sector size is a multiple of the account size we pad the metadata until
	// it's as large as a single account
	metadataPadding := int64(accountSize - persist.FixedMetadataSize)
	return persist.FixedMetadataSize + metadataPadding + int64(uint64(index)*accountSize)
}
