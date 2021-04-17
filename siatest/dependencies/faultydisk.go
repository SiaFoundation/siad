package dependencies

import (
	"errors"
	"os"
	"sync"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/modules"
)

var (
	// ErrDiskFault is returned when a simulated disk error happens
	ErrDiskFault = errors.New("disk fault")
)

const (
	// DisruptFaultyFile defines the disrupt signature with which we can check if an
	// error was genuine or injected
	DisruptFaultyFile = "faultyFile"
)

// scrambleData takes some data as input and replaces parts of it randomly with
// random data
func scrambleData(d []byte) []byte {
	randomData := fastrand.Bytes(len(d))
	scrambled := make([]byte, len(d), len(d))
	for i := 0; i < len(d); i++ {
		if fastrand.Intn(4) == 0 { // 25% chance to replace byte
			scrambled[i] = randomData[i]
		} else {
			scrambled[i] = d[i]
		}
	}
	return scrambled
}

// DependencyFaultyDisk implements dependencies that simulate a faulty disk.
type DependencyFaultyDisk struct {
	modules.ProductionDependencies
	// failDenominator determines how likely it is that a write will fail,
	// defined as 1/failDenominator. Each write call increments
	// failDenominator, and it starts at 2. This means that the more calls to
	// WriteAt, the less likely the write is to fail. All calls will start
	// automatically failing after writeLimit writes.
	disabled        bool
	failed          bool
	failDenominator int
	writeLimit      int

	mu sync.Mutex
}

// NewFaultyDiskDependency creates a dependency that can be used to simulate a
// failing disk. writeLimit is the maximum number of writes the disk will
// endure before failing
func NewFaultyDiskDependency(writeLimit int) *DependencyFaultyDisk {
	return &DependencyFaultyDisk{
		writeLimit: writeLimit,
	}
}

// disabled allows the caller to temporarily disable the dependency
func (d *DependencyFaultyDisk) disable() {
	d.mu.Lock()
	d.disabled = true
	d.mu.Unlock()
}
func (d *DependencyFaultyDisk) enable() {
	d.mu.Lock()
	d.disabled = false
	d.mu.Unlock()
}

// Disrupt returns true if the faulty disk dependency is enabled to make sure we
// don't panic when updates can't be applied but instead are able to handle the
// error gracefully during testing.
func (d *DependencyFaultyDisk) Disrupt(s string) bool {
	return s == DisruptFaultyFile
}

// tryFail will check if the disk has failed yet, and if not, it'll rng to see
// if the disk should fail now. Returns 'true' if the disk has failed.
func (d *DependencyFaultyDisk) tryFail() bool {
	if d.disabled {
		return false
	}
	if d.failed {
		return true
	}

	d.failDenominator += fastrand.Intn(8)
	fail := fastrand.Intn(d.failDenominator+1) == 0 // +1 to prevent 0 from being passed in.
	if fail || d.failDenominator >= d.writeLimit {
		d.failed = true
		return true
	}
	return false
}

// newFaultyFile creates a new faulty file around the provided file handle.
func (d *DependencyFaultyDisk) newFaultyFile(f *os.File) modules.File {
	return &FaultyFile{d: d, file: f}
}

// Disable allows the caller to temporarily disable the dependency
func (d *DependencyFaultyDisk) Disable() {
	d.mu.Lock()
	d.disabled = true
	d.mu.Unlock()
}

// Enable allows the caller to re-enable the dependency
func (d *DependencyFaultyDisk) Enable() {
	d.mu.Lock()
	d.disabled = false
	d.mu.Unlock()
}

// Reset resets the failDenominator and the failed flag of the dependency
func (d *DependencyFaultyDisk) Reset() {
	d.mu.Lock()
	d.failDenominator = 0
	d.failed = false
	d.mu.Unlock()
}

// Open is an os.Open replacement
func (d *DependencyFaultyDisk) Open(path string) (modules.File, error) {
	return d.OpenFile(path, os.O_RDONLY, 0)
}

// OpenFile is an os.OpenFile replacement
func (d *DependencyFaultyDisk) OpenFile(path string, flag int, perm os.FileMode) (modules.File, error) {
	f, err := os.OpenFile(path, flag, perm)
	if err != nil {
		return nil, err
	}
	return d.newFaultyFile(f), nil
}

// FaultyFile implements a file that simulates a faulty disk.
type FaultyFile struct {
	d    *DependencyFaultyDisk
	file *os.File
}

// Read is an *File.Read replacement
func (f *FaultyFile) Read(p []byte) (int, error) {
	return f.file.Read(p)
}

// Write is a *File.Write replacement
func (f *FaultyFile) Write(p []byte) (int, error) {
	f.d.mu.Lock()
	defer f.d.mu.Unlock()
	if f.d.tryFail() {
		return f.file.Write(scrambleData(p))
	}
	return f.file.Write(p)
}

// Close is a *File.Close replacement
func (f *FaultyFile) Close() error { return f.file.Close() }

// Name returns the name of the file
func (f *FaultyFile) Name() string {
	return f.file.Name()
}

// ReadAt is a *File.ReadAt replacement
func (f *FaultyFile) ReadAt(p []byte, off int64) (int, error) {
	return f.file.ReadAt(p, off)
}

// Seek is a *File.Seek replacement
func (f *FaultyFile) Seek(offset int64, whence int) (int64, error) {
	return f.file.Seek(offset, whence)
}

// Truncate is a *File.Truncate replacement
func (f *FaultyFile) Truncate(size int64) error {
	return f.file.Truncate(size)
}

// WriteAt is a *File.WriteAt replacement
func (f *FaultyFile) WriteAt(p []byte, off int64) (int, error) {
	f.d.mu.Lock()
	defer f.d.mu.Unlock()
	if f.d.tryFail() {
		return f.file.WriteAt(scrambleData(p), off)
	}
	return f.file.WriteAt(p, off)
}

// Stat is a *File.Stat replacement
func (f *FaultyFile) Stat() (os.FileInfo, error) {
	return f.file.Stat()
}

// Sync is a *File.Sync replacement
func (f *FaultyFile) Sync() error {
	f.d.mu.Lock()
	defer f.d.mu.Unlock()
	if f.d.tryFail() {
		return ErrDiskFault
	}
	return f.file.Sync()
}
