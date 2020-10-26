package modules

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"os"
	"reflect"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestSkyfileReader verifies the functionality of the SkyfileReader.
func TestSkyfileReader(t *testing.T) {
	t.Run("Basic", testSkyfileReaderBasic)
	t.Run("ReadBuffer", testSkyfileReaderReadBuffer)
	t.Run("MetadataTimeout", testSkyfileReaderMetadataTimeout)
}

// testSkyfileReaderBasic verifies the basic use case of the SkyfileReader
func testSkyfileReaderBasic(t *testing.T) {
	t.Parallel()

	// create upload parameters
	sup := SkyfileUploadParameters{
		Filename: t.Name(),
		Mode:     os.FileMode(644),
	}

	// create a reader
	dataLen := fastrand.Intn(1000) + 10
	data := fastrand.Bytes(dataLen)
	reader := bytes.NewReader(data)
	sfReader := NewSkyfileReader(reader, sup)

	// read 1 byte
	peek := make([]byte, 1)
	n, err := sfReader.Read(peek)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || !bytes.Equal(peek, data[:1]) {
		t.Fatal("unexpected read")
	}

	// read another 9 bytes
	next := make([]byte, 9)
	n, err = sfReader.Read(next)
	if err != nil {
		t.Fatal(err)
	}
	if n != 9 || !bytes.Equal(next, data[1:10]) {
		t.Fatal("unexpected read")
	}

	// read the remaining bytes
	remainingLen := dataLen - 10
	next = make([]byte, remainingLen)
	n, err = sfReader.Read(next)
	if err != nil {
		t.Fatal(err)
	}
	if n != remainingLen || !bytes.Equal(next, data[10:]) {
		t.Fatal("unexpected read")
	}

	// read again, expect EOF
	n, err = sfReader.Read(next)
	if err != io.EOF {
		t.Fatal(err, n)
	}

	// fetch the metadata from the reader
	metadata, err := sfReader.SkyfileMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// check against what we expect it to be
	if !reflect.DeepEqual(metadata, SkyfileMetadata{
		Filename: sup.Filename,
		Mode:     sup.Mode,
		Length:   uint64(dataLen),
	}) {
		t.Fatal("unexpected metadata", metadata)
	}
}

// testSkyfileReaderReadBuffer verifies the functionality of the read buffer.
func testSkyfileReaderReadBuffer(t *testing.T) {
	t.Parallel()

	// create upload parameters
	sup := SkyfileUploadParameters{
		Filename: t.Name(),
		Mode:     os.FileMode(644),
	}

	// create a reader
	size := 100
	data := fastrand.Bytes(size)
	reader := bytes.NewReader(data)
	sfReader := NewSkyfileReader(reader, sup)

	// read some data
	buf := make([]byte, 40)
	n, err := io.ReadFull(sfReader, buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 40 {
		t.Fatal("unexpected read")
	}

	// set that data is read buffer
	sfReader.AddReadBuffer(buf)

	// read the rest of the data
	rest := make([]byte, 100)
	_, err = sfReader.Read(rest)
	if err != nil {
		t.Fatal(err)
	}

	// read again, expect EOF
	_, err = sfReader.Read(make([]byte, 1))
	if err != io.EOF {
		t.Fatal(err, n)
	}

	// verify we have just read all data, including the buffer
	if !bytes.Equal(rest, data) {
		t.Fatal("unexpected read")
	}

	// fetch the metadata from the reader
	metadata, err := sfReader.SkyfileMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// check against what we expect it to be
	if !reflect.DeepEqual(metadata, SkyfileMetadata{
		Filename: sup.Filename,
		Mode:     sup.Mode,
		Length:   uint64(size),
	}) {
		t.Fatal("unexpected metadata", metadata)
	}
}

// testSkyfileReaderMetadataTimeout verifies metadata returns on timeout,
// potentially before the reader is fully read
func testSkyfileReaderMetadataTimeout(t *testing.T) {
	t.Parallel()

	// create upload parameters
	sup := SkyfileUploadParameters{
		Filename: t.Name(),
		Mode:     os.FileMode(644),
	}

	// create a reader
	dataLen := fastrand.Intn(1000) + 10
	data := fastrand.Bytes(dataLen)
	reader := bytes.NewReader(data)
	sfReader := NewSkyfileReader(reader, sup)

	// read less than dataLen
	read := make([]byte, dataLen/2)
	_, err := sfReader.Read(read)
	if err != nil {
		t.Fatal(err)
	}

	// cancel the context in a goroutine
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(time.Second)
		cancel()
	}()

	// fetch the metadata from the reader
	metadata, err := sfReader.SkyfileMetadata(ctx)
	if !errors.Contains(err, ErrSkyfileMetadataUnavailable) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(metadata, SkyfileMetadata{}) {
		t.Fatal("unexpected metadata", metadata)
	}
}

