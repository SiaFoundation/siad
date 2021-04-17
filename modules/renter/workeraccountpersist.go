package renter

// TODO: Derive the account secret key using the wallet seed. Can use:
// `account specifier || wallet seed || host pubkey` I believe.
//
// If we derive the seeds deterministically, that may mean that we can
// regenerate accounts even we fail to load them from disk. When we make a new
// account with a host, we should always query that host for a balance even if
// we think this is a new account, some previous run on siad may have created
// the account for us.
//
// TODO: How long does the host keep an account open? Does it keep the account
// open for the entire period? If not, we should probably adjust that on the
// host side, otherwise renters that go offline for a while are going to lose
// their accounts because the hosts will expire them. Does the renter track the
// expiration date of the accounts? Will it know upload load that the account is
// missing from the host not because of malice but because they expired?

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/types"
)

const (
	// accountSize is the fixed account size in bytes
	accountSize     = 1 << 10 // 1024 bytes
	accountSizeV150 = 1 << 8  // 256 bytes
	accountsOffset  = 1 << 12 // 4kib to sector align
)

var (
	// accountsFilename is the filename of the accounts persistence file
	accountsFilename = "accounts.dat"

	// accountsTmpFilename is the filename of the temporary account file created
	// when upgrading the account's persistence file.
	accountsTmpFilename = "accounts.tmp.dat"

	// Metadata
	metadataHeader  = types.NewSpecifier("Accounts\n")
	metadataVersion = persist.MetadataVersionv156
	metadataSize    = 2*types.SpecifierLen + 1 // 1 byte for 'clean' flag

	// Metadata validation errors
	errWrongHeader  = errors.New("wrong header")
	errWrongVersion = errors.New("wrong version")

	// Persistence data validation errors
	errInvalidChecksum = errors.New("invalid checksum")
)

type (
	// accountManager tracks the set of accounts known to the renter.
	accountManager struct {
		accounts map[string]*account

		// Utils. The file is global to all accounts, each account looks at a
		// specific offset within the file.
		mu           sync.Mutex
		staticFile   modules.File
		staticRenter *Renter
	}

	// accountsMetadata is the metadata of the accounts persist file
	accountsMetadata struct {
		Header  types.Specifier
		Version types.Specifier
		Clean   bool
	}

	// accountPersistence is the account's persistence object which holds all
	// data that gets persisted for a single account.
	accountPersistence struct {
		AccountID modules.AccountID
		HostKey   types.SiaPublicKey
		SecretKey crypto.SecretKey

		// balance details, aside from the balance we keep track of the balance
		// drift, in both directions, that may occur when the renter's account
		// balance becomes out of sync with the host's version of the balance
		Balance              types.Currency
		BalanceDriftPositive types.Currency
		BalanceDriftNegative types.Currency

		// spending details
		SpendingDownloads         types.Currency
		SpendingRegistryReads     types.Currency
		SpendingRegistryWrites    types.Currency
		SpendingRepairDownloads   types.Currency
		SpendingRepairUploads     types.Currency
		SpendingSnapshotDownloads types.Currency
		SpendingSnapshotUploads   types.Currency
		SpendingSubscriptions     types.Currency
		SpendingUploads           types.Currency
	}

	// accountPersistenceV150 is how the account persistence struct looked
	// before adding the spending details in v156
	accountPersistenceV150 struct {
		AccountID modules.AccountID
		Balance   types.Currency
		HostKey   types.SiaPublicKey
		SecretKey crypto.SecretKey
	}
)

// newAccountManager will initialize the account manager for the renter.
func (r *Renter) newAccountManager() error {
	if r.staticAccountManager != nil {
		return errors.New("account manager already exists")
	}

	r.staticAccountManager = &accountManager{
		accounts: make(map[string]*account),

		staticRenter: r,
	}

	return r.staticAccountManager.load()
}

// managedPersist will write the account to the given file at the account's
// offset, without syncing the file.
func (a *account) managedPersist() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.persist()
}

