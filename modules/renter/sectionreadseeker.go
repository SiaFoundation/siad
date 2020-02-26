package renter

import (
	"io"

	"gitlab.com/NebulousLabs/errors"
)

// ReadSeekerCloser is an object that implements both the io.ReadSeeker and
// io.Closer interfaces
type ReadSeekerCloser interface {
	io.ReadSeeker
	io.Closer
}

// SectionReadSeeker is based on the io.SectionReader with the addition of the
// Seeker interface. It can be used
type SectionReadSeeker struct {
	rsc   ReadSeekerCloser
	base  uint64
	off   uint64
	limit uint64
}

// NewSectionReadSeeker returns a new SectionReadSeeker from given arguments. It
// allows reading and seeking 'n' bytes from the underlying data start at the
// given offset.
func NewSectionReadSeeker(rsc ReadSeekerCloser, off, n uint64) *SectionReadSeeker {
	return &SectionReadSeeker{
		rsc:   rsc,
		base:  off,
		off:   off,
		limit: off + n,
	}
}

// Read implements the io.Reader interface
func (srs *SectionReadSeeker) Read(p []byte) (n int, err error) {
	if srs.off >= srs.limit {
		return 0, io.EOF
	}
	if max := srs.limit - srs.off; uint64(len(p)) > max {
		p = p[0:max]
	}

	n, err = srs.rsc.Read(p)
	srs.off += uint64(n)
	return
}

// Seek implements the io.Seeker interface
func (srs *SectionReadSeeker) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		offset += int64(srs.base)
	case io.SeekCurrent:
		offset += int64(srs.off)
	case io.SeekEnd:
		offset += int64(srs.limit)
	default:
		return 0, errors.New("invalid value for 'whence' in call to seek")
	}

	// Note that we allow an offset greater than the limit, however on the next
	// read we return an EOF.
	if uint64(offset) < srs.base {
		return 0, errors.New("invalid offset")
	}

	srs.off = uint64(offset)
	_, err := srs.rsc.Seek(int64(srs.off), io.SeekStart)
	if err != nil {
		return offset - int64(srs.base), err
	}

	return offset - int64(srs.base), nil
}

// Close implements the io.Closer interface
func (srs *SectionReadSeeker) Close() error {
	return srs.rsc.Close()
}
