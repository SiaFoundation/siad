// Package encoding converts arbitrary objects into byte slices, and vis
// versa. It also contains helper functions for reading and writing length-
// prefixed data. See doc/Encoding.md for the full encoding specification.
package encoding

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
)

const (
	// DefaultAllocLimit is a reasonable number of bytes that may be allocated
	// when decoding an unspecified object.
	DefaultAllocLimit = 1e6
)

// ErrAllocLimitExceeded is the error returned when an encoded object exceeds
// the specified allocation limit.
type ErrAllocLimitExceeded int

// Error implements the error interface.
func (e ErrAllocLimitExceeded) Error() string {
	return fmt.Sprintf("encoded object exceeded allocation limit by %v bytes", int(e))
}

var (
	errBadPointer = errors.New("cannot decode into invalid pointer")
)

type (
	// A SiaMarshaler can encode and write itself to a stream.
	SiaMarshaler interface {
		MarshalSia(io.Writer) error
	}

	// A SiaUnmarshaler can read and decode itself from a stream.
	SiaUnmarshaler interface {
		UnmarshalSia(io.Reader) error
	}
)

// An Encoder writes objects to an output stream. It also provides helper
// methods for writing custom SiaMarshaler implementations. All of its methods
// become no-ops after the Encoder encounters a Write error.
type Encoder struct {
	w   io.Writer
	buf [8]byte
	err error
}

// Write implements the io.Writer interface.
func (e *Encoder) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	var n int
	n, e.err = e.w.Write(p)
	if n != len(p) && e.err == nil {
		e.err = io.ErrShortWrite
	}
	return n, e.err
}

// WriteByte implements the io.ByteWriter interface.
func (e *Encoder) WriteByte(b byte) error {
	if e.err != nil {
		return e.err
	}
	e.buf[0] = b
	e.Write(e.buf[:1])
	return e.err
}

// WriteBool writes b to the underlying io.Writer.
func (e *Encoder) WriteBool(b bool) error {
	if b {
		return e.WriteByte(1)
	}
	return e.WriteByte(0)
}

// WriteUint64 writes a uint64 value to the underlying io.Writer.
func (e *Encoder) WriteUint64(u uint64) error {
	if e.err != nil {
		return e.err
	}
	binary.LittleEndian.PutUint64(e.buf[:8], u)
	e.Write(e.buf[:8])
	return e.err
}

// WriteInt writes an int value to the underlying io.Writer.
func (e *Encoder) WriteInt(i int) error {
	return e.WriteUint64(uint64(i))
}

// WritePrefixedBytes writes p to the underlying io.Writer, prefixed by its length.
func (e *Encoder) WritePrefixedBytes(p []byte) error {
	e.WriteInt(len(p))
	e.Write(p)
	return e.err
}

// Err returns the first non-nil error encountered by e.
func (e *Encoder) Err() error {
	return e.err
}

// Encode writes the encoding of v to the stream. For encoding details, see
// the package docstring.
func (e *Encoder) Encode(v interface{}) error {
	return e.encode(reflect.ValueOf(v))
}

// EncodeAll encodes a variable number of arguments.
func (e *Encoder) EncodeAll(vs ...interface{}) error {
	for _, v := range vs {
		if err := e.Encode(v); err != nil {
			return err
		}
	}
	return nil
}

