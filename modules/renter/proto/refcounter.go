package proto

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sync"

	siasync "go.sia.tech/siad/sync"

	"go.sia.tech/siad/modules"

	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/errors"
)

var (
	// ErrInvalidHeaderData is returned when we try to deserialize the header from
	// a []byte with incorrect data
	ErrInvalidHeaderData = errors.New("invalid header data")

	// ErrInvalidSectorNumber is returned when the requested sector doesnt' exist
	ErrInvalidSectorNumber = errors.New("invalid sector given - it does not exist")

	// ErrInvalidVersion is returned when the version of the file we are trying to
	// read does not match the current refCounterHeaderSize
	ErrInvalidVersion = errors.New("invalid file version")

	// ErrInvalidUpdateInstruction is returned when trying to parse a WAL update
	// instruction that is too short to possibly contain all the required data.
	ErrInvalidUpdateInstruction = errors.New("instructions slice is too short to contain the required data")

	// ErrRefCounterNotExist is returned when there is no refcounter file with
	// the given path
	ErrRefCounterNotExist = errors.New("refcounter does not exist")

	// ErrUpdateWithoutUpdateSession is returned when an update operation is
	// called without an open update session
	ErrUpdateWithoutUpdateSession = errors.New("an update operation was called without an open update session")

	// ErrUpdateAfterDelete is returned when an update operation is attempted to
	// be created after a delete
	ErrUpdateAfterDelete = errors.New("updates cannot be created after a deletion")

	// refCounterVersion defines the latest version of the refCounter
	refCounterVersion = [8]byte{1}

	// updateNameRCDelete is the name of an idempotent update that deletes a file
	// from the disk.
	updateNameRCDelete = "RC_DELETE"

	// updateNameRCTruncate is the name of an idempotent update that truncates a
	// refcounter file by a number of sectors.
	updateNameRCTruncate = "RC_TRUNCATE"

	// updateNameRCWriteAt is the name of an idempotent update that writes a
	// value to a position in the file.
	updateNameRCWriteAt = "RC_WRITE_AT"
)

const (
	// refCounterHeaderSize is the size of the header in bytes
	refCounterHeaderSize = 8
)

type (
	// refCounter keeps track of how many references to each sector exist.
	//
	// Once the number of references drops to zero we consider the sector as
	// garbage. We move the sector to end of the data and set the
	// GarbageCollectionOffset to point to it. We can either reuse it to store
	// new data or drop it from the contract at the end of the current period
	// and before the contract renewal.
	refCounter struct {
		refCounterHeader

		filepath   string // where the refcounter is persisted on disk
		numSectors uint64 // used for sanity checks before we attempt mutation operations
		staticWal  *writeaheadlog.WAL
		mu         sync.Mutex

		// utility fields
		staticDeps modules.Dependencies

		refCounterUpdateControl
	}

	// refCounterHeader contains metadata about the reference counter file
	refCounterHeader struct {
		Version [8]byte
	}

	// refCounterUpdateControl is a helper struct that holds fields pertaining
	// to the process of updating the refcounter
	refCounterUpdateControl struct {
		// isDeleted marks when a refcounter has been deleted and therefore
		// cannot accept further updates
		isDeleted bool
		// isUpdateInProgress marks when an update session is open and updates
		// are allowed to be created and applied
		isUpdateInProgress bool
		// newSectorCounts holds the new values of sector counters during an
		// update session, so we can use them even before they are stored on
		// disk
		newSectorCounts map[uint64]uint16

		// muUpdate serializes updates to the refcounter. It is acquired by
		// callStartUpdate and released by callUpdateApplied.
		muUpdate siasync.TryMutex
	}

	// u16 is a utility type for ser/des of uint16 values
	u16 [2]byte
)

