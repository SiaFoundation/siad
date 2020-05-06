package encoding

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// ReadPrefixedBytes reads an 8-byte length prefixes, followed by the number of bytes
// specified in the prefix. The operation is aborted if the prefix exceeds a
// specified maximum length.
func ReadPrefixedBytes(r io.Reader, maxLen uint64) ([]byte, error) {
	prefix := make([]byte, 8)
	if _, err := io.ReadFull(r, prefix); err != nil {
		return nil, err
	}
	dataLen := DecUint64(prefix)
	if dataLen > maxLen {
		return nil, fmt.Errorf("length %d exceeds maxLen of %d", dataLen, maxLen)
	}
	// read dataLen bytes
	data := make([]byte, dataLen)
	_, err := io.ReadFull(r, data)
	return data, err
}

// ReadObject reads and decodes a length-prefixed and marshalled object.
func ReadObject(r io.Reader, obj interface{}, maxLen uint64) error {
	data, err := ReadPrefixedBytes(r, maxLen)
	if err != nil {
		return err
	}
	return Unmarshal(data, obj)
}

// WritePrefixedBytes writes a length-prefixed byte slice to w.
func WritePrefixedBytes(w io.Writer, data []byte) error {
	err := WriteInt(w, len(data))
	if err != nil {
		return err
	}
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}

// WriteObject writes a length-prefixed object to w.
func WriteObject(w io.Writer, v interface{}) error {
	var buf bytes.Buffer
	if s, ok := v.(interface{ MarshalSiaSize() int }); ok {
		buf.Grow(8 + s.MarshalSiaSize())
	}
	enc := NewEncoder(&buf)
	enc.WriteUint64(0) // placeholder
	enc.Encode(v)
	b := buf.Bytes()
	binary.LittleEndian.PutUint64(b[:8], uint64(len(b)-8))
	n, err := w.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return err
}