// Encode writes the encoding of val to the stream. For encoding details, see
// the package docstring.
func (e *Encoder) encode(val reflect.Value) error {
	if e.err != nil {
		return e.err
	}
	// check for MarshalSia interface first
	if val.CanInterface() {
		if m, ok := val.Interface().(SiaMarshaler); ok {
			return m.MarshalSia(e)
		}
	}

	switch val.Kind() {
	case reflect.Ptr:
		// write either a 1 or 0
		if err := e.Encode(!val.IsNil()); err != nil {
			return err
		}
		if val.IsNil() {
			return nil // nothing to encode
		}
		// write dereferenced value
		return e.encode(val.Elem())
	case reflect.Bool:
		return e.WriteBool(val.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return e.WriteUint64(uint64(val.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return e.WriteUint64(val.Uint())
	case reflect.String:
		return e.WritePrefixedBytes([]byte(val.String()))
	case reflect.Slice:
		// slices are variable length, so prepend the length and then fallthrough to array logic
		if err := e.WriteInt(val.Len()); err != nil {
			return err
		}
		if val.Len() == 0 {
			return nil
		}
		fallthrough
	case reflect.Array:
		// special case for byte arrays
		if val.Type().Elem().Kind() == reflect.Uint8 {
			// if the array is addressable, we can optimize a bit here
			if val.CanAddr() {
				_, err := e.Write(val.Slice(0, val.Len()).Bytes())
				return err
			}
			// otherwise we have to copy into a newly allocated slice
			slice := reflect.MakeSlice(reflect.SliceOf(val.Type().Elem()), val.Len(), val.Len())
			reflect.Copy(slice, val)
			_, err := e.Write(slice.Bytes())
			return err
		}
		// normal slices/arrays are encoded by sequentially encoding their elements
		for i := 0; i < val.Len(); i++ {
			if err := e.encode(val.Index(i)); err != nil {
				return err
			}
		}
		return nil
	case reflect.Struct:
		for i := 0; i < val.NumField(); i++ {
			if err := e.encode(val.Field(i)); err != nil {
				return err
			}
		}
		return nil
	}

	// Marshalling should never fail. If it panics, you're doing something wrong,
	// like trying to encode a map or an unexported struct field.
	panic("could not marshal type " + val.Type().String())
}

// NewEncoder converts w to an Encoder.
func NewEncoder(w io.Writer) *Encoder {
	if e, ok := w.(*Encoder); ok {
		return e
	}
	return &Encoder{w: w}
}

// Marshal returns the encoding of v. For encoding details, see the package
// docstring.
func Marshal(v interface{}) []byte {
	b := new(bytes.Buffer)
	NewEncoder(b).Encode(v) // no error possible when using a bytes.Buffer
	return b.Bytes()
}

// MarshalAll encodes all of its inputs and returns their concatenation.
func MarshalAll(vs ...interface{}) []byte {
	b := new(bytes.Buffer)
	enc := NewEncoder(b)
	// Error from EncodeAll is ignored as encoding cannot fail when writing
	// to a bytes.Buffer.
	_ = enc.EncodeAll(vs...)
	return b.Bytes()
}

// WriteFile writes v to a file. The file will be created if it does not exist.
func WriteFile(filename string, v interface{}) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	err = NewEncoder(file).Encode(v)
	if err != nil {
		return errors.New("error while writing " + filename + ": " + err.Error())
	}
	return nil
}

// A Decoder reads and decodes values from an input stream. It also provides
// helper methods for writing custom SiaUnmarshaler implementations. These
// methods do not return errors, but instead set the value of d.Err(). Once
// d.Err() is set, future operations become no-ops.
type Decoder struct {
	r        io.Reader
	buf      [8]byte
	err      error
	canAlloc uint64 // number of bytes that may be allocated
}

// Read implements the io.Reader interface.
func (d *Decoder) Read(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	var n int
	n, d.err = d.r.Read(p)
	return n, d.err
}

// ReadFull is shorthand for io.ReadFull(d, p).
func (d *Decoder) ReadFull(p []byte) {
	if d.err != nil {
		return
	}
	_, err := io.ReadFull(d.r, p)
	if err != nil {
		d.err = err
	}
}

// ReadPrefixedBytes reads a length-prefix, allocates a byte slice with that
// length, reads into the byte slice, and returns it. If the byte slice would
// exceed the allocation limit, ReadPrefixedBytes returns nil and sets d.Err().
func (d *Decoder) ReadPrefixedBytes() []byte {
	n := d.NextPrefix(1) // if too large, n == 0
	b := make([]byte, n)
	d.ReadFull(b)
	if d.err != nil {
		return nil
	}
	return b
}

// NextUint64 reads the next 8 bytes and returns them as a uint64.
func (d *Decoder) NextUint64() uint64 {
	d.ReadFull(d.buf[:8])
	if d.err != nil {
		return 0
	}
	return DecUint64(d.buf[:])
}

// NextBool reads the next byte and returns it as a bool.
func (d *Decoder) NextBool() bool {
	d.ReadFull(d.buf[:1])
	if d.buf[0] > 1 && d.err == nil {
		d.err = errors.New("boolean value was not 0 or 1")
	}
	return d.buf[0] == 1
}

// NextPrefix is like NextUint64, but performs sanity checks on the prefix.
// Specifically, if the prefix multiplied by elemSize exceeds MaxSliceSize,
// NextPrefix returns 0 and sets d.Err().
func (d *Decoder) NextPrefix(elemSize uintptr) uint64 {
	n := d.NextUint64()
	allocSize := n * uint64(elemSize)
	if allocSize > d.canAlloc {
		d.err = ErrAllocLimitExceeded(allocSize - d.canAlloc)
		return 0
	}
	d.canAlloc -= allocSize
	return n
}

// Err returns the first non-nil error encountered by d.
func (d *Decoder) Err() error {
	return d.err
}

// Decode reads the next encoded value from its input stream and stores it in
// v, which must be a pointer. The decoding rules are the inverse of those
// specified in the package docstring.
func (d *Decoder) Decode(v interface{}) (err error) {
	// v must be a pointer
	pval := reflect.ValueOf(v)
	if pval.Kind() != reflect.Ptr || pval.IsNil() {
		return errBadPointer
	}

	// catch decoding panics and convert them to errors
	// note that this allows us to skip boundary checks during decoding
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("could not decode type %s: %v", pval.Elem().Type().String(), r)
		}
	}()

	// decode the value
	d.decode(pval.Elem())
	return
}