// loadRefCounter loads a refcounter from disk
func loadRefCounter(path string, wal *writeaheadlog.WAL) (_ *refCounter, err error) {
	// Open the file and start loading the data.
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrRefCounterNotExist
	}
	defer func() {
		err = errors.Compose(err, f.Close())
	}()

	var header refCounterHeader
	headerBytes := make([]byte, refCounterHeaderSize)
	if _, err = f.ReadAt(headerBytes, 0); err != nil {
		return nil, errors.AddContext(err, "unable to read from file")
	}
	if err = deserializeHeader(headerBytes, &header); err != nil {
		return nil, errors.AddContext(err, "unable to load refcounter header")
	}
	if header.Version != refCounterVersion {
		return nil, errors.AddContext(ErrInvalidVersion, fmt.Sprintf("expected version %d, got version %d", refCounterVersion, header.Version))
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, errors.AddContext(err, "failed to read file stats")
	}
	numSectors := uint64((fi.Size() - refCounterHeaderSize) / 2)
	return &refCounter{
		refCounterHeader: header,
		filepath:         path,
		numSectors:       numSectors,
		staticWal:        wal,
		staticDeps:       modules.ProdDependencies,
		refCounterUpdateControl: refCounterUpdateControl{
			newSectorCounts: make(map[uint64]uint16),
		},
	}, nil
}

// newCustomRefCounter creates a new sector reference counter file to accompany
// a contract file and allows setting custom dependencies
func newCustomRefCounter(path string, numSec uint64, wal *writeaheadlog.WAL, deps modules.Dependencies) (*refCounter, error) {
	h := refCounterHeader{
		Version: refCounterVersion,
	}
	updateHeader := writeaheadlog.WriteAtUpdate(path, 0, serializeHeader(h))

	b := make([]byte, numSec*2)
	for i := uint64(0); i < numSec; i++ {
		binary.LittleEndian.PutUint16(b[i*2:i*2+2], 1)
	}
	updateCounters := writeaheadlog.WriteAtUpdate(path, refCounterHeaderSize, b)

	err := wal.CreateAndApplyTransaction(writeaheadlog.ApplyUpdates, updateHeader, updateCounters)
	return &refCounter{
		refCounterHeader: h,
		filepath:         path,
		numSectors:       numSec,
		staticWal:        wal,
		staticDeps:       deps,
		refCounterUpdateControl: refCounterUpdateControl{
			newSectorCounts: make(map[uint64]uint16),
		},
	}, err
}

// newRefCounter creates a new sector reference counter file to accompany
// a contract file
func newRefCounter(path string, numSec uint64, wal *writeaheadlog.WAL) (*refCounter, error) {
	return newCustomRefCounter(path, numSec, wal, modules.ProdDependencies)
}

// callAppend appends one counter to the end of the refcounter file and
// initializes it with `1`
func (rc *refCounter) callAppend() (writeaheadlog.Update, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if !rc.isUpdateInProgress {
		return writeaheadlog.Update{}, ErrUpdateWithoutUpdateSession
	}
	if rc.isDeleted {
		return writeaheadlog.Update{}, ErrUpdateAfterDelete
	}
	rc.numSectors++
	rc.newSectorCounts[rc.numSectors-1] = 1
	return createWriteAtUpdate(rc.filepath, rc.numSectors-1, 1), nil
}

// callCount returns the number of references to the given sector
func (rc *refCounter) callCount(secIdx uint64) (uint16, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.readCount(secIdx)
}

// callCreateAndApplyTransaction is a helper method that creates a writeaheadlog
// transaction and applies it.
func (rc *refCounter) callCreateAndApplyTransaction(updates ...writeaheadlog.Update) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	// We allow the creation of the file here because of the case where we got
	// interrupted during the creation of the refcounter after writing the
	// header update to the Wal but before applying it.
	f, err := rc.staticDeps.OpenFile(rc.filepath, os.O_CREATE|os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "failed to open refcounter file in order to apply updates")
	}
	defer func() {
		err = errors.Compose(err, f.Close())
	}()
	if !rc.isUpdateInProgress {
		return ErrUpdateWithoutUpdateSession
	}
	// Create the writeaheadlog transaction.
	txn, err := rc.staticWal.NewTransaction(updates)
	if err != nil {
		return errors.AddContext(err, "failed to create wal txn")
	}
	// No extra setup is required. Signal that it is done.
	if err := <-txn.SignalSetupComplete(); err != nil {
		return errors.AddContext(err, "failed to signal setup completion")
	}
	// Starting at this point, the changes to be made are written to the disk.
	// This means that we need to panic in case applying the updates fails in
	// order to avoid data corruption.
	defer func() {
		if err != nil {
			panic(err)
		}
	}()
	// Apply the updates.
	if err = applyUpdates(f, updates...); err != nil {
		return errors.AddContext(err, "failed to apply updates")
	}
	// Updates are applied. Let the writeaheadlog know.
	if err = txn.SignalUpdatesApplied(); err != nil {
		return errors.AddContext(err, "failed to signal that updates are applied")
	}
	// If the refcounter got deleted then we're done.
	if rc.isDeleted {
		return nil
	}
	// Update the in-memory helper fields.
	fi, err := os.Stat(rc.filepath)
	if err != nil {
		return errors.AddContext(err, "failed to read from disk after updates")
	}
	rc.numSectors = uint64((fi.Size() - refCounterHeaderSize) / 2)
	return nil
}

