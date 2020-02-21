package renter

import (
	"io"

	"gitlab.com/NebulousLabs/errors"
)

type SectionReadSeeker struct {
	stream *stream
	base   uint64
	off    uint64
	limit  uint64
}

func NewSectionReadSeeker(s *stream, off, n uint64) *SectionReadSeeker {
	return &SectionReadSeeker{
		stream: s,
		base:   off,
		off:    off,
		limit:  off + n,
	}
}

func (srs *SectionReadSeeker) Read(p []byte) (n int, err error) {
	if srs.off >= srs.limit {
		return 0, io.EOF
	}
	if max := srs.limit - srs.off; uint64(len(p)) > max {
		p = p[0:max]
	}

	n, err = srs.stream.Read(p)
	srs.off += uint64(n)
	return
}

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
	if uint64(offset) < srs.base {
		return 0, errors.New("invalid offset")
	}

	srs.off = uint64(offset)
	_, err := srs.stream.Seek(int64(srs.off), io.SeekStart)
	if err != nil {
		return offset - int64(srs.base), err
	}

	return offset - int64(srs.base), nil
}

func (srs *SectionReadSeeker) Close() error {
	return srs.stream.Close()
}
