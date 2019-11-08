package siadir

import (
	"os"

	"gitlab.com/NebulousLabs/errors"
)

// DirReader is a helper type that allows reading a raw .siadir from disk while
// keeping the file in memory locked.
type DirReader struct {
	f  *os.File
	sd *SiaDir
}

// Close closes the underlying file.
func (sdr *DirReader) Close() error {
	defer sdr.sd.mu.Unlock()
	return sdr.f.Close()
}

// Read calls Read on the underlying file.
func (sdr *DirReader) Read(b []byte) (int, error) {
	return sdr.f.Read(b)
}

// Stat returns the FileInfo of the underlying file.
func (sdr *DirReader) Stat() (os.FileInfo, error) {
	return sdr.f.Stat()
}

// DirReader creates a io.ReadCloser that can be used to read the raw SiaDir
// from disk.
func (sd *SiaDir) DirReader() (*DirReader, error) {
	sd.mu.Lock()
	if sd.deleted {
		sd.mu.Unlock()
		return nil, errors.New("can't copy deleted SiaDir")
	}
	// Open file.
	path := sd.siaPath.SiaDirMetadataSysPath(sd.rootDir)
	f, err := os.Open(path)
	if err != nil {
		sd.mu.Unlock()
		return nil, err
	}
	return &DirReader{
		sd: sd,
		f:  f,
	}, nil
}