// callDecrement decrements the reference counter of a given sector. The sector
// is specified by its sequential number (secIdx).
// Returns the updated number of references or an error.
func (rc *refCounter) callDecrement(secIdx uint64) (writeaheadlog.Update, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if !rc.isUpdateInProgress {
		return writeaheadlog.Update{}, ErrUpdateWithoutUpdateSession
	}
	if rc.isDeleted {
		return writeaheadlog.Update{}, ErrUpdateAfterDelete
	}
	if secIdx >= rc.numSectors {
		return writeaheadlog.Update{}, errors.AddContext(ErrInvalidSectorNumber, "failed to decrement")
	}
	count, err := rc.readCount(secIdx)
	if err != nil {
		return writeaheadlog.Update{}, errors.AddContext(err, "failed to read count from decrement")
	}
	if count == 0 {
		return writeaheadlog.Update{}, errors.New("sector count underflow")
	}
	count--
	rc.newSectorCounts[secIdx] = count
	return createWriteAtUpdate(rc.filepath, secIdx, count), nil
}

// callDeleteRefCounter deletes the counter's file from disk
func (rc *refCounter) callDeleteRefCounter() (writeaheadlog.Update, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if !rc.isUpdateInProgress {
		return writeaheadlog.Update{}, ErrUpdateWithoutUpdateSession
	}
	if rc.isDeleted {
		return writeaheadlog.Update{}, ErrUpdateAfterDelete
	}
	// mark the refcounter as deleted and don't allow any further updates to be created
	rc.isDeleted = true
	return createDeleteUpdate(rc.filepath), nil
}

// callDropSectors removes the last numSec sector counts from the refcounter file
func (rc *refCounter) callDropSectors(numSec uint64) (writeaheadlog.Update, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if !rc.isUpdateInProgress {
		return writeaheadlog.Update{}, ErrUpdateWithoutUpdateSession
	}
	if rc.isDeleted {
		return writeaheadlog.Update{}, ErrUpdateAfterDelete
	}
	if numSec > rc.numSectors {
		return writeaheadlog.Update{}, errors.AddContext(ErrInvalidSectorNumber, "failed to drop sectors")
	}
	rc.numSectors -= numSec
	return createTruncateUpdate(rc.filepath, rc.numSectors), nil
}

// callIncrement increments the reference counter of a given sector. The sector
// is specified by its sequential number (secIdx).
// Returns the updated number of references or an error.
func (rc *refCounter) callIncrement(secIdx uint64) (writeaheadlog.Update, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if !rc.isUpdateInProgress {
		return writeaheadlog.Update{}, ErrUpdateWithoutUpdateSession
	}
	if rc.isDeleted {
		return writeaheadlog.Update{}, ErrUpdateAfterDelete
	}
	if secIdx >= rc.numSectors {
		return writeaheadlog.Update{}, errors.AddContext(ErrInvalidSectorNumber, "failed to increment")
	}
	count, err := rc.readCount(secIdx)
	if err != nil {
		return writeaheadlog.Update{}, errors.AddContext(err, "failed to read count from increment")
	}
	if count == math.MaxUint16 {
		return writeaheadlog.Update{}, errors.New("sector count overflow")
	}
	count++
	rc.newSectorCounts[secIdx] = count
	return createWriteAtUpdate(rc.filepath, secIdx, count), nil
}

// callSetCount sets the value of the reference counter of a given sector. The
// sector is specified by its sequential number (secIdx).
func (rc *refCounter) callSetCount(secIdx uint64, c uint16) (writeaheadlog.Update, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if !rc.isUpdateInProgress {
		return writeaheadlog.Update{}, ErrUpdateWithoutUpdateSession
	}
	if rc.isDeleted {
		return writeaheadlog.Update{}, ErrUpdateAfterDelete
	}
	// this allows the client to set multiple new counts in random order
	if secIdx >= rc.numSectors {
		rc.numSectors = secIdx + 1
	}
	rc.newSectorCounts[secIdx] = c
	return createWriteAtUpdate(rc.filepath, secIdx, c), nil
}

