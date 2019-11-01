package host

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// account size is the fixed account size in bytes
const accountSize = 1 << 7

var (
	// accountsFilename is the filename of the file that holds the accounts
	accountsFilename = "accounts.txt"
)

type (
	// account contains all data associated with a single ephemeral account,
	// this data is what gets persisted to disk
	account struct {
		id      types.SiaPublicKey
		balance types.Currency
		updated int64
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
func (h *Host) newAccountsPersister(deps modules.Dependencies) (*accountsPersister, error) {
	ap := &accountsPersister{
		lockedIndices: make(map[uint32]*indexLock),
		deps:          deps,
		h:             h,
	}

	// Create the accounts file if it doesn't exist yet
	file, err := ap.deps.OpenFile(filepath.Join(h.persistDir, accountsFilename), os.O_RDWR|os.O_TRUNC|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	ap.accounts = file

	// TODO Create the fingerprint buckets

	return ap, nil
}

// Close will cleanly shutdown the account persister
func (ap *accountsPersister) Close() error {
	ap.accounts.Close()
	return nil
}

// CallSaveAccount writes away the data for a single account to disk
func (ap *accountsPersister) callSaveAccount(index uint32, a *account) error {
	ap.managedLockIndex(index)
	defer ap.managedUnlockIndex(index)

	bytes, err := a.Bytes()
	if err != nil {
		return err
	}

	_, err = ap.accounts.WriteAt(bytes, int64(uint64(index)*accountSize))
	if err != nil {
		return err
	}

	return nil
}

// callLoadAccountsData loads the saved account data from disk and returns it
// the account data is two-part, the
func (ap *accountsPersister) callLoadAccountsData() accountsData {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	var data accountsData
	data.Accounts = make(map[string]types.Currency)
	data.AccountIndices = make(map[string]uint32)
	data.AccountUpdated = make(map[string]int64)
	data.Fingerprints = make(map[crypto.Hash]struct{})

	bytes, err := ap.deps.ReadFile(filepath.Join(ap.h.persistDir, accountsFilename))
	if err != nil {
		ap.h.log.Println("ERROR while reading accounts file", err)
		return data
	}

	var index uint32 = 0
	for i := 0; i < len(bytes); i += accountSize {
		a := account{}
		if err := a.LoadBytes(bytes[i : i+accountSize]); err != nil {
			ap.h.log.Println("ERROR: could not properly unmarshal account", err)
			index++
			continue
		}

		id := a.id.String()
		data.Accounts[id] = a.balance
		data.AccountIndices[id] = index
		data.AccountUpdated[id] = a.updated
		index++
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

// Bytes returns the account as a byte slice of fixed length
func (a *account) Bytes() ([]byte, error) {
	buf := new(bytes.Buffer)
	err := a.MarshalSia(buf)
	if err != nil {
		return nil, err
	}

	accBytes := make([]byte, accountSize)
	copy(accBytes, buf.Bytes())
	return accBytes, nil
}

// LoadBytes will load the account data from the given byte slice
func (a *account) LoadBytes(data []byte) error {
	buf := new(bytes.Buffer)
	buf.Write(data)

	err := a.UnmarshalSia(buf)
	if err != nil {
		return err
	}
	return nil
}

// MarshalSia implements the encoding.SiaMarshaler interface.
func (a *account) MarshalSia(w io.Writer) error {
	e := encoding.NewEncoder(w)
	a.id.MarshalSia(e)
	a.balance.MarshalSia(e)
	e.WriteUint64(uint64(a.updated))
	return e.Err()
}

// UnmarshalSia implements the encoding.SiaUnmarshaler interface.
func (a *account) UnmarshalSia(r io.Reader) error {
	d := encoding.NewDecoder(r, encoding.DefaultAllocLimit)
	a.id.UnmarshalSia(d)
	a.balance.UnmarshalSia(d)
	a.updated = int64(d.NextUint64())
	return d.Err()
}
