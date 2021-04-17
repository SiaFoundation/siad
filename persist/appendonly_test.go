package persist

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/types"
)

var (
	// testHeader is the header specifier for testing.
	testHeader = types.NewSpecifier("TestHeader\n")
	// testVersion is the version specifier for testing
	testVersion = types.NewSpecifier("TestVersion\n")
)

// TestWrite tests that written data is appended and persistent.
func TestWrite(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a new AppendOnlyPersist.
	testdir := build.TempDir("appendonlypersist", t.Name())
	filename := "test"
	aop, reader, err := NewAppendOnlyPersist(testdir, filename, testHeader, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	readBytes, err := ioutil.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	// Test FilePath().
	filepath := filepath.Join(testdir, filename)
	if filepath != aop.FilePath() {
		t.Fatalf("Expected filepath %v, was %v", filepath, aop.FilePath())
	}

	// The returned bytes should be empty.
	if len(readBytes) != 0 {
		t.Fatalf("Expected %v returned bytes, got %v", 0, len(readBytes))
	}

	// Check the persist length.
	if aop.PersistLength() != MetadataPageSize {
		t.Fatalf("Expected persist length %v, was %v", MetadataPageSize, aop.PersistLength())
	}

	// Write some corruption data to the persist file before any other write, it
	// should get truncated off.
	f, err := os.OpenFile(filepath, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	minNumBytes := int(2 * MetadataPageSize)
	_, err = f.Write(fastrand.Bytes(minNumBytes + fastrand.Intn(minNumBytes)))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Write some data to the AOP.
	length1 := 1000
	bytes1 := fastrand.Bytes(length1)
	numBytes, err := aop.Write(bytes1)
	if err != nil {
		t.Fatal(err)
	}
	if numBytes != length1 {
		t.Fatalf("Expected to write %v bytes, wrote %v bytes", length1, numBytes)
	}

	// Write some corruption data to the persist file after another write, it
	// should get truncated off.
	f, err = os.OpenFile(filepath, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write(fastrand.Bytes(minNumBytes + fastrand.Intn(minNumBytes)))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	err = aop.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Load the AOP again.
	aop, reader, err = NewAppendOnlyPersist(testdir, filename, testHeader, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	readBytes, err = ioutil.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	// Check the persist length.
	expectedLength := MetadataPageSize + uint64(length1)
	if aop.PersistLength() != expectedLength {
		t.Fatalf("Expected persist length %v, was %v", expectedLength, aop.PersistLength())
	}

	// Check the returned bytes.
	if !bytes.Equal(readBytes, bytes1) {
		t.Fatalf("Expected and received byte slices don't match")
	}

	// Write more data to the AOP.
	length2 := 500
	bytes2 := fastrand.Bytes(length2)
	numBytes, err = aop.Write(bytes2)
	if err != nil {
		t.Fatal(err)
	}
	if numBytes != length2 {
		t.Fatalf("Expected to write %v bytes, wrote %v bytes", length2, numBytes)
	}
	err = aop.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Load the AOP again.
	aop, reader, err = NewAppendOnlyPersist(testdir, filename, testHeader, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	readBytes, err = ioutil.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	// Check the persist length.
	expectedLength = MetadataPageSize + uint64(length1) + uint64(length2)
	if aop.PersistLength() != expectedLength {
		t.Fatalf("Expected persist length %v, was %v", expectedLength, aop.PersistLength())
	}

	// Check the returned bytes (should have been appended).
	if !bytes.Equal(readBytes, append(bytes1, bytes2...)) {
		t.Fatalf("Expected and received byte slices don't match")
	}
	err = aop.Close()
	if err != nil {
		t.Fatal(err)
	}
}

// TestMarshalMetadata verifies that the marshaling and unmarshaling of the
// metadata and length provides the expected results
func TestMarshalMetadata(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create persist file
	testdir := build.TempDir("appendonlypersist", t.Name())
	testfile := "testpersist"
	err := os.MkdirAll(testdir, defaultDirPermissions)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(testdir, testfile)
	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, defaultFilePermissions)
	if err != nil {
		t.Fatal(err)
	}

	// Manually create struct of a persist object and set the length. Not using
	// the New method to avoid overwriting the persist file on disk.
	aop := AppendOnlyPersist{
		staticPath: filename,

		metadata: appendOnlyPersistMetadata{
			Header:  testHeader,
			Version: testVersion,

			Length: MetadataPageSize,
		},
		staticF: f,
	}
	defer func() {
		if err := aop.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Marshal the metadata and write to disk
	metadataBytes := encoding.Marshal(aop.metadata)
	_, err = f.Write(metadataBytes)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Sync()
	if err != nil {
		t.Fatal(err)
	}

	// Update the length, and write to disk
	lengthOffset := int64(2 * types.SpecifierLen)
	lengthBytes := encoding.Marshal(2 * MetadataPageSize)
	_, err = f.WriteAt(lengthBytes, lengthOffset)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Sync()
	if err != nil {
		t.Fatal(err)
	}

	// Try unmarshaling the metadata to ensure that it did not get corrupted by
	// the length updates
	metadataSize := uint64(lengthOffset) + lengthSize
	mdBytes := make([]byte, metadataSize)
	_, err = f.ReadAt(mdBytes, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The header and the version are checked during the unmarshaling of the
	// metadata
	var aopm appendOnlyPersistMetadata
	err = encoding.Unmarshal(mdBytes, &aopm)
	if err != nil {
		t.Fatal(err)
	}
	err = aop.updateMetadata(aopm)
	if err != nil {
		t.Fatal(err)
	}
	if aop.metadata.Length != 2*MetadataPageSize {
		t.Fatalf("incorrect decoded length, got %v expected %v", aop.metadata.Length, 2*MetadataPageSize)
	}

	// Write an incorrect version and verify that unmarshaling the metadata will
	// fail for unmarshaling a bad version
	badVersion := types.NewSpecifier("badversion")
	badBytes, err := badVersion.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(badBytes, types.SpecifierLen)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Sync()
	if err != nil {
		t.Fatal(err)
	}
	mdBytes = make([]byte, metadataSize)
	_, err = f.ReadAt(mdBytes, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = encoding.Unmarshal(mdBytes, &aopm)
	if err != nil {
		t.Fatal(err)
	}
	err = aop.updateMetadata(aopm)
	if !errors.Contains(err, ErrWrongVersion) {
		t.Fatalf("Expected %v got %v", ErrWrongVersion, err)
	}

	// Write an incorrect header and verify that unmarshaling the metadata will
	// fail for unmarshaling a bad header
	badHeader := types.NewSpecifier("badheader")
	badBytes, err = badHeader.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(badBytes, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Sync()
	if err != nil {
		t.Fatal(err)
	}
	mdBytes = make([]byte, metadataSize)
	_, err = f.ReadAt(mdBytes, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = encoding.Unmarshal(mdBytes, &aopm)
	if err != nil {
		t.Fatal(err)
	}
	err = aop.updateMetadata(aopm)
	if !errors.Contains(err, ErrWrongHeader) {
		t.Fatalf("Expected %v got %v", ErrWrongHeader, err)
	}
}
