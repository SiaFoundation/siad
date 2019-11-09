package host

import (
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

const (
	// accountSize is the fixed account size in bytes
	accountSize = 1 << 7

	// accountsFilename is the filename of the file that holds the accounts
	accountsFilename = "accounts.txt"

	// fingerprintSize is the fixed fingerprint size in bytes
	fingerprintSize = 1 << 6

	// filenames for fingerprint buckets
	currentBucketFilename = "fingerprints_c.txt"
	nextBucketFilename    = "fingerprints_n.txt"

	// headerOffset is the size of the header in bytes
	headerOffset = 32
)

var (
	// accountMetadata contains the header and version strings that identify the
	// accounts persist file.
	accountMetadata = fixedMetadata{
		Header:  types.Specifier{'A', 'c', 'c', 'o', 'u', 'n', 't', 's'},
		Version: types.Specifier{'1', '.', '4', '.', '1', '.', '3'},
	}

	// fingerprintsMetadata contains the header and version strings that
	// identify the fingerprints persist file.
	fingerprintsMetadata = fixedMetadata{
		Header:  types.Specifier{'F', 'i', 'n', 'g', 'e', 'r', 'P', 'r', 'i', 'n', 't', 's'},
		Version: types.Specifier{'1', '.', '4', '.', '1', '.', '3'},
	}
)

type (
	// accountsPersister is a subsystem that will persist all account data
	accountsPersister struct {
		accounts      modules.File
		fingerprints  *fileBucket
		lockedIndices map[uint32]*indexLock

		mu   sync.Mutex
		deps modules.Dependencies
		h    *Host
	}

	// fixedMetadata contains the persist metadata
	fixedMetadata struct {
		Header  types.Specifier
		Version types.Specifier
	}

	// accountsData contains all data related to the ephemeral accounts
	accountsData struct {
		Accounts     map[string]*account
		Fingerprints map[crypto.Hash]struct{}
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
)

// newAccountsPersister returns a new account persister
func (h *Host) newAccountsPersister(deps modules.Dependencies) (ap *accountsPersister, err error) {
	ap = &accountsPersister{
		lockedIndices: make(map[uint32]*indexLock),
		deps:          deps,
		h:             h,
	}

	// Open the accounts file
	path := filepath.Join(h.persistDir, accountsFilename)
	ap.accounts, err = ap.openFileWithMetadata(path, os.O_RDWR|os.O_CREATE, accountMetadata)
	if err != nil {
		return nil, err
	}

	// Open the 'current' fingerprints file
	path = filepath.Join(h.persistDir, currentBucketFilename)
	fileA, err := ap.openFileWithMetadata(path, bucketFlag, fingerprintsMetadata)
	if err != nil {
		return nil, err
	}

	// Open the 'next' fingerprints file
	path = filepath.Join(h.persistDir, nextBucketFilename)
	fileB, err := ap.openFileWithMetadata(path, bucketFlag, fingerprintsMetadata)
	if err != nil {
		return nil, err
	}

	// Create a new file bucket for the fingerprint files
	ap.fingerprints = newFileBucket(h.persistDir, fileA, fileB, h.blockHeight)

	return ap, nil
}

// Close will cleanly shutdown the account persister's open file handles
func (ap *accountsPersister) Close() error {
	files := map[string]*modules.File{
		"accounts":             &ap.accounts,
		"current fingerprints": &ap.fingerprints.current,
		"next fingerprints":    &ap.fingerprints.next,
	}

	for name, file := range files {
		if err := (*file).Sync(); err != nil {
			ap.h.log.Printf("could not synchronize %v file: %v \n", name, err)
		}
		if err := (*file).Close(); err != nil {
			ap.h.log.Printf("could not close %v file: %v \n", name, err)
		}
	}

	return nil
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
func (ap *accountsPersister) callSaveFingerprint(fp *crypto.Hash, expiry types.BlockHeight) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	if err := ap.fingerprints.save(fp, expiry); err != nil {
		ap.h.log.Println("could not save fingerprint:", err)
		return err
	}

	return nil
}

// callLoadAccountsData loads the saved account data from disk and returns it
// the account data is two-part, the
func (ap *accountsPersister) callLoadAccountsData() accountsData {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	data := accountsData{
		Accounts:     make(map[string]*account),
		Fingerprints: make(map[crypto.Hash]struct{}),
	}

	// Read account data
	bytes, err := ap.deps.ReadFile(filepath.Join(ap.h.persistDir, accountsFilename))
	if err != nil {
		ap.h.log.Println("ERROR: could not read accounts", err)
		return data
	}

	var index uint32 = headerOffset
	for i := 0; i < len(bytes); i += accountSize {
		aD := accountData{}
		if err := encoding.Unmarshal(bytes[i:i+accountSize], aD); err != nil {
			ap.h.log.Println("ERROR: could not properly unmarshal account", err)
			index++
			continue
		}
		acc := aD.transformToAccount(index)
		data.Accounts[acc.id] = acc
		index++
	}

	// Load fingerprint data
	fps, err := ap.fingerprints.all()
	if err != nil {
		ap.h.log.Println("ERROR: could not load fingerprints", err)
		return data
	}
	for _, fp := range fps {
		data.Fingerprints[*fp] = struct{}{}
	}

	return data
}

// managedLockAccount grabs a lock on an (account) index.
func (ap *accountsPersister) managedLockIndex(index uint32) {
	ap.mu.Lock()
	il, exists := ap.lockedIndices[index]
	if exists {
		il.waiting++
	} else {
		il = &indexLock{
			waiting: 1,
		}
		ap.lockedIndices[index] = il
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
	il, exists := ap.lockedIndices[index]
	if !exists {
		ap.h.log.Critical("Unlock of an account index that is not locked.")
		return
	}
	il.waiting--
	il.mu.Unlock()

	// If nobody else is trying to lock the sector, perform garbage collection.
	if il.waiting == 0 {
		delete(ap.lockedIndices, index)
	}
}

// openFileWithMetadata will open the file at given path and write the metadata
// header to it if the file did not exist prior to calling this method
func (ap *accountsPersister) openFileWithMetadata(path string, flags int, metadata fixedMetadata) (modules.File, error) {
	_, statErr := os.Stat(path)

	// Open the file, create it if it does not exist yet
	file, err := ap.deps.OpenFile(path, flags, 0600)
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
