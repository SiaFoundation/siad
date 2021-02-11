package accounting

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// logFile is the name of the log file for the Accounting module.
	logFile string = modules.AccountingDir + ".log"

	// maxPersistSize is the size of the marshaled persistence
	//
	// 16 bytes for Currency fields
	// 8 bytes for Timestamp field
	// 40 bytes for overhead
	//
	// (4 x 16) + 8 + 40 = 112
	maxPersistSize int = 112

	// persistEntrySize is the size of the encoded persist entry
	//
	// maxPersistSize + 8 for the size
	persistEntrySize int = 120

	// persistFile is the name of the persist file
	persistFile string = "accounting.dat"
)

var (
	// metadataHeader is the header of the metadata for the persist file
	metadataHeader = types.NewSpecifier("Accounting\n")

	// metadataVersion is the version of the persistence file
	metadataVersion = types.NewSpecifier("v1.5.5\n")

	// persistErrorInterval is the interval at which the persist loop will wait in
	// the event of an error.
	persistErrorInterval = build.Select(build.Var{
		Dev:      time.Second,
		Standard: time.Hour,
		Testing:  time.Millisecond * 100,
	}).(time.Duration)

	// persistInterval is the interval at which the accounting information will be
	// persisted.
	persistInterval = build.Select(build.Var{
		Dev:      time.Minute,
		Standard: time.Hour * 24,
		Testing:  time.Second,
	}).(time.Duration)
)

type (
	// persistence contains the accounting information that is persisted on disk
	persistence struct {
		// Not implemented yet
		//
		// FeeManager modules.FeeManagerAccounting `json:"feemanager"`
		// Host       modules.HostAccounting       `json:"host"`
		// Miner      modules.MinerAccounting      `json:"miner"`

		Renter modules.RenterAccounting `json:"renter"`
		Wallet modules.WalletAccounting `json:"wallet"`

		// Unix Timestamp
		Timestamp int64 `json:"timestamp"`
	}

	// persistEntry is the struct that is persisted on disk
	persistEntry struct {
		// Size is the actual size for the persisted data
		Size        int
		Persistence [maxPersistSize]byte
	}
)

// callThreadedPersistAccounting is a background loop that persists the
// accounting information based on the persistInterval.
func (a *Accounting) callThreadedPersistAccounting() {
	err := a.staticTG.Add()
	if err != nil {
		return
	}
	defer a.staticTG.Done()

	// Determine the initial interval for persisting the accounting information
	a.mu.Lock()
	lastPersistTime := time.Unix(a.persistence.Timestamp, 0)
	a.mu.Unlock()
	interval := time.Since(lastPersistTime)
	if interval >= persistInterval {
		// If it has been longer than the persistInterval then persist the
		// accounting information immediately
		err = a.managedUpdateAndPersistAccounting()
		if err != nil {
			a.staticLog.Println("WARN: Persist loop error:", err)
			interval = persistErrorInterval
		} else {
			interval = persistInterval
		}
	}

	// Persist the accounting information in a loop until there is a shutdown
	// event.
	for {
		select {
		case <-a.staticTG.StopChan():
			return
		case <-time.After(interval):
		}
		err = a.managedUpdateAndPersistAccounting()
		if err != nil {
			a.staticLog.Println("WARN: Persist loop error:", err)
			interval = persistErrorInterval
		} else {
			interval = persistInterval
		}
	}
}

// initPersist initializes the persistence for the Accounting module
func (a *Accounting) initPersist() error {
	// Make sure the persistence directory exists
	err := os.MkdirAll(a.staticPersistDir, modules.DefaultDirPerm)
	if err != nil {
		return errors.AddContext(err, "unable to create persistence directory")
	}

	// Initialize the log
	a.staticLog, err = persist.NewFileLogger(filepath.Join(a.staticPersistDir, logFile))
	if err != nil {
		return errors.AddContext(err, "unable to initialize the accounting log")
	}
	err = a.staticTG.AfterStop(a.staticLog.Close)
	if err != nil {
		return errors.AddContext(err, "unable to add log close to threadgroup AfterStop")
	}

	// Initialize the AOP
	var reader io.Reader
	a.staticAOP, reader, err = persist.NewAppendOnlyPersist(a.staticPersistDir, persistFile, metadataHeader, metadataVersion)
	if err != nil {
		return errors.AddContext(err, "unable to create AppendOnlyPersist")
	}
	err = a.staticTG.AfterStop(a.staticAOP.Close)
	if err != nil {
		return errors.AddContext(err, "unable to add AOP close to threadgroup AfterStop")
	}

	// Load the last persisted entry
	a.persistence, err = unmarshalLastPersistence(reader)
	if err != nil {
		return errors.AddContext(err, "unable to load the last persist entry")
	}

	return nil
}