// TestSkyfileMultipartReader verifies the functionality of the
// SkyfileMultipartReader.
func TestSkyfileMultipartReader(t *testing.T) {
	t.Run("Basic", testSkyfileMultipartReaderBasic)
	t.Run("IllegalFormName", testSkyfileMultipartReaderIllegalFormName)
	t.Run("EmptyFilename", testSkyfileMultipartReaderEmptyFilename)
	t.Run("RandomReadSize", testSkyfileMultipartReaderRandomReadSize)
	t.Run("ReadBuffer", testSkyfileMultipartReaderReadBuffer)
	t.Run("MetadataTimeout", testSkyfileMultipartReaderMetadataTimeout)
}

// testSkyfileMultipartReaderBasic verifies the basic use case of a skyfile
// multipart reader, reading out the exact parts.
func testSkyfileMultipartReaderBasic(t *testing.T) {
	t.Parallel()

	// create upload parameters
	sup := SkyfileUploadParameters{
		Filename: t.Name(),
		Mode:     os.FileMode(644),
	}

	// create a multipart writer
	buffer := new(bytes.Buffer)
	writer := multipart.NewWriter(buffer)

	// prepare random file data
	data1 := fastrand.Bytes(10)
	data2 := fastrand.Bytes(20)

	// write the multipart files
	off := uint64(0)
	md1, err1 := AddMultipartFile(writer, data1, "files[]", "part1", 0600, &off)
	md2, err2 := AddMultipartFile(writer, data2, "files[]", "part2", 0600, &off)
	if errors.Compose(err1, err2) != nil {
		t.Fatal("unexpected")
	}

	// close the writer
	err := writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	// turn it into a skyfile reader
	reader := bytes.NewReader(buffer.Bytes())
	multipartReader := multipart.NewReader(reader, writer.Boundary())
	sfReader := NewSkyfileMultipartReader(multipartReader, sup)

	// verify we can read part 1
	part1Data := make([]byte, 10)
	n, err := sfReader.Read(part1Data)
	if err != nil {
		t.Fatal(err)
	}
	if n != 10 || !bytes.Equal(part1Data, data1) {
		t.Fatal("unexpected read", n)
	}

	// verify we can read part 2
	part2Data := make([]byte, 20)
	n, err = sfReader.Read(part2Data)
	if err != nil {
		t.Fatal(err)
	}
	if n != 20 || !bytes.Equal(part2Data, data2) {
		t.Fatal("unexpected read", n)
	}

	// we have to simulate a consecutive read to mimic not knowing how many
	// parts the request contains, only then will the metadata be released
	sfReader.Read(make([]byte, 1))

	// fetch the metadata from the reader
	metadata, err := sfReader.SkyfileMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	part1Meta, ok := metadata.Subfiles["part1"]
	if !ok || !reflect.DeepEqual(part1Meta, md1) {
		t.Fatal("unexpected metadata")
	}

	part2Meta, ok := metadata.Subfiles["part2"]
	if !ok || !reflect.DeepEqual(part2Meta, md2) {
		t.Fatal("unexpected metadata")
	}
}

