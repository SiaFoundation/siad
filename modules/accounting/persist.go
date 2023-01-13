package accounting

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/types"
)

const (
	// logFile is the name of the log file for the Accounting module.
	logFile string = modules.AccountingDir + ".log"

	// persistFile is the name of the persist file
	persistFile string = "accounting"
)

var (
	// metadataHeader is the header of the metadata for the persist file
	metadataHeader = types.NewSpecifier("Accounting\n")

	// persistErrorInterval is the interval at which the persist loop will wait in
	// the event of an error.
	persistErrorInterval = build.Select(build.Var{
		Dev:      time.Second,
		Standard: time.Minute,
		Testnet:  time.Minute,
		Testing:  time.Millisecond * 100,
	}).(time.Duration)

	// persistInterval is the interval at which the accounting information will be
	// persisted.
	persistInterval = build.Select(build.Var{
		Dev:      time.Minute,
		Standard: time.Hour * 24,
		Testnet:  time.Hour * 24,
		Testing:  time.Second,
	}).(time.Duration)
)

// persistence contains the accounting information that is persisted on disk
type persistence struct {
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
	interval := persistInterval - time.Since(lastPersistTime)
	if interval <= 0 {
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
	a.staticAOP, reader, err = persist.NewAppendOnlyPersist(a.staticPersistDir, persistFile, metadataHeader, persist.MetadataVersionv156)
	if err != nil {
		return errors.AddContext(err, "unable to create AppendOnlyPersist")
	}
	err = a.staticTG.AfterStop(a.staticAOP.Close)
	if err != nil {
		return errors.AddContext(err, "unable to add AOP close to threadgroup AfterStop")
	}

	// Unmarshal the persistence
	persistence, err := unmarshalPersistence(reader)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal persistence")
	}

	// Keep the last persist entry in memory
	if len(persistence) > 0 {
		a.persistence = persistence[len(persistence)-1]
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
func marshalPersistence(p persistence) ([]byte, error) {
	// Marshal the persistence
	data, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// unmarshalPersistence uses a json Decoder to read the persisted json entries
// and unmarshals them.
func unmarshalPersistence(r io.Reader) ([]persistence, error) {
	// Create decoder
	d := json.NewDecoder(r)

	var persist []persistence
	for {
		// Decode persisted json entry
		var p persistence
		err := d.Decode(&p)
		if errors.Contains(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.AddContext(err, "unable to read from reader")
		}
		// Append to persist
		persist = append(persist, p)
	}
	return persist, nil
}
