package mdm

import (
	"io"

	"gitlab.com/NebulousLabs/Sia/crypto"
)

// ProgramData is a buffer for the program data. It will read packets from r and
// append them to data.
type ProgramData struct {
	// data contains the already received data.
	data []byte

	// staticLength is the expected length of the program data. This is the
	// amount of data that was paid for and not more than that will be read from
	// the reader. Less data will be considered an unexpected EOF.
	staticLength uint64

	// r is the reader used to fetch more data.
	r io.Reader
}

// NewProgramData creates a new ProgramData object from the specified reader. It
// will read from the reader until dataLength is reached.
func NewProgramData(r io.Reader, dataLength uint64) *ProgramData {
	pd := &ProgramData{
		r: r,
	}
	go pd.threadedFetchData()
	return pd
}

// threadedFetchData fetches the program's data from the underlying reader of
// the ProgramData. It will read from the reader until io.EOF is reached or
// until the maximum number of packets are read.
func (pd *ProgramData) threadedFetchData() {
	panic("not implemented yet")
}

// Uint64 returns the next 8 bytes at the specified offset within the program
// data as an uint64. This call will block if the data at the specified offset
// hasn't been fetched yet.
func (pd *ProgramData) Uint64(offset uint64) (uint64, error) {
	panic("not implemented yet")
}

// Hash returns the next crypto.HashSize bytes at the specified offset within
// the program data as a crypto.Hash. This call will block if the data at the
// specified offset hasn't been fetched yet.
func (pd *ProgramData) Hash(offset uint64) (crypto.Hash, error) {
	panic("not implemented yet")
}

// Len returns the length of the program data.
func (pd *ProgramData) Len() uint64 {
	return pd.staticLength
}