// testSkyfileMultipartReaderIllegalFormName verifies the reader returns an
// error if the given form name is not one of the allowed values.
func testSkyfileMultipartReaderIllegalFormName(t *testing.T) {
	t.Parallel()

	// create upload parameters
	sup := SkyfileUploadParameters{
		Filename: t.Name(),
		Mode:     os.FileMode(644),
	}

	// create a multipart writer
	buffer := new(bytes.Buffer)
	writer := multipart.NewWriter(buffer)

	// prepare random file data
	data := fastrand.Bytes(10)

	// write the multipart files
	off := uint64(0)
	_, err := AddMultipartFile(writer, data, "part", "part", 0600, &off)
	if err != nil {
		t.Fatal("unexpected")
	}

	// close the writer
	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	// turn it into a skyfile reader
	reader := bytes.NewReader(buffer.Bytes())
	multipartReader := multipart.NewReader(reader, writer.Boundary())
	sfReader := NewSkyfileMultipartReader(multipartReader, sup)

	// verify we
	_, err = ioutil.ReadAll(sfReader)
	if !errors.Contains(err, ErrIllegalFormName) {
		t.Fatalf("expected ErrIllegalFormName error, instead err was '%v'", err)
	}
}

// testSkyfileMultipartReaderRandomReadSize creates a random multipart request
// and reads the entire request using random read sizes.
func testSkyfileMultipartReaderRandomReadSize(t *testing.T) {
	t.Parallel()

	// create upload parameters
	sup := SkyfileUploadParameters{
		Filename: t.Name(),
		Mode:     os.FileMode(644),
	}

	// create a multipart writer
	buffer := new(bytes.Buffer)
	writer := multipart.NewWriter(buffer)

	// prepare some random data
	randomParts := 3 //fastrand.Intn(10) + 3
	randomPartsData := make([][]byte, randomParts)
	for i := 0; i < randomParts; i++ {
		randomPartLen := fastrand.Intn(10) + 1
		randomPartsData[i] = fastrand.Bytes(randomPartLen)
	}

	// write the multipart files
	off := uint64(0)
	for i, data := range randomPartsData {
		filename := fmt.Sprintf("file%d", i)
		_, err := AddMultipartFile(writer, data, "files[]", filename, 644, &off)
		if err != nil {
			t.Fatal(err)
		}
	}

	// close the writer
	err := writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	// turn it into a skyfile reader
	reader := bytes.NewReader(buffer.Bytes())
	multipartReader := multipart.NewReader(reader, writer.Boundary())
	sfReader := NewSkyfileMultipartReader(multipartReader, sup)

	// concat all data
	expected := make([]byte, 0)
	for _, data := range randomPartsData {
		expected = append(expected, data...)
	}

	// read, randomly, until all data is read
	actual := make([]byte, 0)
	maxLen := len(expected) / 3
	for {
		randomLength := fastrand.Intn(maxLen) + 1

		data := make([]byte, randomLength)
		n, err := sfReader.Read(data)

		actual = append(actual, data[:n]...)
		if errors.Contains(err, io.EOF) {
			break
		}
	}

	// verify we've read all of the data
	if !bytes.Equal(actual, expected) {
		t.Fatal("unexpected data")
	}

	// fetch the metadata from the reader
	metadata, err := sfReader.SkyfileMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// verify the metadata is properly read
	if len(metadata.Subfiles) != randomParts {
		t.Fatal("unexpected amount of metadata")
	}

	currOffset := 0
	for i, data := range randomPartsData {
		filename := fmt.Sprintf("file%d", i)
		metadata, ok := metadata.Subfiles[filename]
		if !ok {
			t.Fatal("metadata not found")
		}
		if metadata.Len != uint64(len(data)) {
			t.Fatal("unexpected len")
		}
		if metadata.Offset != uint64(currOffset) {
			t.Fatal("unexpected offset")
		}
		currOffset += len(data)
	}
}

