package host

import (
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
)

var (
	// accountsFilename is the filename of the file that holds the accounts
	accountsFilename = "accounts.json"

	// accountsMetadata is the header that is used when writing the account
	// manager's state to disk.
	accountsMetadata = persist.Metadata{
		Header:  "Accounts Persistence",
		Version: "1.4.1.3",
	}
)

type (
	// accountsData contains all account manager data we want to persist
	accountsData struct {
		Accounts     map[string]types.Currency
		TotalExpired types.Currency
	}

	// accountsPersister is a subsystem that will persist all account data
	accountsPersister struct {
		location     string
		mu           sync.Mutex
		dependencies modules.Dependencies
		hostUtils
	}
)

// newAccountsPersister returns a new account persister
func (h *Host) newAccountsPersister(dependencies modules.Dependencies) (*accountsPersister, error) {
	ap := &accountsPersister{
		location:     filepath.Join(h.persistDir, accountsFilename),
		dependencies: dependencies,
		hostUtils:    h.hostUtils,
	}

	// Create the perist directory if it does not yet exist.
	err := ap.dependencies.MkdirAll(h.persistDir, 0700)
	if err != nil {
		return nil, err
	}

	return ap, nil
}

// callSaveAccountsData saves the account data to disk
func (ap *accountsPersister) callSaveAccountsData(data *accountsData) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	return ap.dependencies.SaveFileSync(accountsMetadata, data, ap.location)
}

// callLoadAccountData loads the saved account data from disk and returns it
func (ap *accountsPersister) callLoadAccountData() *accountsData {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	var data accountsData
	data.Accounts = make(map[string]types.Currency)
	data.TotalExpired = types.ZeroCurrency

	err := ap.dependencies.LoadFile(accountsMetadata, &data, ap.location)
	if err != nil {
		ap.log.Println("Unable to load accounts data:", err)
	}

	return &data
}