// persist will write the account to the given file at the account's offset,
// without syncing the file.
func (a *account) persist() error {
	accountData := accountPersistence{
		AccountID: a.staticID,
		HostKey:   a.staticHostKey,
		SecretKey: a.staticSecretKey,

		// balance details
		Balance:              a.minExpectedBalance(),
		BalanceDriftPositive: a.balanceDriftPositive,
		BalanceDriftNegative: a.balanceDriftNegative,

		// spending details
		SpendingDownloads:         a.spending.downloads,
		SpendingRegistryReads:     a.spending.registryReads,
		SpendingRegistryWrites:    a.spending.registryWrites,
		SpendingRepairDownloads:   a.spending.repairDownloads,
		SpendingRepairUploads:     a.spending.repairUploads,
		SpendingSnapshotDownloads: a.spending.snapshotDownloads,
		SpendingSnapshotUploads:   a.spending.snapshotUploads,
		SpendingSubscriptions:     a.spending.subscriptions,
		SpendingUploads:           a.spending.uploads,
	}

	_, err := a.staticFile.WriteAt(accountData.bytes(), a.staticOffset)
	return errors.AddContext(err, "unable to write the account to disk")
}

// bytes is a helper method on the persistence object that outputs the bytes to
// put on disk, these include the checksum and the marshaled persistence object.
func (ap accountPersistence) bytes() []byte {
	accBytes := encoding.Marshal(ap)
	accBytesMaxSize := accountSize - crypto.HashSize // leave room for checksum
	if len(accBytes) > accBytesMaxSize {
		build.Critical("marshaled object is larger than expected size", len(accBytes))
		return nil
	}

	// Calculate checksum on padded account bytes. Upon load, the padding will
	// be ignored by the unmarshaling.
	accBytesPadded := make([]byte, accBytesMaxSize)
	copy(accBytesPadded, accBytes)
	checksum := crypto.HashBytes(accBytesPadded)

	// create final byte slice of account size
	b := make([]byte, accountSize)
	copy(b[:len(checksum)], checksum[:])
	copy(b[len(checksum):], accBytesPadded)
	return b
}

// loadBytes is a helper method that takes a byte slice, containing a checksum
// and the account bytes, and unmarshals them onto the persistence object if the
// checksum is valid.
func (ap *accountPersistence) loadBytes(b []byte) error {
	// extract checksum and verify it
	checksum := b[:crypto.HashSize]
	accBytes := b[crypto.HashSize:]
	accHash := crypto.HashBytes(accBytes)
	if !bytes.Equal(checksum, accHash[:]) {
		return errInvalidChecksum
	}

	// unmarshal the account bytes onto the persistence object
	return errors.AddContext(encoding.Unmarshal(accBytes, ap), "failed to unmarshal account bytes")
}

// managedOpenAccount returns an account for the given host. If it does not
// exist already one is created.
func (am *accountManager) managedOpenAccount(hostKey types.SiaPublicKey) (acc *account, err error) {
	// Check if we already have an account. Due to a race condition around
	// account creation, we need to check that the account was persisted to disk
	// before we can start using it, this happens with the 'staticReady' and
	// 'externActive' variables of the account. See the rest of this functions
	// implementation to understand how they are used in practice.
	am.mu.Lock()
	acc, exists := am.accounts[hostKey.String()]
	if exists {
		am.mu.Unlock()
		<-acc.staticReady
		if acc.externActive {
			return acc, nil
		}
		return nil, errors.New("account creation failed")
	}
	// Open a new account.
	offset := accountsOffset + len(am.accounts)*accountSize
	aid, sk := modules.NewAccountID()
	acc = &account{
		staticID:        aid,
		staticHostKey:   hostKey,
		staticSecretKey: sk,

		staticFile:   am.staticFile,
		staticOffset: int64(offset),

		staticReady: make(chan struct{}),
	}
	am.accounts[hostKey.String()] = acc
	am.mu.Unlock()
	// Defer a close on 'staticReady'. By default, 'externActive' is false, so
	// if there is an error, the account will be marked as unusable.
	defer close(acc.staticReady)

	// Defer a function to delete the account if the persistence fails. This is
	// technically a race condition, but the alternative is holding the lock on
	// the account mangager while doing an fsync, which is not ideal.
	defer func() {
		if err != nil {
			am.mu.Lock()
			delete(am.accounts, hostKey.String())
			am.mu.Unlock()
		}
	}()

	// Save the file. After the file gets written to disk, perform a sync
	// because we want to ensure that the secret key of the account can be
	// recovered before we start using the account.
	err = acc.managedPersist()
	if err != nil {
		return nil, errors.AddContext(err, "failed to persist account")
	}
	err = acc.staticFile.Sync()
	if err != nil {
		return nil, errors.AddContext(err, "failed to sync accounts file")
	}

	// Mark the account as usable so that anyone who tried to open the account
	// after this function ran will see that the account is persisted correctly.
	acc.mu.Lock()
	acc.externActive = true
	acc.mu.Unlock()
	return acc, nil
}

