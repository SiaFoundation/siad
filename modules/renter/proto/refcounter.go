package proto

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"regexp"
	"sync"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/errors"
)

var (
	// RefCounterHeaderSize is the size of the header in bytes
	RefCounterHeaderSize = int64(8)

	// RefCounterVersion defines the latest version of the RefCounter
	RefCounterVersion = [8]byte{1, 0, 0, 0, 0, 0, 0, 0}

	// ErrInvalidFilePath is thrown when we try to create a RefCounter but supply
	// a bad file path. A correct file path consists of:
	// a correct path + FileContractID hash + refCounterExtension
	ErrInvalidFilePath = errors.New("invalid refcounter file path")

	// ErrInvalidHeaderData is thrown when we try to deserialize the header from
	// a []byte with incorrect data
	ErrInvalidHeaderData = errors.New("invalid header data")

	// ErrInvalidSectorNumber is thrown when the requested sector doesnt' exist
	ErrInvalidSectorNumber = errors.New("invalid sector given - it does not exist")

	// we initialise the counters with this relatively high value, so we don't run
	// the risk of the sectors being accidentally marked as garbage if someone
	// deletes a number of backups before we've been able to get the actual
	// redundancy value
	initialCounterValue = uint16(1024)
)

type (
	// RefCounter keeps track of how many references to each sector exist.
	//
	// Once the number of references drops to zero we consider the sector as
	// garbage. We move the sector to end of the data and set the
	// GarbageCollectionOffset to point to it. We can either reuse it to store new
	// data or drop it from the contract at the end of the current period and
	// before the contract renewal.
	RefCounter struct {
		RefCounterHeader

		filepath     string   // where the refcounter is persisted on disk
		sectorCounts []uint16 // number of references per sector
		mu           sync.Mutex
	}

	// RefCounterHeader contains metadata about the reference counter file
	RefCounterHeader struct {
		Version [8]byte
	}
)

// IncrementCount increments the reference counter of a given sector. The sector
// is specified by its sequential number (`secNum`).
// Returns the updated number of references or an error.
func (rc *RefCounter) IncrementCount(secNum int64) (uint16, error) {
	if secNum < 0 || secNum >= int64(len(rc.sectorCounts)) {
		return 0, ErrInvalidSectorNumber
	}
	if rc.sectorCounts[secNum] == math.MaxUint16 {
		// TODO: Handle the overflow!
	}
	rc.sectorCounts[secNum]++
	return rc.sectorCounts[secNum], rc.syncCountToDisk(secNum, rc.sectorCounts[secNum])
}

// DecrementCount decrements the reference counter of a given sector. The sector
// is specified by its sequential number (`secNum`).
// Returns the updated number of references or an error.
func (rc *RefCounter) DecrementCount(secNum int64) (uint16, error) {
	if secNum < 0 || secNum >= int64(len(rc.sectorCounts)) {
		return 0, ErrInvalidSectorNumber
	}
	// we check before decrementing in order to avoid a possible underflow if the
	// counter is somehow zero
	if rc.sectorCounts[secNum] <= 1 {
		rc.managedMarkSectorAsGarbage(secNum)
		return 0, nil
	}
	rc.sectorCounts[secNum]--
	return rc.sectorCounts[secNum], rc.syncCountToDisk(secNum, rc.sectorCounts[secNum])
}

// GetCount returns the number of references to the given sector
// Note: Use a pointer because a gigabyte file has >512B RefCounter object.
func (rc *RefCounter) GetCount(secNum int64) (uint16, error) {
	if secNum < 0 || secNum >= int64(len(rc.sectorCounts)) {
		return 0, ErrInvalidSectorNumber
	}
	return rc.sectorCounts[secNum], nil
}

// DeleteRefCounter deletes the counter's file from disk
func (rc *RefCounter) DeleteRefCounter() (err error) {
	return os.Remove(rc.filepath)
}

func (rc *RefCounter) managedMarkSectorAsGarbage(secNum int64) {
	// this method is unexported and the secNum validation is already done.
	// TODO: perform the sector drop in the contract
	rc.mu.Lock()
	rc.sectorCounts[secNum] = rc.sectorCounts[len(rc.sectorCounts)-1]
	rc.sectorCounts = rc.sectorCounts[:len(rc.sectorCounts)-1] // drop the last one
	rc.dropSectorFromFile(secNum)                              // TODO: use WAL
	rc.mu.Unlock()
}

