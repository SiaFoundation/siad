package api

import (
	"io"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// limitStreamer is a helper struct that wraps a modules.Streamer so it starts
// at a certain offset, and can only be read from until a certain limit. It
// wraps both Read and Seek calls and handles the offset and returned bytes
// appropriately.
type limitStreamer struct {
	stream modules.Streamer
	base   uint64
	off    uint64
	limit  uint64
}

// NewLimitStreamer wraps the given modules.Streamer and ensures it can only be read from within the given offset and size boundary. It does this by wrapping both the Read and Seek calls and adjusting the offset and size of the returned byte slice appropriately.
func NewLimitStreamer(s modules.Streamer, off, size uint64) modules.Streamer {
	return &limitStreamer{
		stream: s,
		base:   off,
		off:    off,
		limit:  off + size,
	}
}

// Read implements the io.Reader interface
func (ls *limitStreamer) Read(p []byte) (n int, err error) {
	if ls.off >= ls.limit {
		return 0, io.EOF
	}
	if max := ls.limit - ls.off; uint64(len(p)) > max {
		p = p[0:max]
	}

	n, err = ls.stream.Read(p)
	ls.off += uint64(n)
	return
}

// Seek implements the io.Seeker interface
func (ls *limitStreamer) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		offset += int64(ls.base)
	case io.SeekCurrent:
		offset += int64(ls.off)
	case io.SeekEnd:
		offset += int64(ls.limit)
	default:
		return 0, errors.New("invalid value for 'whence' in call to seek")
	}

	if uint64(offset) < ls.base {
		return 0, errors.New("invalid offset")
	}

	ls.off = uint64(offset)
	_, err := ls.stream.Seek(int64(ls.off), io.SeekStart)
	if err != nil {
		return offset - int64(ls.base), err
	}

	return offset - int64(ls.base), nil
}

// Close implements the io.Closer interface
func (ls *limitStreamer) Close() error {
	return ls.stream.Close()
}
