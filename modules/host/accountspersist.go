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

// TODO add peristence metadata headers for accounts and fingerprints

const (
	// accountSize is the fixed account size in bytes
	accountSize = 1 << 7

	// accountsFilename is the filename of the file that holds the accounts
	accountsFilename = "accounts.txt"
)

type (
	// account contains all data associated with a single ephemeral account,
	// this data is what gets persisted to disk
	account struct {
		Id      types.SiaPublicKey
		Balance types.Currency
		Updated int64
	}

	// accountsData contains all account manager data we want to persist
	accountsData struct {
		Accounts       map[string]types.Currency
		AccountIndices map[string]uint32
		AccountUpdated map[string]int64
		Fingerprints   map[crypto.Hash]struct{}
	}

	// accountsPersister is a subsystem that will persist all account data
	accountsPersister struct {
		accounts      modules.File
		fingerprints  *fileBucket
		lockedIndices map[uint32]*indexLock

		mu   sync.Mutex
		deps modules.Dependencies
		h    *Host
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

	// Open the accounts file, create file if it doesn't exist yet
	ap.accounts, err = ap.deps.OpenFile(filepath.Join(h.persistDir, accountsFilename), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}

	// Open the fingerprint buckets
	ap.fingerprints, err = newFileBucket(h.persistDir, h.blockHeight)
	if err != nil {
		return nil, err
	}

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
func (ap *accountsPersister) callSaveAccount(index uint32, a *account) error {
	ap.managedLockIndex(index)
	defer ap.managedUnlockIndex(index)

	accBytes := make([]byte, accountSize)
	copy(accBytes, encoding.Marshal(*a))
	_, err := ap.accounts.WriteAt(accBytes, int64(uint64(index)*accountSize))
	if err != nil {
		return err
	}

	return nil
}

// callSaveFingerprint writes away the fingerprint to disk
func (ap *accountsPersister) callSaveFingerprint(fp *fingerprint) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	if err := ap.fingerprints.save(fp); err != nil {
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
		Accounts:       make(map[string]types.Currency),
		AccountIndices: make(map[string]uint32),
		AccountUpdated: make(map[string]int64),
		Fingerprints:   make(map[crypto.Hash]struct{}),
	}

	// Read account data
	bytes, err := ap.deps.ReadFile(filepath.Join(ap.h.persistDir, accountsFilename))
	if err != nil {
		ap.h.log.Println("ERROR: could not read accounts", err)
		return data
	}

	var index uint32 = 0
	for i := 0; i < len(bytes); i += accountSize {
		a := account{}
		if err := encoding.Unmarshal(bytes[i:i+accountSize], a); err != nil {
			ap.h.log.Println("ERROR: could not properly unmarshal account", err)
			index++
			continue
		}

		id := a.Id.String()
		data.Accounts[id] = a.Balance
		data.AccountIndices[id] = index
		data.AccountUpdated[id] = a.Updated
		index++
	}

	// Load fingerprint data
	fps, err := ap.fingerprints.all()
	if err != nil {
		ap.h.log.Println("ERROR: could not load fingerprints", err)
		return data
	}
	for _, fp := range fps {
		data.Fingerprints[fp.Hash] = struct{}{}
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
