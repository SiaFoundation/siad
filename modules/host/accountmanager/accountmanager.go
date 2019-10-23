package accountmanager

import (
	"crypto"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	siasync "gitlab.com/NebulousLabs/Sia/sync"
	"gitlab.com/NebulousLabs/Sia/types"
)

// The AccountManager is a subsystem responsible for managing ephemeral
// accounts.These accounts are essentially off-chain balances that can used
// as a new form of payment.
//
// The host manages these accounts in a trusted way. Meaning the account
// owner has no recourse if the host decides to steal the funds.
//
// Every interaction with an account that happens through the account
// manager will need to acquire a lock on said account. These locks are kept
// by the account manager as well.
type AccountManager struct {
	accounts map[string]*EphemeralAccount
	receipts []crypto.Hash

	// Mutex necessary to ensure that no two ephemeral accounts get created
	// at the same time when a deposit is made into a non-existing account
	mu sync.Mutex

	// Utilities.
	dependencies modules.Dependencies
	log          *persist.Logger
	persistDir   string
	tg           siasync.ThreadGroup
}

// New creates a new AccountManager
func New(persistDir string) (*AccountManager, error) {
	return newAccountManager(new(modules.ProductionDependencies), persistDir)
}

// Close will cleanly shut down the account manager
func (am *AccountManager) Close() error {
	return build.ExtendErr("error while stopping account manager", am.tg.Stop())
}

// newAccountManager returns an account manager that is ready to be used with
// the provided dependencies
func newAccountManager(dependencies modules.Dependencies, persistDir string) (*AccountManager, error) {
	am := &AccountManager{
		accounts:     make(map[string]*EphemeralAccount),
		dependencies: dependencies,
		persistDir:   persistDir,
	}
	am.tg.AfterStop(func() {
		dependencies.Destruct()
	})

	// Perform clean shutdown of already-initialized features if startup fails.
	var err error
	defer func() {
		if err != nil {
			err1 := build.ExtendErr("error during account manager startup", err)
			err2 := build.ExtendErr("error while stopping a partially started account manager", am.tg.Stop())
			err = build.ComposeErrors(err1, err2)
		}
	}()

	// Create the perist directory if it does not yet exist.
	err = dependencies.MkdirAll(am.persistDir, 0700)
	if err != nil {
		return nil, build.ExtendErr("error while creating the persist directory for the account manager", err)
	}

	// Logger is always the first thing initialized.
	am.log, err = dependencies.NewLogger(filepath.Join(am.persistDir, logFile))
	if err != nil {
		return nil, build.ExtendErr("error while creating the logger for the account manager", err)
	}
	// Set up the clean shutdown of the logger.
	am.tg.AfterStop(func() {
		err = build.ComposeErrors(am.log.Close(), err)
	})

	// TODO: load account manager state

	// Simulate an error to make sure the cleanup code is triggered correctly.
	if am.dependencies.Disrupt("erroredStartup") {
		err = errors.New("startup disrupted")
		return nil, err
	}

	return am, nil
}

// createAccount will create an empty account for the given accountID
func (am *AccountManager) managedCreateAccount(accountID string) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Escape early if the account exists already after acquiring the lock
	if am.accounts[accountID] != nil {
		return nil
	}

	// Ensure the given accountID can be properly parsed into a SiaPublicKey
	pk := &types.SiaPublicKey{}
	pk.LoadString(accountID)
	if pk.Key == nil {
		return fmt.Errorf("Invalid accountID: %v", accountID)
	}

	am.accounts[accountID] = &EphemeralAccount{
		accountID: *pk,
		balance:   types.ZeroCurrency,
		updated:   time.Now(),
	}

	return nil
}
