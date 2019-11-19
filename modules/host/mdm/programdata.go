package mdm

import (
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"sync"

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

	// readErr contains the first error encountered by threadedFetchData.
	readErr error

	// requests are queued up calls to 'bytes' waiting for the requested data to
	// arrive.
	requests []dataRequest

	// cancel is used to cancel the background thread.
	cancel chan struct{}

	// wg is used to wait for the background thread to finish.
	wg sync.WaitGroup

	mu sync.Mutex
}

type dataRequest struct {
	requiredLength uint64
	c              chan struct{}
}

// NewProgramData creates a new ProgramData object from the specified reader. It
// will read from the reader until dataLength is reached.
func NewProgramData(r io.Reader, dataLength uint64) *ProgramData {
	pd := &ProgramData{
		cancel:       make(chan struct{}),
		r:            r,
		staticLength: dataLength,
	}
	pd.wg.Add(1)
	go func() {
		defer pd.wg.Done()
		pd.threadedFetchData()
	}()
	return pd
}

// threadedFetchData fetches the program's data from the underlying reader of
// the ProgramData. It will read from the reader until io.EOF is reached or
// until the maximum number of packets are read.
func (pd *ProgramData) threadedFetchData() {
	packet := make([]byte, 1<<11) // 1kib
	remainingData := int64(pd.staticLength)
	for remainingData > 0 {
		select {
		case <-pd.cancel:
			return
		default:
		}
		// Adjust the length of the packet according to the remaining data.
		if remainingData <= int64(cap(packet)) {
			packet = packet[:remainingData]
		}
		n, err := pd.r.Read(packet)
		if err != nil {
			pd.mu.Lock()
			// Remember the error and close all open requests before stopping
			// the loop.
			pd.readErr = err
			for _, r := range pd.requests {
				close(r.c)
			}
			pd.mu.Unlock()
			break
		}
		pd.mu.Lock()
		remainingData -= int64(n)
		pd.data = append(pd.data, packet[:n]...)

		// Sort the request and unlock the ones that are ready to be unlocked.
		sort.Slice(pd.requests, func(i, j int) bool {
			return pd.requests[i].requiredLength < pd.requests[j].requiredLength
		})
		for len(pd.requests) > 0 {
			r := pd.requests[0]
			if r.requiredLength > uint64(len(pd.data)) {
				break
			}
			close(r.c)
			pd.requests = pd.requests[1:]
		}
		pd.mu.Unlock()
	}
}

// managedBytes tries to fetch length bytes at offset from the underlying data slice of
// the ProgramData. If the data is not available yet,
func (pd *ProgramData) managedBytes(offset, length uint64) ([]byte, error) {
	// Check if request is valid.
	if offset+length > pd.staticLength {
		return nil, fmt.Errorf("offset+length out of bounds: %v > %v", offset+length, pd.staticLength)
	}
	// Check if data is available already.
	pd.mu.Lock()
	if uint64(len(pd.data)) >= offset+length {
		defer pd.mu.Unlock()
		return pd.data[offset:], nil
	}
	// If not, queue up a request.
	c := make(chan struct{})
	pd.requests = append(pd.requests, dataRequest{
		requiredLength: offset + length,
		c:              c,
	})
	pd.mu.Unlock()
	<-c
	pd.mu.Lock()
	defer pd.mu.Unlock()
	// Check if the data is available again. It should be unless there was a
	// reading error.
	if uint64(len(pd.data)) < offset+length {
		return nil, pd.readErr
	}
	return pd.data[offset:][:length], nil
}

// Uint64 returns the next 8 bytes at the specified offset within the program
// data as an uint64. This call will block if the data at the specified offset
// hasn't been fetched yet.
func (pd *ProgramData) Uint64(offset uint64) (uint64, error) {
	d, err := pd.managedBytes(offset, 8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(d), nil
}

// Hash returns the next crypto.HashSize bytes at the specified offset within
// the program data as a crypto.Hash. This call will block if the data at the
// specified offset hasn't been fetched yet.
func (pd *ProgramData) Hash(offset uint64) (crypto.Hash, error) {
	d, err := pd.managedBytes(offset, crypto.HashSize)
	if err != nil {
		return crypto.Hash{}, err
	}
	var h crypto.Hash
	copy(h[:], d)
	return h, nil
}

// Len returns the length of the program data.
func (pd *ProgramData) Len() uint64 {
	return pd.staticLength
}

// Stop will stop the background thread and wait for it to return.
func (pd *ProgramData) Stop() {
	pd.mu.Lock()
	defer pd.mu.Unlock()
	close(pd.cancel)
	pd.wg.Wait()
}
