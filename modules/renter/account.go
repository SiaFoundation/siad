package renter

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
)

const (
	// accountSize is the fixed account size in bytes
	accountSize = 1 << 8 // 256 bytes

	// withdrawalValidityPeriod defines the period (in blocks) a withdrawal
	// message remains spendable after it has been created. Together with the
	// current block height at time of creation, this period makes up the
	// WithdrawalMessage's expiry height.
	withdrawalValidityPeriod = 6
)

var (
	// accountsFilename is the filename of the accounts persistence file
	accountsFilename = "accounts.dat"

	// Metadata
	metadataHeader  = types.NewSpecifier("Accounts\n")
	metadataVersion = types.NewSpecifier("v1.5.0\n")
	metadataSize    = 2*types.SpecifierLen + 1

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

	// account represents a renter's ephemeral account on a host.
	account struct {
		staticID        modules.AccountID
		staticHostKey   types.SiaPublicKey
		staticOffset    int64
		staticSecretKey crypto.SecretKey

		balance       types.Currency
		pendingFunds  types.Currency
		pendingSpends types.Currency

		staticPersistence modules.File
		staticMu          sync.RWMutex
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

// ProvidePayment takes a stream and various payment details and handles the
// payment by sending and processing payment request and response objects.
// Returns an error in case of failure.
func (a *account) ProvidePayment(stream siamux.Stream, host types.SiaPublicKey, rpc types.Specifier, amount types.Currency, blockHeight types.BlockHeight) error {
	// NOTE: we purposefully do not verify if the account has sufficient funds.
	// Seeing as withdrawals are a blocking action on the host, it is perfectly
	// ok to trigger them from an account with insufficient balance.

	// create a withdrawal message
	msg := newWithdrawalMessage(a.staticID, amount, blockHeight)
	sig := crypto.SignHash(crypto.HashObject(msg), a.staticSecretKey)

	// send PaymentRequest
	err := modules.RPCWrite(stream, modules.PaymentRequest{Type: modules.PayByEphemeralAccount})
	if err != nil {
		return err
	}

	// send PayByEphemeralAccountRequest
	err = modules.RPCWrite(stream, modules.PayByEphemeralAccountRequest{
		Message:   msg,
		Signature: sig,
	})
	if err != nil {
		return err
	}

	// receive PayByEphemeralAccountResponse
	//
	// TODO: this should not be blocking! handle in a separate goroutine
	var payByResponse modules.PayByEphemeralAccountResponse
	err = modules.RPCRead(stream, &payByResponse)
	if err != nil {
		return err
	}
	return nil
}

// managedAvailableBalance returns the amount of money that is available to
// spend. It is calculated by taking into account pending spends and pending
// funds.
func (a *account) managedAvailableBalance() types.Currency {
	a.staticMu.RLock()
	defer a.staticMu.RUnlock()

	total := a.balance.Add(a.pendingFunds)
	if a.pendingSpends.Cmp(total) < 0 {
		return total.Sub(a.pendingSpends)
	}
	return types.ZeroCurrency
}

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

	// truncate the file if it was not properly saved
	if !metadata.Clean {
		err = file.Truncate(int64(metadataSize))
		return errors.AddContext(err, "Failed to truncate the accounts persistence file")
	}

	// sanity check that the account size is larger than the metadata size,
	// before setting the initial offset
	if metadataSize > accountSize {
		build.Critical("Metadata size is larger than account size, this means the initial offset is too small")
	}
	initialOffset := int64(accountSize)

	// seek to the initial offset
	_, err = file.Seek(initialOffset, io.SeekStart)
	if err != nil {
		return errors.AddContext(err, "Failed to seek to initial offset in accounts file")
	}

	// read the raw account data and decode them into accounts
	accounts := make(map[string]*account)
	nextOffset := initialOffset
	for {
		accountBytes := make([]byte, accountSize, accountSize)
		_, err = file.Read(accountBytes)
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
			staticID:          accountData.AccountID,
			staticHostKey:     accountData.HostKey,
			staticOffset:      nextOffset,
			staticPersistence: file,
			staticSecretKey:   accountData.SecretKey,
			balance:           accountData.Balance,
		}
		accounts[acc.staticHostKey.String()] = acc
		nextOffset += accountSize
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

// managedOpenAccount returns an account for the given host. If it does not
// exist already one is created.
func (r *Renter) managedOpenAccount(hostKey types.SiaPublicKey) *account {
	id := r.mu.Lock()
	defer r.mu.Unlock(id)

	acc, ok := r.accounts[hostKey.String()]
	if ok {
		return acc
	}
	acc = r.newAccount(hostKey)
	r.accounts[hostKey.String()] = acc
	return acc
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

		bytes, err := accountData.toBytes()
		if err != nil {
			r.log.Println("ERROR:", err)
			continue
		}
		_, err = r.staticAccountsFile.WriteAt(bytes, account.staticOffset)
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

// newAccount returns an new account object for the given host.
func (r *Renter) newAccount(hostKey types.SiaPublicKey) *account {
	aid, sk := modules.NewAccountID()

	// calculate the account's offset
	metadataPadding := accountSize - metadataSize
	offset := metadataSize + metadataPadding + (len(r.accounts) * accountSize)

	return &account{
		staticID:          aid,
		staticHostKey:     hostKey,
		staticOffset:      int64(offset),
		staticPersistence: r.staticAccountsFile,
		staticSecretKey:   sk,
	}
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
func (ap accountPersistence) toBytes() ([]byte, error) {
	apMar := encoding.Marshal(ap)
	if len(apMar) > accountSize {
		return nil, errors.New("marshaled object is larger than expected size")
	}

	accountBytes := make([]byte, accountSize, accountSize)
	copy(accountBytes, apMar)
	return accountBytes, nil
}

// verifyChecksum creates a checksum of the accountPersistence object and
// checks if it's equal to the given checksum
func (ap accountPersistence) verifyChecksum(checksum crypto.Hash) bool {
	expected := ap.checksum()
	return bytes.Equal(expected[:], checksum[:])
}

// MarshalSia implements the SiaMarshaler interface.
func (am accountsMetadata) MarshalSia(w io.Writer) error {
	e := encoding.NewEncoder(w)
	e.Write(am.Header[:])
	e.Write(am.Version[:])
	e.WriteBool(am.Clean)
	return e.Err()
}

// UnmarshalSia implements the SiaMarshaler interface.
func (am *accountsMetadata) UnmarshalSia(r io.Reader) error {
	d := encoding.NewDecoder(r, encoding.DefaultAllocLimit)
	d.ReadFull(am.Header[:])
	d.ReadFull(am.Version[:])
	buf := make([]byte, 1)
	d.ReadFull(buf)
	am.Clean = buf[0] == 1
	return d.Err()
}

// newWithdrawalMessage is a helper function that takes a set of parameters and
// a returns a new WithdrawalMessage.
func newWithdrawalMessage(id modules.AccountID, amount types.Currency, blockHeight types.BlockHeight) modules.WithdrawalMessage {
	expiry := blockHeight + withdrawalValidityPeriod
	var nonce [modules.WithdrawalNonceSize]byte
	fastrand.Read(nonce[:])
	return modules.WithdrawalMessage{
		Account: id,
		Expiry:  expiry,
		Amount:  amount,
		Nonce:   nonce,
	}
}