// managedSaveAndClose is called on shutdown and ensures the account data is
// properly persisted to disk
func (am *accountManager) managedSaveAndClose() error {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Save the account data to disk.
	clean := true
	var persistErrs error
	for _, account := range am.accounts {
		err := account.managedPersist()
		if err != nil {
			clean = false
			persistErrs = errors.Compose(persistErrs, err)
			continue
		}
	}
	// If there was an error saving any of the accounts, the system is not clean
	// and we do not need to update the metadata for the file.
	if !clean {
		return errors.AddContext(persistErrs, "unable to persist all accounts cleanly upon shutdown")
	}

	// Sync the file before updating the header. We want to make sure that the
	// accounts have been put into a clean and finalized state before writing an
	// update to the metadata.
	err := am.staticFile.Sync()
	if err != nil {
		return errors.AddContext(err, "failed to sync accounts file")
	}

	// update the metadata and mark the file as clean
	if err = am.updateMetadata(accountsMetadata{
		Header:  metadataHeader,
		Version: metadataVersion,
		Clean:   true,
	}); err != nil {
		return errors.AddContext(err, "failed to update accounts file metadata")
	}

	// Close the account file.
	return am.staticFile.Close()
}

// checkMetadata will load the metadata from the account file and return whether
// or not the previous shutdown was clean. If the metadata does not match the
// expected metadata, an error will be returned.
//
// NOTE: If we change the version of the file, this is probably the function
// that should handle doing the persist upgrade. Inside of this function there
// would be a call to the upgrade function.
func (am *accountManager) checkMetadata() (bool, error) {
	// Read metadata.
	metadata, err := readAccountsMetadata(am.staticFile)
	if err != nil {
		return false, errors.AddContext(err, "failed to read metadata from accounts file")
	}

	// Validate the metadata.
	if metadata.Header != metadataHeader {
		return false, errors.AddContext(errWrongHeader, "failed to verify accounts metadata")
	}
	if metadata.Version != metadataVersion {
		return false, errors.AddContext(errWrongVersion, "failed to verify accounts metadata")
	}
	return metadata.Clean, nil
}

// handleInterruptedUpgrade ensures that an interrupted upgrade can be recovered
// from. It does so by checking for the existence of a tmp accounts file, if
// that file is present we want to handle it occordingly.
func (am *accountManager) handleInterruptedUpgrade() error {
	// convenience variables
	r := am.staticRenter
	tmpFilePath := filepath.Join(r.persistDir, accountsTmpFilename)

	// check whether the tmp file exists
	tmpFileExists, err := fileExists(tmpFilePath)
	if err != nil {
		return errors.AddContext(err, "error checking if tmp file exists")
	}

	// if the tmp file does not exist, we don't have to do anything
	if !tmpFileExists {
		return nil
	}

	// open the tmp file
	tmpFile, err := r.deps.OpenFile(tmpFilePath, os.O_RDWR, defaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "error opening tmp account file")
	}

	// read the metadata, there can only be two scenarios:
	// - the tmp file is clean, continue from that file
	// - the tmp file is dirty, remove it
	tmpFileMetadata, err := readAccountsMetadata(tmpFile)
	if err == nil && tmpFileMetadata.Clean {
		return am.upgradeFromV150ToV156_CopyAccountsFromFile(tmpFile)
	}

	return errors.Compose(tmpFile.Close(), r.deps.RemoveFile(tmpFilePath))
}