// managedUpdateAndPersistAccounting will update the accounting information and write the
// information to disk.
func (a *Accounting) managedUpdateAndPersistAccounting() error {
	logStr := "Update and Persist error"
	// Update the persistence information
	_, err := a.callUpdateAccounting()
	if err != nil {
		err = errors.AddContext(err, "unable to update accounting information")
		a.staticLog.Printf("WARN: %v:%v", logStr, err)
		return err
	}

	// Marshall the persistence
	a.mu.Lock()
	p := a.persistence
	a.mu.Unlock()
	data, err := marshalPersistence(p)
	if err != nil {
		err = errors.AddContext(err, "unable to marshal persistence")
		a.staticLog.Printf("WARN: %v:%v", logStr, err)
		return err
	}

	// Persist
	_, err = a.staticAOP.Write(data[:])
	if err != nil {
		err = errors.AddContext(err, "unable to write persistence to disk")
		a.staticLog.Printf("WARN: %v:%v", logStr, err)
		return err
	}

	return nil
}

// marshalPersistence marshals the persistence.
func marshalPersistence(p persistence) (rpe [persistEntrySize]byte, err error) {
	// Marshal the persistence and ensure is is less that then max size
	data := encoding.Marshal(p)
	if len(data) > maxPersistSize {
		err = fmt.Errorf("Marshaled persistence is too big: persistence %v, max %v", len(data), maxPersistSize)
		build.Critical(err)
		return
	}

	// Create the persistEntry
	entry := persistEntry{
		Size: len(data),
	}
	copy(entry.Persistence[:], data)

	// Marshal the persistEntry and check the size
	encodedEntry := encoding.Marshal(entry)
	if len(encodedEntry) != persistEntrySize {
		err = fmt.Errorf("Marshaled persist entry incorrect size: entry %v, max %v", len(encodedEntry), persistEntrySize)
		build.Critical(err)
		return
	}

	// Copy the encodedEntry into the returned value
	copy(rpe[:], encodedEntry)
	return
}

// unmarshalLastPersistence will read through the reader until the last
// persist entry is found and will unmarshal and return that persistence.
func unmarshalLastPersistence(r io.Reader) (persistence, error) {
	var p persistence
	// Read through the reader until the last element
	for {
		buf := make([]byte, persistEntrySize)
		_, err := io.ReadFull(r, buf)
		if errors.Contains(err, io.EOF) {
			break
		}
		if err != nil {
			return persistence{}, errors.AddContext(err, "unable to read from reader")
		}
		// New entry found, unmarshal and overwrite any previous persistence
		p, err = unmarshalPersistence(buf)
		if err != nil {
			return persistence{}, errors.AddContext(err, "unable to unmarshal persistence")
		}
	}
	return p, nil
}

// unmarshalPersistence unmarshals the persist entry and then unmarshals the
// encoded persistence.
func unmarshalPersistence(entry []byte) (persistence, error) {
	// Unmarshal persistEntry
	var pe persistEntry
	err := encoding.Unmarshal(entry, &pe)
	if err != nil {
		return persistence{}, errors.AddContext(err, "unable to unmarshal persistEntry")
	}

	// Unmarshal persistence
	var p persistence
	err = encoding.Unmarshal(pe.Persistence[:], &p)
	if err != nil {
		return persistence{}, errors.AddContext(err, "unable to unmarshal persistence")
	}

	return p, nil
}