// Stores the given sector count on disk
func (rc *RefCounter) syncCountToDisk(secNum int64, c uint16) error {
	f, err := os.OpenFile(rc.filepath, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	bytes := make([]byte, 2, 2)
	binary.LittleEndian.PutUint16(bytes, c)
	if _, err = f.WriteAt(bytes, offset(secNum)); err != nil {
		return err
	}
	return f.Sync()
}

func (rc *RefCounter) dropSectorFromFile(secNum int64) error {
	f, err := os.OpenFile(rc.filepath, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := os.Stat(rc.filepath)
	if err != nil {
		return err
	}
	b := make([]byte, 2, 2)
	if _, err = f.ReadAt(b, info.Size()-2); err != nil {
		return err
	}
	if _, err = f.WriteAt(b, offset(secNum)); err != nil {
		return err
	}
	return f.Truncate(info.Size() - 2)
}

// NewRefCounter creates a new sector reference counter file to accompany a contract file
func NewRefCounter(path string, numSectors int64) (RefCounter, error) {
	if !isValidRefCounterPath(path) {
		return RefCounter{}, ErrInvalidFilePath
	}
	f, err := os.Create(path)
	if err != nil {
		return RefCounter{}, err
	}
	defer f.Close()
	h := RefCounterHeader{
		Version: RefCounterVersion,
	}

	if _, err := f.WriteAt(serializeHeader(h), 0); err != nil {
		return RefCounter{}, err
	}

	zeroCounts := make([]uint16, numSectors, numSectors)
	for i := int64(0); i < numSectors; i++ {
		zeroCounts[i] = initialCounterValue
	}
	zeroBytes := make([]byte, numSectors*2, numSectors*2)
	for i := range zeroCounts {
		binary.LittleEndian.PutUint16(zeroBytes[i*2:i*2+2], zeroCounts[i])
	}
	if _, err := f.WriteAt(zeroBytes, RefCounterHeaderSize); err != nil {
		return RefCounter{}, err
	}
	if err := f.Sync(); err != nil {
		return RefCounter{}, err
	}
	return RefCounter{
		RefCounterHeader: h,
		sectorCounts:     zeroCounts,
		filepath:         path,
	}, nil
}

// LoadRefCounter loads a refcounter from disk
func LoadRefCounter(path string) (RefCounter, error) {
	f, err := os.Open(path)
	if err != nil {
		return RefCounter{}, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return RefCounter{}, err
	}

	var header RefCounterHeader
	headerBytes := make([]byte, RefCounterHeaderSize, RefCounterHeaderSize)
	if _, err = f.ReadAt(headerBytes, 0); err != nil {
		return RefCounter{}, errors.AddContext(err, "unable to read from file")
	}
	if err = deserializeHeader(headerBytes, &header); err != nil {
		return RefCounter{}, errors.AddContext(err, "unable to load refcounter header")
	}

	numSectors := (stat.Size() - RefCounterHeaderSize) / 2
	sectorBytes := make([]byte, numSectors*2, numSectors*2)
	if _, err = f.ReadAt(sectorBytes, RefCounterHeaderSize); err != nil {
		return RefCounter{}, err
	}
	sectorCounts := make([]uint16, numSectors, numSectors)
	for i := int64(0); i < numSectors; i++ {
		sectorCounts[i] = binary.LittleEndian.Uint16(sectorBytes[i*2 : i*2+2])
	}
	return RefCounter{
		RefCounterHeader: header,
		filepath:         path,
		sectorCounts:     sectorCounts,
	}, nil
}

func deserializeHeader(b []byte, h *RefCounterHeader) error {
	if int64(len(b)) < RefCounterHeaderSize {
		return ErrInvalidHeaderData
	}
	copy(h.Version[:], b[:8])
	return nil
}

func isValidRefCounterPath(path string) bool {
	r, err := regexp.Compile(fmt.Sprintf(`^.*[0-9a-f]{64}\%s$`, refCounterExtension))
	if err != nil {
		build.Critical("refCounterExtension's value breaks our regex", err)
		return false
	}
	return r.Match([]byte(path))
}

func offset(secNum int64) int64 {
	return RefCounterHeaderSize + secNum*2
}

func serializeHeader(h RefCounterHeader) []byte {
	b := make([]byte, RefCounterHeaderSize, RefCounterHeaderSize)
	copy(b[:8], h.Version[:])
	return b
}