// managedLoad will pull all of the accounts off of disk and load them into the
// account manager. This should complete before the accountManager is made
// available to other processes.
func (am *accountManager) load() error {
	// Open the accounts file.
	clean, err := am.openFile()
	if err != nil {
		return errors.AddContext(err, "failed to open accounts file")
	}

	// Read the raw account data and decode them into accounts. We start at an
	// offset of 'accountsOffset' because the metadata precedes the accounts
	// data.
	for offset := int64(accountsOffset); ; offset += accountSize {
		// read the account at offset
		acc, err := am.readAccountAt(offset)
		if errors.Contains(err, io.EOF) {
			break
		} else if err != nil {
			am.staticRenter.log.Println("ERROR: could not load account", err)
			continue
		}

		// reset the account balances after an unclean shutdown
		if !clean {
			acc.balance = types.ZeroCurrency
		}
		am.accounts[acc.staticHostKey.String()] = acc
	}

	// Ensure that when the renter is shut down, the save and close function
	// runs.
	if am.staticRenter.deps.Disrupt("InterruptAccountSaveOnShutdown") {
		// Dependency injection to simulate an unclean shutdown.
		return nil
	}
	err = am.staticRenter.tg.AfterStop(am.managedSaveAndClose)
	if err != nil {
		return errors.AddContext(err, "unable to schedule a save and close with the thread group")
	}
	return nil
}

// openFile will open the file of the account manager and set the account
// manager's file variable.
//
// openFile will return 'true' if the previous shutdown was clean, and 'false'
// if the previous shutdown was not clean.
func (am *accountManager) openFile() (bool, error) {
	r := am.staticRenter

	// Sanity check that the file isn't already opened.
	if am.staticFile != nil {
		r.log.Critical("double open detected on account manager")
		return false, errors.New("accounts file already open")
	}

	// Open the accounts file
	accountsFile, err := am.openAccountsFile(accountsFilename)
	if err != nil {
		return false, errors.AddContext(err, "error opening account file")
	}
	am.staticFile = accountsFile

	// Read accounts metadata
	metadata, err := readAccountsMetadata(am.staticFile)
	if err != nil {
		return false, errors.AddContext(err, "error reading account metadata")
	}

	// Handle a potentially interrupted upgrade
	err = am.handleInterruptedUpgrade()
	if err != nil {
		return false, errors.AddContext(err, "error occurred while trying to recover from an intterupted upgrade")
	}

	// Check accounts metadata
	_, err = am.checkMetadata()
	if err != nil && !errors.Contains(err, errWrongVersion) {
		return false, errors.AddContext(err, "error reading account metadata")
	}

	// If the metadata contains a wrong version, run the upgrade code
	if errors.Contains(err, errWrongVersion) {
		err = am.upgradeFromV150ToV156()
		if err != nil {
			return false, errors.AddContext(err, "error upgrading accounts file")
		}

		// log the successful upgrade
		am.staticRenter.log.Println("successfully upgraded accounts file from v150 to v156")
	}

	// Whether this is a new file or an existing file, we need to set the header
	// on the metadata. When opening an account, the header should represent an
	// unclean shutdown. This will be flipped to a header that represents a
	// clean shutdown upon closing.
	err = am.updateMetadata(accountsMetadata{
		Header:  metadataHeader,
		Version: metadataVersion,
		Clean:   false,
	})
	if err != nil {
		return false, errors.AddContext(err, "unable to update the account metadata")
	}

	// Sync the metadata to ensure the acounts will load as dirty before any
	// accounts are created.
	err = am.staticFile.Sync()
	if err != nil {
		return false, errors.AddContext(err, "failed to sync accounts file")
	}

	return metadata.Clean, nil
}

// openAccountsFile is a helper function that will open an accounts file with
// given filename. If the accounts file does not exist prior to calling this
// function, it will be created and provided with the metadata header.
func (am *accountManager) openAccountsFile(filename string) (modules.File, error) {
	r := am.staticRenter

	// check whether the file exists
	accountsFilepath := filepath.Join(r.persistDir, filename)
	accountsFileExists, err := fileExists(accountsFilepath)
	if err != nil {
		return nil, err
	}

	// open the file and create it if necessary
	accountsFile, err := r.deps.OpenFile(accountsFilepath, os.O_RDWR|os.O_CREATE, defaultFilePerm)
	if err != nil {
		return nil, errors.AddContext(err, "error opening account file")
	}

	// make sure a newly created accounts file has the metadata header
	if !accountsFileExists {
		_, err = accountsFile.WriteAt(encoding.Marshal(accountsMetadata{
			Header:  metadataHeader,
			Version: metadataVersion,
			Clean:   false,
		}), 0)
		err = errors.Compose(err, accountsFile.Sync())
		if err != nil {
			return accountsFile, errors.AddContext(err, "error writing metadata to accounts file")
		}
	}

	return accountsFile, nil
}

