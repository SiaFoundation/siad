package modules

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"os"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestSkyfileReader verifies the functionality of the SkyfileReader.
func TestSkyfileReader(t *testing.T) {
	dataLen := fastrand.Intn(1000) + 10
	data := fastrand.Bytes(dataLen)
	reader := bytes.NewReader(data)

	metadata := SkyfileMetadata{
		Mode:     os.FileMode(644),
		Filename: t.Name(),
		Length:   uint64(dataLen),
	}

	sfReader := NewSkyfileReader(reader, metadata)

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
		t.Fatal(err)
	}

	if !reflect.DeepEqual(metadata, sfReader.SkyfileMetadata()) {
		t.Fatal("unexpected metadata")
	}
}

// TestSkyfileMultipartReader verifies the functionality of the
// SkyfileMultipartReader.
func TestSkyfileMultipartReader(t *testing.T) {
	t.Run("Basic", testSkyfileMultipartReaderBasic)
	t.Run("IllegalFormName", testSkyfileMultipartReaderIllegalFormName)
	t.Run("RandomReadSize", testSkyfileMultipartReaderRandomReadSize)
}

// testSkyfileMultipartReaderBasic verifies the basic use case of a skyfile
// multipart reader, reading out the exact parts.
func testSkyfileMultipartReaderBasic(t *testing.T) {
	t.Parallel()

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
	sfReader := NewSkyfileMultipartReader(multipartReader)

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

	part1Meta, ok := sfReader.SkyfileMetadata().Subfiles["part1"]
	if !ok || !reflect.DeepEqual(part1Meta, md1) {
		t.Fatal("unexpected metadata")
	}

	part2Meta, ok := sfReader.SkyfileMetadata().Subfiles["part2"]
	if !ok || !reflect.DeepEqual(part2Meta, md2) {
		t.Fatal("unexpected metadata")
	}
}

// testSkyfileMultipartReaderIllegalFormName verifies the reader returns an
// error if the given form name is not one of the allowed values.
func testSkyfileMultipartReaderIllegalFormName(t *testing.T) {
	t.Parallel()

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
	sfReader := NewSkyfileMultipartReader(multipartReader)

	// verify we
	_, err = ioutil.ReadAll(sfReader)
	if !errors.Contains(err, ErrIllegalFormName) {
		t.Fatalf("expected ErrIllegalFormName error, instead err was '%v'", err)
	}
}

// testSkyfileMultipartReaderRandomReadSize creates a random multipart request
// and reads the entire request using random read sizes.
func testSkyfileMultipartReaderRandomReadSize(t *testing.T) {
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
	sfReader := NewSkyfileMultipartReader(multipartReader)

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

	// verify the metadata is properly read
	metadata := sfReader.SkyfileMetadata()
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