// callStartUpdate acquires a lock, ensuring the caller is the only one currently
// allowed to perform updates on this refcounter file. This lock is released by
// calling callUpdateApplied after calling callCreateAndApplyTransaction in
// order to apply the updates.
func (rc *refCounter) callStartUpdate() error {
	rc.muUpdate.Lock()
	return rc.managedStartUpdate()
}

// callSwap swaps the two sectors at the given indices
func (rc *refCounter) callSwap(firstIdx, secondIdx uint64) ([]writeaheadlog.Update, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if !rc.isUpdateInProgress {
		return []writeaheadlog.Update{}, ErrUpdateWithoutUpdateSession
	}
	if rc.isDeleted {
		return []writeaheadlog.Update{}, ErrUpdateAfterDelete
	}
	if firstIdx >= rc.numSectors || secondIdx >= rc.numSectors {
		return []writeaheadlog.Update{}, errors.AddContext(ErrInvalidSectorNumber, "failed to swap sectors")
	}
	firstVal, err := rc.readCount(firstIdx)
	if err != nil {
		return []writeaheadlog.Update{}, errors.AddContext(err, "failed to read count from swap")
	}
	secondVal, err := rc.readCount(secondIdx)
	if err != nil {
		return []writeaheadlog.Update{}, errors.AddContext(err, "failed to read count from swap")
	}
	rc.newSectorCounts[firstIdx] = secondVal
	rc.newSectorCounts[secondIdx] = firstVal
	return []writeaheadlog.Update{
		createWriteAtUpdate(rc.filepath, firstIdx, secondVal),
		createWriteAtUpdate(rc.filepath, secondIdx, firstVal),
	}, nil
}

// callUpdateApplied cleans up temporary data and releases the update lock, thus
// allowing other actors to acquire it in order to update the refcounter.
func (rc *refCounter) callUpdateApplied() error {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	// this method cannot be called if there is no active update session
	if !rc.isUpdateInProgress {
		return ErrUpdateWithoutUpdateSession
	}

	// clean up the temp counts
	rc.newSectorCounts = make(map[uint64]uint16)
	// close the update session
	rc.isUpdateInProgress = false
	// release the update lock
	rc.muUpdate.Unlock()
	return nil
}

// managedStartUpdate does everything callStartUpdate needs, aside from acquiring a
// lock
func (rc *refCounter) managedStartUpdate() error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.isDeleted {
		return ErrUpdateAfterDelete
	}
	// open an update session
	rc.isUpdateInProgress = true
	return nil
}

// readCount reads the given sector count either from disk (if there are no
// pending updates) or from the in-memory cache (if there are).
func (rc *refCounter) readCount(secIdx uint64) (_ uint16, err error) {
	// check if the secIdx is a valid sector index based on the number of
	// sectors in the file
	if secIdx >= rc.numSectors {
		return 0, errors.AddContext(ErrInvalidSectorNumber, "failed to read count")
	}
	// check if the value is being changed by a pending update
	if count, ok := rc.newSectorCounts[secIdx]; ok {
		return count, nil
	}
	// read the value from disk
	f, err := rc.staticDeps.Open(rc.filepath)
	if err != nil {
		return 0, errors.AddContext(err, "failed to open the refcounter file")
	}
	defer func() {
		err = errors.Compose(err, f.Close())
	}()

	var b u16
	if _, err = f.ReadAt(b[:], int64(offset(secIdx))); err != nil {
		return 0, errors.AddContext(err, "failed to read from refcounter file")
	}
	return binary.LittleEndian.Uint16(b[:]), nil
}

// applyUpdates takes a list of WAL updates and applies them.
func applyUpdates(f modules.File, updates ...writeaheadlog.Update) (err error) {
	for _, update := range updates {
		switch update.Name {
		case updateNameRCDelete:
			err = applyDeleteUpdate(update)
		case updateNameRCTruncate:
			err = applyTruncateUpdate(f, update)
		case updateNameRCWriteAt:
			err = applyWriteAtUpdate(f, update)
		default:
			err = fmt.Errorf("unknown update type: %v", update.Name)
		}
		if err != nil {
			return err
		}
	}
	return f.Sync()
}