// readAccountAt tries to read an account object from the account persist file
// at the given offset.
func (am *accountManager) readAccountAt(offset int64) (*account, error) {
	// read account bytes
	accountBytes := make([]byte, accountSize)
	_, err := am.staticFile.ReadAt(accountBytes, offset)
	if err != nil {
		return nil, errors.AddContext(err, "failed to read account bytes")
	}

	// load the account bytes onto the a persistence object
	var accountData accountPersistence
	err = accountData.loadBytes(accountBytes)
	if err != nil {
		return nil, errors.AddContext(err, "failed to load account bytes")
	}

	acc := &account{
		staticID:        accountData.AccountID,
		staticHostKey:   accountData.HostKey,
		staticSecretKey: accountData.SecretKey,

		// balance details
		balance:              accountData.Balance,
		balanceDriftPositive: accountData.BalanceDriftPositive,
		balanceDriftNegative: accountData.BalanceDriftNegative,

		// spending details
		spending: spendingDetails{
			downloads:         accountData.SpendingDownloads,
			registryReads:     accountData.SpendingRegistryReads,
			registryWrites:    accountData.SpendingRegistryWrites,
			repairDownloads:   accountData.SpendingRepairDownloads,
			repairUploads:     accountData.SpendingRepairUploads,
			snapshotDownloads: accountData.SpendingSnapshotDownloads,
			snapshotUploads:   accountData.SpendingSnapshotUploads,
			subscriptions:     accountData.SpendingSubscriptions,
			uploads:           accountData.SpendingUploads,
		},

		staticReady:  make(chan struct{}),
		externActive: true,

		staticOffset: offset,
		staticFile:   am.staticFile,
	}
	close(acc.staticReady)
	return acc, nil
}

// upgradeFromV150ToV156 is compat code that upgrades the accounts file from
// v150 to v156. The new accounts take up more space on disk, so we have to read
// all of them, assign them new offets and rewrite them to the accounts file.
func (am *accountManager) upgradeFromV150ToV156() error {
	// convenience variables
	r := am.staticRenter

	// open a tmp accounts file
	tmpFile, err := am.openAccountsFile(accountsTmpFilename)
	if err != nil {
		return errors.AddContext(err, "failed to open tmp accounts file")
	}

	// read the accounts from the accounts file, but link them to the tmp file,
	// when calling persist on the account it will write the account into the
	// tmp file
	accounts := compatV150ReadAccounts(r.log, am.staticFile, tmpFile)
	for _, acc := range accounts {
		if err := acc.managedPersist(); err != nil {
			r.log.Println("failed to upgrade account from v150 to v156", err)
		}
	}

	// sync the tmp file
	err = tmpFile.Sync()
	if err != nil {
		return errors.AddContext(err, "failed to sync tmp file")
	}

	// update the header and mark it clean
	_, err = tmpFile.WriteAt(encoding.Marshal(accountsMetadata{
		Header:  metadataHeader,
		Version: metadataVersion,
		Clean:   true,
	}), 0)
	if err != nil {
		return errors.AddContext(err, "failed to write header to tmp file")
	}

	// sync the tmp file, this step is very important because
	// if it completes successfully, and the upgrade fails over this point, the
	// tmp file will be used to recover from an interrupted upgrade.
	err = tmpFile.Sync()
	if err != nil {
		return errors.AddContext(err, "failed to sync tmp file")
	}

	// copy the accounts from the tmp file to the accounts file, this is
	// extracted into a separate method as the recovery flow might have to pick
	// up from where we left off in case of failure during an initial attempt
	return am.upgradeFromV150ToV156_CopyAccountsFromFile(tmpFile)
}

