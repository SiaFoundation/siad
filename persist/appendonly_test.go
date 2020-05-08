package persist

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// TestMarshalMetadata verifies that the marshaling and unmarshaling of the
// metadata and length provides the expected results
func TestMarshalMetadata(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create persist file
	testdir := build.TempDir(t.Name())
	testfile := "testpersist"
	err := os.MkdirAll(testdir, DefaultDirPermissions)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(testdir, testfile)
	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, DefaultFilePermissions)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Manually create struct of a persist object and set the length. Not using
	// the New method to avoid overwriting the persist file on disk.
	aop := AppendOnlyPersist{
		staticPath:            filename,
		staticMetadataHeader:  types.NewSpecifier("header\n"),
		staticMetadataVersion: types.NewSpecifier("version\n"),

		persistLength: MetadataPageSize,
	}

	// Marshal the metadata and write to disk
	metadataBytes, err := aop.marshalMetadata()
	if err != nil {
		t.Fatal(err)
	}
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
	metadataSize := uint64(lengthOffset) + LengthSize
	mdBytes := make([]byte, metadataSize)
	_, err = f.ReadAt(mdBytes, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The header and the version are checked during the unmarshaling of the
	// metadata
	err = aop.unmarshalMetadata(mdBytes)
	if err != nil {
		t.Fatal(err)
	}
	if aop.persistLength != 2*MetadataPageSize {
		t.Fatalf("incorrect decoded length, got %v expected %v", aop.persistLength, 2*MetadataPageSize)
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
	err = aop.unmarshalMetadata(mdBytes)
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
	err = aop.unmarshalMetadata(mdBytes)
	if err != ErrWrongHeader {
		t.Fatalf("Expected %v got %v", ErrWrongHeader, err)
	}
}
