package host

import (
	"io/ioutil"
	"math/bits"
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
	"gitlab.com/NebulousLabs/fastrand"
)

const (
	// accountSize is the fixed account size in bytes
	accountSize = 1 << 7 // 128 bytes

	// accountsFilename is the filename of the file that holds the accounts
	accountsFilename = "accounts.txt"

	// fingerprintSize is the fixed fingerprint size in bytes
	fingerprintSize = 1 << 6 // 64 bytes

	// filenames for fingerprint buckets
	fingerprintsCurrFilename = "fingerprints_c.db"
	fingerprintsNxtFilename  = "fingerprints_n.db"

	// bucketBlockRange defines the range of blocks a bucket spans
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
	// accountsPersister is a subsystem that will persist all account data
	accountsPersister struct {
		accounts                 modules.File
		staticFingerprintManager *fingerprintManager

		indexLocks     map[uint32]*indexLock
		indexBitfields []uint64

		mu sync.Mutex
		h  *Host
	}

	// accountsPersisterData contains all data that the account manager needs
	// when it is reloaded
	accountsPersisterData struct {
		accounts     map[string]*account
		fingerprints map[crypto.Hash]struct{}
	}

	// accountData contains all data persisted for a single account
	accountData struct {
		Id          types.SiaPublicKey
		Balance     types.Currency
		lastTxnTime int64
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

// newFileBucket will create a new bucket for given size and blockheight
func (ap *accountsPersister) newFingerprintManager(bucketBlockRange int) (_ *fingerprintManager, err error) {
	currPath := filepath.Join(ap.h.persistDir, fingerprintsCurrFilename)
	nextPath := filepath.Join(ap.h.persistDir, fingerprintsNxtFilename)

	fm := &fingerprintManager{
		bucketBlockRange: bucketBlockRange,
		currentPath:      currPath,
		nextPath:         nextPath,
	}

	if fm.current, err = ap.openFingerprintBucket(fm.currentPath); err != nil {
		return nil, errors.AddContext(err, "could not open fingerprint bucket")
	}
	if fm.next, err = ap.openFingerprintBucket(fm.nextPath); err != nil {
		return nil, errors.AddContext(err, "could not open fingerprint bucket")
	}

	return fm, nil
}

// callSaveAccount writes the data for a single ephemeral account to disk
func (ap *accountsPersister) callSaveAccount(index uint32, data *accountData) error {
	ap.managedLockIndex(index)
	defer ap.managedUnlockIndex(index)

	accBytes, err := data.bytes()
	if err != nil {
		return err
	}

	location := persist.FixedMetadataSize + int64(uint64(index)*accountSize)
	_, err = ap.accounts.WriteAt(accBytes, location)
	if err != nil {
		return errors.AddContext(err, "could not save account")
	}

	return nil
}

// callSaveFingerprint writes the fingerprint to disk
func (ap *accountsPersister) callSaveFingerprint(fp crypto.Hash, expiry, currentBlockHeight types.BlockHeight) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	if err := ap.staticFingerprintManager.save(fp, expiry, currentBlockHeight); err != nil {
		return errors.AddContext(err, "could not save fingerprint")
	}

	return nil
}

// callLoadData loads the saved account data from disk and returns it
func (ap *accountsPersister) callLoadData() (*accountsPersisterData, error) {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	// Load accounts & build the accounts index
	accounts := make(map[string]*account)
	if err := ap.loadAccounts(ap.accounts, accounts); err != nil {
		return nil, err
	}
	ap.buildAccountIndex(len(accounts))

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

// callAssignFreeIndex will return the next available account index
func (ap *accountsPersister) callAssignFreeIndex() uint32 {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	var i, pos int = 0, -1

	// Go through all bitmaps in random order to find a free index
	full := ^uint64(0)
	for i := range fastrand.Perm(len(ap.indexBitfields)) {
		if ap.indexBitfields[i] != full {
			pos = bits.TrailingZeros(uint(^ap.indexBitfields[i]))
			break
		}
	}

	// Add a new bitfield if all account indices are taken
	if pos == -1 {
		pos = 0
		ap.indexBitfields = append(ap.indexBitfields, 1<<uint(pos))
	} else {
		ap.indexBitfields[i] = ap.indexBitfields[i] << uint(pos)
	}

	// Calculate the index my multiplying the bitfield index by 64 (seeing as
	// the bitfields are of type uint64) and adding the position
	index := uint32((i * 64) + pos)

	return index
}

// callReleaseIndex will unset the bit corresponding to given index
func (ap *accountsPersister) callReleaseIndex(index uint32) {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	i := index / 64
	pos := index % 64
	var mask uint64 = ^(1 << pos)
	ap.indexBitfields[i] &= mask
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

	// Release the lock on the sector.
	il, exists := ap.indexLocks[index]
	if !exists {
		ap.h.log.Critical("Unlock of an account index that is not locked.")
		return
	}
	il.waiting--
	il.mu.Unlock()

	// If nobody else is trying to lock the sector, perform garbage collection.
	if il.waiting == 0 {
		delete(ap.indexLocks, index)
	}
}

// buildAccountIndex will initialize bitfields representing all ephemeral
// accounts, the index is used to recycle account indices
func (ap *accountsPersister) buildAccountIndex(numAccounts int) {
	n := numAccounts / 64
	for i := 0; i < n; i++ {
		ap.indexBitfields = append(ap.indexBitfields, ^uint64(0))
	}

	r := numAccounts % 64
	if r > 0 {
		ap.indexBitfields = append(ap.indexBitfields, (1<<uint(r))-1)
	}
}

// openAccountsFile wraps openFileWithMetadata and specifies the appropriate
// metadata header and flags for the accounts file
func (ap *accountsPersister) openAccountsFile(path string) (modules.File, error) {
	// open file in read-write mode and create if it does not exist yet
	return ap.openFileWithMetadata(path, os.O_RDWR|os.O_CREATE, accountMetadata)
}

// openFingerprintBucket wraps openFileWithMetadata and specifies the
// appropriate metadata header and flags for the fingerprints bucket file
func (ap *accountsPersister) openFingerprintBucket(path string) (modules.File, error) {
	// open file in append-only mode and create if it does not exist yet
	return ap.openFileWithMetadata(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, fingerprintsMetadata)

}

// openFileWithMetadata will open the file at given path and write the metadata
// header to it if the file did not exist prior to calling this method
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

// tryRotateFingerprintBuckets will rotate the fingerprint bucket files, but
// only if the current block height exceeds the current bucket's threshold
func (ap *accountsPersister) tryRotateFingerprintBuckets(currentBlockHeight types.BlockHeight) (err error) {
	fm := ap.staticFingerprintManager
	threshold := calculateBucketThreshold(currentBlockHeight, fm.bucketBlockRange)

	// If the current blockheihgt is less than the calculated threshold, we do
	// not need to rotate the bucket files on the file system
	if currentBlockHeight < threshold {
		return nil
	}

	// If it is larger, cleanly close the current fingerprint bucket
	if err = syncAndClose(fm.current); err != nil {
		return err
	}

	// Rename the next bucket to the current bucket
	if err = os.Rename(fm.nextPath, fm.currentPath); err != nil {
		return err
	}
	fm.current = fm.next

	// Create a new next bucket
	fm.next, err = ap.openFingerprintBucket(fm.nextPath)
	if err != nil {
		return err
	}

	return nil
}

// close will cleanly shutdown the account persister's open file handles
func (ap *accountsPersister) close() error {
	err1 := ap.accounts.Sync()
	err2 := ap.accounts.Close()
	err3 := ap.staticFingerprintManager.close()
	return errors.Compose(err1, err2, err3)
}

// loadAccounts will load the accounts from the file into the map
func (ap *accountsPersister) loadAccounts(file modules.File, m map[string]*account) error {
	bytes, err := ioutil.ReadFile(file.Name())
	if err != nil {
		return errors.AddContext(err, "could not read accounts file")
	}

	var accIndex uint32 = 0
	for i := persist.FixedMetadataSize; i < len(bytes); i += accountSize {
		var data accountData
		if err := encoding.Unmarshal(bytes[i:i+accountSize], data); err != nil {
			// No cause for error, just log it
			ap.h.log.Debugln("ERROR: could not decode account", err)
			continue
		}
		account := data.account(accIndex)
		m[account.id] = account
		accIndex++
	}

	return nil
}

// loadFingerprints will load the fingerprints from the file into the map
func (ap *accountsPersister) loadFingerprints(path string, m map[crypto.Hash]struct{}) error {
	bytes, err := ioutil.ReadFile(path)
	if err != nil {
		return errors.AddContext(err, "could not read fingerprints file")
	}

	for i := persist.FixedMetadataSize; i < len(bytes); i += fingerprintSize {
		var fp crypto.Hash
		if err := encoding.Unmarshal(bytes[i:i+fingerprintSize], &fp); err != nil {
			// No cause for error, just log it
			ap.h.log.Debugln("ERROR: could not decode fingerprint", err)
			continue
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
		lastTxnTime: a.lastTxnTime,
	}
}

// account transforms the accountData we loaded from disk into an account we
// keep in memory
func (a *accountData) account(index uint32) *account {
	return &account{
		id:          a.Id.String(),
		balance:     a.Balance,
		index:       index,
		lastTxnTime: a.lastTxnTime,
	}
}

// bytes returns the account data as an array of bytes.
func (a *accountData) bytes() ([]byte, error) {
	// Sanity check, ensure the encoded account data is not too large
	accMar := encoding.Marshal(a)
	if len(accMar) > accountSize {
		build.Critical(errors.New("ERROR: ephemeral account size is larger than the expected size"))
		return nil, ErrAccountPersist
	}

	accBytes := make([]byte, accountSize)
	copy(accBytes, accMar)
	return accBytes, nil
}

// save will add the given fingerprint to the appropriate bucket
func (fm *fingerprintManager) save(fp crypto.Hash, expiry, currentBlockHeight types.BlockHeight) (err error) {
	// Sanity check, ensure the encoded fingerprint is not too large
	fpMar := encoding.Marshal(fp)
	if len(fpMar) > fingerprintSize {
		build.Critical(errors.New("ERROR: fingerprint size is larger than the expected size"))
		return ErrAccountPersist
	}

	fpBytes := make([]byte, fingerprintSize)
	copy(fpBytes, fpMar)

	threshold := calculateBucketThreshold(currentBlockHeight, fm.bucketBlockRange)
	if expiry <= threshold {
		_, err := fm.current.Write(fpBytes)
		return err
	}

	_, err = fm.next.Write(fpBytes)
	return err
}

// close will close all open files
func (fm *fingerprintManager) close() error {
	return errors.Compose(
		syncAndClose(fm.current),
		syncAndClose(fm.next),
	)
}

// calculateBucketThreshold will calculate the appropriate threshold given the
// current blockheight and the bucket block range
func calculateBucketThreshold(currentBlockHeight types.BlockHeight, bucketBlockRange int) types.BlockHeight {
	cbh := uint64(currentBlockHeight)
	bbr := uint64(bucketBlockRange)
	threshold := cbh + (bbr - (cbh % bbr))
	return types.BlockHeight(threshold)
}

// syncAndClose will sync and close the given file
func syncAndClose(file modules.File) error {
	return errors.Compose(
		file.Sync(),
		file.Close(),
	)
}
