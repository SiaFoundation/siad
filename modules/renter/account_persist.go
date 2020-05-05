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
		Checksum  crypto.Hash
	}
)

// toAccountPersistence returns the account's persistence object
func (a *account) toAccountPersistence() accountPersistence {
	ap := accountPersistence{
		AccountID: a.staticID,
		Balance:   a.balance,
		HostKey:   a.staticHostKey,
		SecretKey: a.staticSecretKey,
	}
	ap.Checksum = ap.checksum()
	return ap
}

// managedLoadAccounts loads the accounts from the persistence file onto the
// Renter object.
func (r *Renter) managedLoadAccounts() error {
	// fetch the fileInfo before opening the file
	path := filepath.Join(r.persistDir, accountsFilename)
	_, statErr := os.Stat(path)

	// open the file, create it if it does not exist yet
	file, err := r.deps.OpenFile(path, os.O_RDWR|os.O_CREATE, defaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "Failed to open accounts persist file")
	}

	// read the metadata
	var metadata accountsMetadata
	if os.IsNotExist(statErr) {
		metadata = accountsMetadata{
			Header:  metadataHeader,
			Version: metadataVersion,
			Clean:   true,
		}
	} else {
		buffer := make([]byte, metadataSize)
		_, err := io.ReadFull(file, buffer)
		if err != nil {
			return errors.AddContext(err, "Failed to read metadata from accounts file")
		}
		err = encoding.Unmarshal(buffer, &metadata)
		if err != nil {
			return errors.AddContext(err, "Failed to decode metadata from accounts file")
		}
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
		build.Critical("Metadata size is larger than account size, this means the initial offset is too small")
	}
	initialOffset := int64(accountSize)

	// read the raw account data and decode them into accounts
	accounts := make(map[string]*account)

	accountOffset := initialOffset
	for offset := initialOffset; ; offset += accountSize {
		accountBytes := make([]byte, accountSize, accountSize)
		_, err = file.ReadAt(accountBytes, offset)
		if err == io.EOF {
			break
		}
		if err != nil {
			r.log.Println("ERROR:", errors.AddContext(err, "Failed to read account data"))
			continue
		}

		var accountData accountPersistence
		err = encoding.Unmarshal(accountBytes, &accountData)
		if err != nil {
			r.log.Println("ERROR:", errors.AddContext(err, "Failed to unmarshal account"))
			continue
		}
		if !accountData.verifyChecksum(accountData.Checksum) {
			r.log.Println("ERROR:", errors.New("Account ignored because checksum did not match"))
			continue
		}

		acc := &account{
			staticID:        accountData.AccountID,
			staticHostKey:   accountData.HostKey,
			staticOffset:    accountOffset,
			staticSecretKey: accountData.SecretKey,
			balance:         accountData.Balance,
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
	_, err = file.WriteAt(encoding.Marshal(metadata), 0)
	if err != nil {
		return errors.AddContext(err, "Failed to write metadata to accounts file")
	}
	err = file.Sync()
	if err != nil {
		return errors.AddContext(err, "Failed to sync accounts file")
	}

	// load the accounts on to the renter
	id := r.mu.Lock()
	r.accounts = accounts
	r.staticAccountsFile = file
	r.mu.Unlock(id)
	return nil
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
		account.staticMu.Lock()
		accountData := account.toAccountPersistence()
		account.staticMu.Unlock()

		bytes := accountData.toBytes()
		_, err := r.staticAccountsFile.WriteAt(bytes, account.staticOffset)
		if err != nil {
			r.log.Println("ERROR:", err)
			continue
		}
	}

	// update the metadata and mark the file as clean
	metadata := accountsMetadata{
		Header:  metadataHeader,
		Version: metadataVersion,
		Clean:   true,
	}
	_, err := r.staticAccountsFile.WriteAt(encoding.Marshal(metadata), 0)
	if err != nil {
		return errors.AddContext(err, "Failed to update accounts file metadata")
	}

	// sync and close the persist file
	return errors.Compose(
		r.staticAccountsFile.Sync(),
		r.staticAccountsFile.Close(),
	)
}

// checksum returns the checksum of the accountPeristence object
func (ap accountPersistence) checksum() crypto.Hash {
	return crypto.HashAll(
		ap.AccountID,
		ap.HostKey,
		ap.SecretKey,
		ap.Balance,
	)
}

// toBytes is a helper method on the persistence object that marshals the object
// into a byte slice and performs a sanity check on the length
func (ap accountPersistence) toBytes() []byte {
	apMar := encoding.Marshal(ap)
	if len(apMar) > accountSize {
		build.Critical("marshaled object is larger than expected size")
	}

	accountBytes := make([]byte, accountSize, accountSize)
	copy(accountBytes, apMar)
	return accountBytes
}

// verifyChecksum creates a checksum of the accountPersistence object and
// checks if it's equal to the given checksum
func (ap accountPersistence) verifyChecksum(checksum crypto.Hash) bool {
	expected := ap.checksum()
	return bytes.Equal(expected[:], checksum[:])
}
