package accountmanager

import (
	"time"

	"gitlab.com/NebulousLabs/Sia/persist"
)

const (
	// DefaultPersistDir is the name of the default directory where the account
	// manager is persisted to
	DefaultPersistDir = "accountmanager"

	// logFile is the name of the file that is used for logging in the account
	// manager.
	logFile = "accountmanager.log"

	// Ephemeral accounts are kept by the host, however not into perpetuity
	// Whenever an account has not been updated for `accountExpiryTimeout` time
	// it gets removed.
	accountExpiryTimeout = 30 * 24 * time.Hour
)

var (
	// accountMetadata is the header that is used when writing the account
	// manager's state to disk.
	accountManagerMetadata = persist.Metadata{
		Header:  "Sia Account Manager",
		Version: "1.4.1",
	}
)