// upgradeFromV150ToV156_CopyAccountsFromFile will copy the contents of the tmp
// file into the accounts file. This is a separate method as this function is
// called during the happy flow, but it is also potentially the steps required
// when trying to recover from a failed initial update attempt.
func (am *accountManager) upgradeFromV150ToV156_CopyAccountsFromFile(tmpFile modules.File) (err error) {
	// convenience variables
	r := am.staticRenter
	tmpFilePath := filepath.Join(r.persistDir, accountsTmpFilename)

	// copy the tmp file to the accounts file
	_, err = io.Copy(am.staticFile, tmpFile)
	if err != nil {
		return errors.AddContext(err, "failed to copy the temporary accounts file to the actual accounts file location")
	}

	// sync the accounts file
	err = am.staticFile.Sync()
	if err != nil {
		return errors.AddContext(err, "failed to sync accounts file")
	}

	// seek to the beginning of the file
	_, err = am.staticFile.Seek(0, io.SeekStart)
	if err != nil {
		return errors.AddContext(err, "failed to seek to the beginning of the accounts file")
	}

	// delete the tmp file
	return errors.AddContext(errors.Compose(tmpFile.Close(), r.deps.RemoveFile(tmpFilePath)), "failed to delete accounts file")
}

// updateMetadata writes the given metadata to the accounts file.
func (am *accountManager) updateMetadata(meta accountsMetadata) error {
	_, err := am.staticFile.WriteAt(encoding.Marshal(meta), 0)
	return err
}

// compatV150ReadAccounts is a helper function that reads the accounts from the
// accounts file assuming they are persisted using the v150 persistence object
// and parameters. Extracted to keep the compat code clean.
func compatV150ReadAccounts(log *persist.Logger, accountsFile modules.File, tmpFile modules.File) []*account {
	// the offset needs to be the new accountsOffset
	newOffset := int64(accountsOffset)

	// collect all accounts from the current accounts file
	var accounts []*account
	for offset := int64(accountSizeV150); ; offset += accountSizeV150 {
		// read account bytes
		accountBytes := make([]byte, accountSizeV150)
		_, err := accountsFile.ReadAt(accountBytes, offset)
		if errors.Contains(err, io.EOF) {
			break
		} else if err != nil {
			log.Println("ERROR: could not read account data", err)
			continue
		}

		// load the account bytes onto the a persistence object
		var accountDataV150 accountPersistenceV150
		err = encoding.Unmarshal(accountBytes[crypto.HashSize:], &accountDataV150)
		if err != nil {
			log.Println("ERROR: could not load account bytes", err)
			continue
		}

		accounts = append(accounts, &account{
			staticID:        accountDataV150.AccountID,
			staticHostKey:   accountDataV150.HostKey,
			staticSecretKey: accountDataV150.SecretKey,

			balance: accountDataV150.Balance,

			staticOffset: newOffset,
			staticFile:   tmpFile,
		})
		newOffset += accountSize
	}

	return accounts
}

// fileExists is a small helper function that checks whether a file at given
// path exists, it abstracts checking whether the error from the stat is an
// `IsNotExists` error or not.
func fileExists(path string) (bool, error) {
	_, statErr := os.Stat(path)
	if statErr == nil {
		return true, nil
	}
	if os.IsNotExist(statErr) {
		return false, nil
	}
	return false, errors.AddContext(statErr, "error calling stat on file")
}

// readAccountsMetadata is a small helper function that tries to read the
// metadata object from the given file.
func readAccountsMetadata(file modules.File) (*accountsMetadata, error) {
	// Seek to the beginning of the file
	_, err := file.Seek(0, io.SeekStart)
	if err != nil {
		return nil, errors.AddContext(err, "failed to seek to the beginning of the file")
	}

	// Read metadata.
	buffer := make([]byte, metadataSize)
	_, err = io.ReadFull(file, buffer)
	if err != nil {
		return nil, errors.AddContext(err, "failed to read metadata from file")
	}

	// Seek to the beginning of the file
	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		return nil, errors.AddContext(err, "failed to seek to the beginning of the file")
	}

	// Decode metadata
	var metadata accountsMetadata
	err = encoding.Unmarshal(buffer, &metadata)
	if err != nil {
		return nil, errors.AddContext(err, "failed to decode metadata")
	}

	return &metadata, nil
}