// testSkyfileMultipartReaderEmptyFilename verifies the reader returns an error
// if the filename is empty.
func testSkyfileMultipartReaderEmptyFilename(t *testing.T) {
	t.Parallel()

	// create upload parameters
	sup := SkyfileUploadParameters{
		Filename: t.Name(),
		Mode:     os.FileMode(644),
	}

	// create a multipart writer
	buffer := new(bytes.Buffer)
	writer := multipart.NewWriter(buffer)

	// prepare random file data
	data := fastrand.Bytes(10)

	// write the multipart files
	off := uint64(0)
	_, err := AddMultipartFile(writer, data, "file", "", 0600, &off)
	if err != nil {
		t.Fatal("unexpected")
	}

	// close the writer
	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	// turn it into a skyfile reader
	reader := bytes.NewReader(buffer.Bytes())
	multipartReader := multipart.NewReader(reader, writer.Boundary())
	sfReader := NewSkyfileMultipartReader(multipartReader, sup)

	// verify we get ErrEmptyFilename if we do not provide a filename
	_, err = ioutil.ReadAll(sfReader)
	if !errors.Contains(err, ErrEmptyFilename) {
		t.Fatalf("expected ErrEmptyFilename error, instead err was '%v'", err)
	}
}

// testSkyfileMultipartReaderReadBuffer verifies the functionality of the read
// buffer.
func testSkyfileMultipartReaderReadBuffer(t *testing.T) {
	t.Parallel()

	// create upload parameters
	sup := SkyfileUploadParameters{
		Filename: t.Name(),
		Mode:     os.FileMode(644),
	}

	// create a multipart writer
	buffer := new(bytes.Buffer)
	writer := multipart.NewWriter(buffer)

	// prepare random file data
	data1 := fastrand.Bytes(10)
	data2 := fastrand.Bytes(20)

	// write the multipart files
	off := uint64(0)
	_, err1 := AddMultipartFile(writer, data1, "files[]", "part1", 0600, &off)
	_, err2 := AddMultipartFile(writer, data2, "files[]", "part2", 0600, &off)
	if errors.Compose(err1, err2) != nil {
		t.Fatal("unexpected")
	}

	// close the writer
	err := writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	// turn it into a skyfile reader
	reader := bytes.NewReader(buffer.Bytes())
	multipartReader := multipart.NewReader(reader, writer.Boundary())
	sfReader := NewSkyfileMultipartReader(multipartReader, sup)

	// read 5 bytes
	data := make([]byte, 5)
	_, err = sfReader.Read(data)
	if err != nil {
		t.Fatal(err)
	}

	// set them as buffer
	sfReader.AddReadBuffer(data)

	// read 20 bytes and compare them to what we expect to receive
	expected := append(data1, data2[:10]...)
	data = make([]byte, 20)
	_, err = sfReader.Read(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expected, data) {
		t.Fatal("unexpected")
	}
}

// testSkyfileMultipartReaderMetadataTimeout verifies metadata returns on
// timeout, potentially before the reader is fully read
func testSkyfileMultipartReaderMetadataTimeout(t *testing.T) {
	t.Parallel()

	// create upload parameters
	sup := SkyfileUploadParameters{
		Filename: t.Name(),
		Mode:     os.FileMode(644),
	}

	// create a multipart writer
	buffer := new(bytes.Buffer)
	writer := multipart.NewWriter(buffer)

	// prepare random file data
	dataLen := fastrand.Intn(100) + 10
	data := fastrand.Bytes(dataLen)

	// write the multipart files
	off := uint64(0)
	_, err := AddMultipartFile(writer, data, "files[]", "part", 0600, &off)
	if err != nil {
		t.Fatal("unexpected")
	}

	// close the writer
	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	// turn it into a skyfile reader
	reader := bytes.NewReader(buffer.Bytes())
	multipartReader := multipart.NewReader(reader, writer.Boundary())
	sfReader := NewSkyfileMultipartReader(multipartReader, sup)

	// read less than dataLen
	read := make([]byte, dataLen/2)
	_, err = sfReader.Read(read)
	if err != nil {
		t.Fatal(err)
	}

	// cancel the context in a goroutine
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(time.Second)
		cancel()
	}()

	// fetch the metadata from the reader
	metadata, err := sfReader.SkyfileMetadata(ctx)
	if !errors.Contains(err, ErrSkyfileMetadataUnavailable) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(metadata, SkyfileMetadata{}) {
		t.Fatal("unexpected metadata", metadata)
	}
}