// createDeleteUpdate is a helper function which creates a writeaheadlog update
// for deleting a given refcounter file.
func createDeleteUpdate(path string) writeaheadlog.Update {
	return writeaheadlog.Update{
		Name:         updateNameRCDelete,
		Instructions: []byte(path),
	}
}

// applyDeleteUpdate parses and applies a Delete update.
func applyDeleteUpdate(update writeaheadlog.Update) error {
	if update.Name != updateNameRCDelete {
		return fmt.Errorf("applyDeleteUpdate called on update of type %v", update.Name)
	}
	// Remove the file and ignore the NotExist error
	if err := os.Remove(string(update.Instructions)); !os.IsNotExist(err) {
		return err
	}
	return nil
}

// createTruncateUpdate is a helper function which creates a writeaheadlog
// update for truncating a number of sectors from the end of the file.
func createTruncateUpdate(path string, newNumSec uint64) writeaheadlog.Update {
	b := make([]byte, 8+len(path))
	binary.LittleEndian.PutUint64(b[:8], newNumSec)
	copy(b[8:8+len(path)], path)
	return writeaheadlog.Update{
		Name:         updateNameRCTruncate,
		Instructions: b,
	}
}

// applyTruncateUpdate parses and applies a Truncate update.
func applyTruncateUpdate(f modules.File, u writeaheadlog.Update) error {
	if u.Name != updateNameRCTruncate {
		return fmt.Errorf("applyAppendTruncate called on update of type %v", u.Name)
	}
	// Decode update.
	_, newNumSec, err := readTruncateUpdate(u)
	if err != nil {
		return err
	}
	// Truncate the file to the needed size.
	return f.Truncate(refCounterHeaderSize + int64(newNumSec)*2)
}

// createWriteAtUpdate is a helper function which creates a writeaheadlog
// update for swapping the values of two positions in the file.
func createWriteAtUpdate(path string, secIdx uint64, value uint16) writeaheadlog.Update {
	b := make([]byte, 8+2+len(path))
	binary.LittleEndian.PutUint64(b[:8], secIdx)
	binary.LittleEndian.PutUint16(b[8:10], value)
	copy(b[10:10+len(path)], path)
	return writeaheadlog.Update{
		Name:         updateNameRCWriteAt,
		Instructions: b,
	}
}

// applyWriteAtUpdate parses and applies a WriteAt update.
func applyWriteAtUpdate(f modules.File, u writeaheadlog.Update) error {
	if u.Name != updateNameRCWriteAt {
		return fmt.Errorf("applyAppendWriteAt called on update of type %v", u.Name)
	}
	// Decode update.
	_, secIdx, value, err := readWriteAtUpdate(u)
	if err != nil {
		return err
	}

	// Write the value to disk.
	var b u16
	binary.LittleEndian.PutUint16(b[:], value)
	_, err = f.WriteAt(b[:], int64(offset(secIdx)))
	return err
}

// deserializeHeader deserializes a header from []byte
func deserializeHeader(b []byte, h *refCounterHeader) error {
	if uint64(len(b)) < refCounterHeaderSize {
		return ErrInvalidHeaderData
	}
	copy(h.Version[:], b[:8])
	return nil
}

// offset calculates the byte offset of the sector counter in the file on disk
func offset(secIdx uint64) uint64 {
	return refCounterHeaderSize + secIdx*2
}

// readTruncateUpdate decodes a Truncate update
func readTruncateUpdate(u writeaheadlog.Update) (path string, newNumSec uint64, err error) {
	if len(u.Instructions) < 8 {
		err = ErrInvalidUpdateInstruction
		return
	}
	newNumSec = binary.LittleEndian.Uint64(u.Instructions[:8])
	path = string(u.Instructions[8:])
	return
}

// readWriteAtUpdate decodes a WriteAt update
func readWriteAtUpdate(u writeaheadlog.Update) (path string, secIdx uint64, value uint16, err error) {
	if len(u.Instructions) < 10 {
		err = ErrInvalidUpdateInstruction
		return
	}
	secIdx = binary.LittleEndian.Uint64(u.Instructions[:8])
	value = binary.LittleEndian.Uint16(u.Instructions[8:10])
	path = string(u.Instructions[10:])
	return
}

// serializeHeader serializes a header to []byte
func serializeHeader(h refCounterHeader) []byte {
	b := make([]byte, refCounterHeaderSize)
	copy(b[:8], h.Version[:])
	return b
}
