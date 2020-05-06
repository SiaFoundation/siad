package renter

import (
	"bytes"
	"io"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// accountSize is the fixed account size in bytes
	accountSize = 1 << 8 // 256 bytes
)

var (
	// accountsFilename is the filename of the accounts persistence file
	accountsFilename = "accounts.dat"

	// Metadata
	metadataHeader  = types.NewSpecifier("Accounts\n")
	metadataVersion = types.NewSpecifier("v1.5.0\n")
	metadataSize    = 2*types.SpecifierLen + 1 // 1 byte for 'clean' flag

	// Metadata validation errors
	errWrongHeader  = errors.New("wrong header")
	errWrongVersion = errors.New("wrong version")

	// Persistence data validation errors
	errInvalidChecksum = errors.New("invalid checksum")
)

type (
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
		Balance   types.Currency
		HostKey   types.SiaPublicKey
		SecretKey crypto.SecretKey
	}
)

// managedPersist will write the account to the given file at the account's
// offset
func (a *account) managedPersist(file modules.File) error {
	a.staticMu.Lock()
	accountData := accountPersistence{
		AccountID: a.staticID,
		Balance:   a.balance,
		HostKey:   a.staticHostKey,
		SecretKey: a.staticSecretKey,
	}
	a.staticMu.Unlock()
	_, err := file.WriteAt(accountData.bytes(), a.staticOffset)
	return err
}

// readAccountAt tries to read an account object from the account persist file
// at the given offset
func (r *Renter) readAccountAt(offset, accountOffset int64) (*account, error) {
	// read account bytes
	accountBytes := make([]byte, accountSize, accountSize)
	_, err := r.staticAccountsFile.ReadAt(accountBytes, offset)
	if err != nil {
		return nil, errors.AddContext(err, "Failed to read account bytes")
	}

	// load the account bytes onto the a persistence object
	var accountData accountPersistence
	err = accountData.loadBytes(accountBytes)
	if err != nil {
		return nil, errors.AddContext(err, "Failed to load account bytes")
	}

	return &account{
		staticID:        accountData.AccountID,
		staticHostKey:   accountData.HostKey,
		staticOffset:    accountOffset,
		staticSecretKey: accountData.SecretKey,
		balance:         accountData.Balance,
	}, nil
}

// managedOpenAccountsFile opens the accounts file at the given path. If the
// file does not exist, it will create it and write the accountsMetadat header.
func (r *Renter) managedOpenAccountsFile(path string) error {
	// fetch the file info
	_, statErr := os.Stat(path)

	// open the file, create it if it does not exist yet
	file, err := r.deps.OpenFile(path, os.O_RDWR|os.O_CREATE, defaultFilePerm)
	if err != nil {
		return err
	}

	// set it on the renter
	id := r.mu.Lock()
	r.staticAccountsFile = file
	r.mu.Unlock(id)

	// if the file is newly created, write the header
	if os.IsNotExist(statErr) {
		return r.updateAccountsMetadata(accountsMetadata{
			Header:  metadataHeader,
			Version: metadataVersion,
			Clean:   true,
		})
	}
	return nil
}