// DecodeAll decodes a variable number of arguments.
func (d *Decoder) DecodeAll(vs ...interface{}) error {
	for _, v := range vs {
		if err := d.Decode(v); err != nil {
			return err
		}
	}
	return nil
}

// decode reads the next encoded value from its input stream and stores it in
// val. The decoding rules are the inverse of those specified in the package
// docstring.
func (d *Decoder) decode(val reflect.Value) {
	// check for UnmarshalSia interface first
	if val.CanAddr() && val.Addr().CanInterface() {
		if u, ok := val.Addr().Interface().(SiaUnmarshaler); ok {
			err := u.UnmarshalSia(d)
			if err != nil {
				panic(err)
			}
			return
		}
	}

	switch val.Kind() {
	case reflect.Ptr:
		valid := d.NextBool()
		if !valid {
			// nil pointer, nothing to decode
			break
		}
		// make sure we aren't decoding into nil
		if val.IsNil() {
			// account for allocation
			allocSize := uint64(val.Type().Elem().Size())
			if allocSize > d.canAlloc {
				d.err = ErrAllocLimitExceeded(allocSize - d.canAlloc)
				return
			}
			d.canAlloc -= allocSize
			val.Set(reflect.New(val.Type().Elem()))
		}
		d.decode(val.Elem())
	case reflect.Bool:
		val.SetBool(d.NextBool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		val.SetInt(int64(d.NextUint64()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		val.SetUint(d.NextUint64())
	case reflect.String:
		val.SetString(string(d.ReadPrefixedBytes()))
	case reflect.Slice:
		// slices are variable length, but otherwise the same as arrays.
		// just have to allocate them first, then we can fallthrough to the array logic.
		sliceLen := d.NextPrefix(val.Type().Elem().Size())
		if sliceLen == 0 {
			break
		}
		val.Set(reflect.MakeSlice(val.Type(), int(sliceLen), int(sliceLen)))
		fallthrough
	case reflect.Array:
		// special case for byte arrays (e.g. hashes)
		if val.Type().Elem().Kind() == reflect.Uint8 {
			// convert val to a slice and read into it directly
			d.ReadFull(val.Slice(0, val.Len()).Bytes())
			break
		}
		// arrays are unmarshalled by sequentially unmarshalling their elements
		for i := 0; i < val.Len(); i++ {
			d.decode(val.Index(i))
		}
	case reflect.Struct:
		for i := 0; i < val.NumField(); i++ {
			d.decode(val.Field(i))
		}
	default:
		panic("unknown type")
	}

	if d.err != nil {
		panic(d.err)
	}
}

// NewDecoder converts r to a Decoder. maxAlloc is the maximum number of bytes
// that may be allocated by the decoder.
func NewDecoder(r io.Reader, maxAlloc int) *Decoder {
	if d, ok := r.(*Decoder); ok {
		return d
	}
	return &Decoder{r: r, canAlloc: uint64(maxAlloc)}
}

// Unmarshal decodes the encoded value b and stores it in v, which must be a
// pointer. The decoding rules are the inverse of those specified in the
// package docstring for marshaling.
func Unmarshal(b []byte, v interface{}) error {
	return NewDecoder(bytes.NewBuffer(b), len(b)*3).Decode(v)
}

// UnmarshalAll decodes the encoded values in b and stores them in vs, which
// must be pointers.
func UnmarshalAll(b []byte, vs ...interface{}) error {
	return NewDecoder(bytes.NewBuffer(b), len(b)*3).DecodeAll(vs...)
}

// ReadFile reads the contents of a file and decodes them into v.
func ReadFile(filename string, v interface{}) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	err = NewDecoder(file, int(stat.Size()*3)).Decode(v)
	if err != nil {
		return errors.New("error while reading " + filename + ": " + err.Error())
	}
	return nil
}
