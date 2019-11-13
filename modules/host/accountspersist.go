package host

import (
	"io/ioutil"
	"math/bits"
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

const (
	// accountSize is the fixed account size in bytes
	accountSize = 1 << 7

	// fingerprintSize is the fixed fingerprint size in bytes
	fingerprintSize = 1 << 6

	// accountsFilename is the filename of the file that holds the accounts
	accountsFilename = "accounts.txt"

	// filenames for fingerprint buckets
	fingerprintsCurrFilename = "fingerprints_c.txt"
	fingerprintsNxtFilename  = "fingerprints_n.txt"

	// headerOffset is the size of the header in bytes
	headerOffset = 32

	// bucketBlockRange defines the range of blocks a bucket spans
	bucketBlockRange = 20

	// appendOnlyFlag specifies the flags to Openfile for fingerprint buckets,
	// which are created if they do not exist and are append only
	appendOnlyFlag = os.O_RDWR | os.O_CREATE | os.O_APPEND
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

	// accountData contains all data persisted for a single account
	accountData struct {
		Id      types.SiaPublicKey
		Balance types.Currency
		LastTxn int64
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
		currentThreshold types.BlockHeight
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
	if ap.accounts, err = ap.openFileWithMetadata(path, os.O_RDWR|os.O_CREATE, accountMetadata); err != nil {
		return nil, err
	}

	// Create the fingerprint manager
	if ap.staticFingerprintManager, err = ap.newFingerprintManager(bucketBlockRange); err != nil {
		return nil, err
	}

	// Load the accounts data onto the account manager
	if err := ap.managedLoadData(am); err != nil {
		return nil, err
	}

	return ap, nil
}

// newFileBucket will create a new bucket for given size and blockheight
func (ap *accountsPersister) newFingerprintManager(bucketBlockRange int) (_ *fingerprintManager, err error) {
	dir := ap.h.persistDir
	fM := &fingerprintManager{
		bucketBlockRange: bucketBlockRange,
		currentPath:      filepath.Join(dir, fingerprintsCurrFilename),
		currentThreshold: calculateBucketThreshold(ap.h.blockHeight, bucketBlockRange),
		nextPath:         filepath.Join(dir, fingerprintsNxtFilename),
	}

	// Open the current fingerprints file in append only mode
	if fM.current, err = ap.openFileWithMetadata(fM.currentPath, appendOnlyFlag, fingerprintsMetadata); err != nil {
		return nil, err
	}

	// Open the next fingerprints file in append only mode
	if fM.next, err = ap.openFileWithMetadata(fM.nextPath, appendOnlyFlag, fingerprintsMetadata); err != nil {
		return nil, err
	}

	return fM, nil
}

// callSaveAccount writes away the data for a single ephemeral account to disk
func (ap *accountsPersister) callSaveAccount(a *account) error {
	ap.managedLockIndex(a.index)
	defer ap.managedUnlockIndex(a.index)

	accBytes := make([]byte, accountSize)
	copy(accBytes, encoding.Marshal(a.transformToAccountData()))
	_, err := ap.accounts.WriteAt(accBytes, headerOffset+int64(uint64(a.index)*accountSize))
	if err != nil {
		return err
	}

	return nil
}

// callSaveFingerprint writes away the fingerprint to disk
func (ap *accountsPersister) callSaveFingerprint(fp crypto.Hash, expiry types.BlockHeight) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	if err := ap.staticFingerprintManager.save(fp, expiry); err != nil {
		ap.h.log.Println("could not save fingerprint:", err)
		return err
	}

	return nil
}

// managedLoadData loads the saved account data from disk and decorates it onto
// the account manager
func (ap *accountsPersister) managedLoadData(am *accountManager) error {
	accounts := make(map[string]*account)
	fingerprints := make(map[crypto.Hash]struct{})

	ap.mu.Lock()
	defer am.callSetData(accounts, fingerprints)
	defer ap.mu.Unlock()

	// Read accounts file
	accPath := filepath.Join(ap.h.persistDir, accountsFilename)
	accBytes, err := ap.h.dependencies.ReadFile(accPath)
	if err != nil {
		ap.h.log.Println("ERROR: could not read accounts", err)
		return err
	}

	// Unmarshal the accounts into an accounts map indexed by account id
	var index uint32 = 0
	for i := headerOffset; i < len(accBytes); i += accountSize {
		accData := accountData{}
		if err := encoding.Unmarshal(accBytes[i:i+accountSize], accData); err != nil {
			// Not being able to load a single ephemeral account is no cause for
			// returning an error
			ap.h.log.Println("ERROR: could not read accounts", err)
			continue
		}
		acc := accData.transformToAccount(index)
		accounts[acc.id] = acc
		index++
	}

	// Load all fingerprints into a map
	if err = ap.staticFingerprintManager.populateMap(fingerprints); err != nil {
		ap.h.log.Println("ERROR: could not load fingerprints", err)
		return err
	}

	ap.buildAccountIndex(int(index))

	return nil
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

// openFileWithMetadata will open the file at given path and write the metadata
// header to it if the file did not exist prior to calling this method
func (ap *accountsPersister) openFileWithMetadata(path string, flags int, metadata persist.FixedMetadata) (modules.File, error) {
	_, statErr := os.Stat(path)

	// Open the file, create it if it does not exist yet
	file, err := ap.h.dependencies.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, err
	}

	// If it did not exist prior to calling this method, write header metadata
	if os.IsNotExist(statErr) {
		_, err = file.Write(encoding.Marshal(metadata))
		if err != nil {
			return nil, err
		}
	}

	return file, nil
}

// close will cleanly shutdown the account persister's open file handles
func (ap *accountsPersister) close() error {
	err1 := ap.accounts.Sync()
	err2 := ap.accounts.Close()
	err3 := ap.staticFingerprintManager.close()
	return errors.Compose(err1, err2, err3)
}

// transformToAccountData transforms the account into an accountData struct
// which will contain all data we persist to disk
func (a *account) transformToAccountData() accountData {
	spk := types.SiaPublicKey{}
	spk.LoadString(a.id)
	return accountData{
		Id:      spk,
		Balance: a.balance,
		LastTxn: a.lastTxn,
	}
}

// transformToAccount transforms the accountData we loaded from disk into an
// account we keep in memory
func (a *accountData) transformToAccount(index uint32) *account {
	return &account{
		id:      a.Id.String(),
		balance: a.Balance,
		index:   index,
		lastTxn: a.LastTxn,
	}
}

// save will add the given fingerprint to the appropriate bucket
func (fM *fingerprintManager) save(fp crypto.Hash, expiry types.BlockHeight) (err error) {
	bytes := make([]byte, fingerprintSize)
	copy(bytes, encoding.Marshal(fp))

	if expiry <= fM.currentThreshold {
		_, err = fM.current.Write(bytes)
		return err
	}

	_, err = fM.next.Write(bytes)
	return err
}

// populateMap will read all the fingerprints from both buckets and
// populate the given map
func (fM *fingerprintManager) populateMap(m map[crypto.Hash]struct{}) error {
	bc, err := ioutil.ReadAll(fM.current)
	if err != nil {
		return err
	}

	bn, err := ioutil.ReadAll(fM.next)
	if err != nil {
		return err
	}

	buf := append(bc, bn...)
	for i := headerOffset; i < len(buf); i += fingerprintSize {
		fp := crypto.Hash{}
		_ = encoding.Unmarshal(buf[i:i+fingerprintSize], &fp)
		m[fp] = struct{}{}
	}

	return nil
}

// tryRotate will rotate the fingerprint bucket files, but only if the current
// block height exceeds the current bucket's threshold
func (fM *fingerprintManager) tryRotate(currentBlockHeight types.BlockHeight) (err error) {
	// If the current blockheihgt is less or equal than the current bucket's
	// threshold, we do not need to rotate the bucket files on the file system
	if currentBlockHeight <= fM.currentThreshold {
		return nil
	}

	// If it is larger, cleanly close the current fingerprint bucket
	if err = syncAndClose(fM.current); err != nil {
		return err
	}

	// Rename the next bucket to the current bucket
	if err = os.Rename(fM.nextPath, fM.currentPath); err != nil {
		return err
	}
	fM.current = fM.next

	// Create a new next bucket
	fM.next, err = os.OpenFile(fM.nextPath, appendOnlyFlag, 0600)
	if err != nil {
		return err
	}

	// Recalculate the threshold
	fM.currentThreshold = calculateBucketThreshold(currentBlockHeight, fM.bucketBlockRange)

	return nil
}

// close will close all open files
func (fM *fingerprintManager) close() error {
	return errors.Compose(
		syncAndClose(fM.current),
		syncAndClose(fM.next),
	)
}

// syncAndClose will sync and close the given file
func syncAndClose(file modules.File) error {
	return errors.Compose(
		file.Sync(),
		file.Close(),
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