// managedLoadAccounts loads the accounts from the persistence file onto the
// Renter object.
func (r *Renter) managedLoadAccounts() error {
	// open accounts file
	path := filepath.Join(r.persistDir, accountsFilename)
	err := r.managedOpenAccountsFile(path)
	if err != nil {
		return errors.AddContext(err, "Failed to open accounts file")
	}

	// read the metadata
	metadata, err := r.managedLoadMetadata()
	if err != nil {
		return errors.AddContext(err, "Failed to load the accounts metadata")
	}

	// validate the metadata
	if metadata.Header != metadataHeader {
		return errors.AddContext(errWrongHeader, "Failed to verify accounts metadata")
	}
	if metadata.Version != metadataVersion {
		return errors.AddContext(errWrongVersion, "Failed to verify accounts metadata")
	}

	// sanity check that the account size is larger than the metadata size,
	// before setting the initial offset
	if metadataSize > accountSize {
		err = errors.New("Metadata size is larger than account size, this means the initial offset is too small")
		build.Critical(err)
		return err
	}

	// the account offset is not necessarily the offset at which the account is
	// read, this is to ensure corrupted accounts do not take up disk space but
	// instead that disk space is reused.
	initialOffset := int64(accountSize)
	accountOffset := int64(accountSize)

	// read the raw account data and decode them into accounts
	accounts := make(map[string]*account)
	for offset := initialOffset; ; offset += accountSize {
		// read the account at offset
		acc, err := r.readAccountAt(offset, accountOffset)
		if errors.Contains(err, io.EOF) {
			break
		} else if err != nil {
			r.log.Println("ERROR: could not load account", err)
			continue
		}

		// reset the account balances after an unclean shutdown
		if !metadata.Clean {
			acc.balance = types.ZeroCurrency
		}
		accounts[acc.staticHostKey.String()] = acc
		accountOffset += accountSize
	}

	// mark the metadata as 'dirty' and update the metadata on disk - this
	// ensures only after a successful shutdown, accounts are reloaded from disk
	metadata.Clean = false
	err = r.updateAccountsMetadata(metadata)
	if err != nil {
		return errors.AddContext(err, "Failed to write metadata to accounts file")
	}
	err = r.staticAccountsFile.Sync()
	if err != nil {
		return errors.AddContext(err, "Failed to sync accounts file")
	}

	// load the accounts on to the renter
	id := r.mu.Lock()
	r.accounts = accounts
	r.mu.Unlock(id)
	return nil
}

// managedLoadMetadata loads the metadata from the accounts file.
func (r *Renter) managedLoadMetadata() (accountsMetadata, error) {
	var metadata accountsMetadata

	// read matadata bytes
	buffer := make([]byte, metadataSize)
	_, err := io.ReadFull(r.staticAccountsFile, buffer)
	if err != nil {
		return metadata, errors.AddContext(err, "Failed to read metadata from accounts file")
	}

	// unmarshal into accountsMetadata
	err = encoding.Unmarshal(buffer, &metadata)
	if err != nil {
		return metadata, errors.AddContext(err, "Failed to decode metadata from accounts file")
	}
	return metadata, nil
}

// managedSaveAccounts is called on shutdown and ensures the account data is
// properly persisted to disk
func (r *Renter) managedSaveAccounts() error {
	// grab the accounts
	id := r.mu.RLock()
	accounts := r.accounts
	r.mu.RUnlock(id)

	// save the account data to disk
	for _, account := range accounts {
		err := account.managedPersist(r.staticAccountsFile)
		if err != nil {
			r.log.Println("ERROR:", err)
			continue
		}
	}

	// sync before updating the header
	err := r.staticAccountsFile.Sync()
	if err != nil {
		return errors.AddContext(err, "Failed to sync accounts file")
	}

	// update the metadata and mark the file as clean
	if err = r.updateAccountsMetadata(accountsMetadata{
		Header:  metadataHeader,
		Version: metadataVersion,
		Clean:   true,
	}); err != nil {
		return errors.AddContext(err, "Failed to update accounts file metadata")
	}

	// sync and close the accounts file
	return errors.AddContext(errors.Compose(
		r.staticAccountsFile.Sync(),
		r.staticAccountsFile.Close(),
	), "Failed to sync and close the accounts file")
}

// updateAccountsMetadata writes the given metadata to the accounts file.
func (r *Renter) updateAccountsMetadata(am accountsMetadata) error {
	_, err := r.staticAccountsFile.WriteAt(encoding.Marshal(am), 0)
	return err
}

// bytes is a helper method on the persistence object that outputs the bytes to
// put on disk, these include the checksum and the marshaled persistence object.
func (ap accountPersistence) bytes() []byte {
	accBytes := encoding.Marshal(ap)
	accBytesMaxSize := accountSize - crypto.HashSize // leave room for checksum
	if len(accBytes) > accBytesMaxSize {
		build.Critical("marshaled object is larger than expected size")
	}

	// calculate checksum on padded account bytes
	accBytesPadded := make([]byte, accBytesMaxSize, accBytesMaxSize)
	copy(accBytesPadded, accBytes)
	checksum := crypto.HashBytes(accBytesPadded)

	// create final byte slice of account size
	b := make([]byte, accountSize, accountSize)
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
	return errors.AddContext(encoding.Unmarshal(accBytes, ap), "Failed to unmarshal account bytes")
}
