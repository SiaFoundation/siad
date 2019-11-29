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
	// accountsPersister is a subsystem that will persist all account data
	accountsPersister struct {
		accounts                 modules.File
		staticFingerprintManager *fingerprintManager

		indexLocks     map[uint32]*indexLock
		indexBitfields []uint64

		mu sync.Mutex
		h  *Host
	}

	// accountsPersisterData contains all data that the accountsPersister saves
	// to disk
	accountsPersisterData struct {
		accounts     map[string]*account
		fingerprints map[crypto.Hash]struct{}
	}

	// accountData contains all data persisted for a single account
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

// newFingerprintManager will create a new fingerprint manager, this manager
// uses two files to store the fingerprints on disk
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

// callSaveAccount writes the account data to disk
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
		return errors.AddContext(err, "failed to write to accounts file")
	}
	if err = ap.accounts.Sync(); err != nil {
		return errors.AddContext(err, "failed to sync accounts file")
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

// callLoadData loads the accounts data from disk and returns it
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

// buildAccountIndex will initialize bitfields representing all ephemeral
// accounts, the index is used to recycle account indices when the account
// expires.
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
		LastTxnTime: a.lastTxnTime,
	}
}

// account transforms the accountData we loaded from disk into an account we
// keep in memory
func (a *accountData) account(index uint32) *account {
	return &account{
		id:          a.Id.String(),
		balance:     a.Balance,
		lastTxnTime: a.LastTxnTime,
		index:       index,
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

// save will persist the given fingerprint into the appropriate bucket
func (fm *fingerprintManager) save(fp crypto.Hash, expiry, currentBlockHeight types.BlockHeight) (err error) {
	// Sanity check, ensure the encoded fingerprint is not too large
	fpMar := encoding.Marshal(fp)
	if len(fpMar) > fingerprintSize {
		build.Critical(errors.New("ERROR: fingerprint size is larger than the expected size"))
		return ErrAccountPersist
	}

	fpBytes := make([]byte, fingerprintSize)
	copy(fpBytes, fpMar)

	threshold := calculateExpiryThreshold(currentBlockHeight, fm.bucketBlockRange)
	if expiry <= threshold {
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

// syncAndClose will sync and close the given file
func syncAndClose(file modules.File) error {
	return errors.Compose(
		file.Sync(),
		file.Close(),
	)
}
