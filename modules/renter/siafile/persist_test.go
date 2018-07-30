package siafile

import (
	"bytes"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// rcCode is a dummy implementation of modules.ErasureCoder
type rsCode struct {
	numPieces  int
	dataPieces int
}

func (rs *rsCode) NumPieces() int                                       { return rs.numPieces }
func (rs *rsCode) MinPieces() int                                       { return rs.dataPieces }
func (rs *rsCode) Encode(data []byte) ([][]byte, error)                 { return nil, nil }
func (rs *rsCode) EncodeShards(pieces [][]byte) ([][]byte, error)       { return nil, nil }
func (rs *rsCode) Recover(pieces [][]byte, n uint64, w io.Writer) error { return nil }
func NewRSCode(nData, nParity int) modules.ErasureCoder {
	return &rsCode{
		numPieces:  nData + nParity,
		dataPieces: nData,
	}
}

// newTestFile is a helper method to create a SiaFile for testing.
func newTestFile() *SiaFile {
	siaPath := string(hex.EncodeToString(fastrand.Bytes(8)))
	rc := NewRSCode(10, 20)
	pieceSize := modules.SectorSize - crypto.TwofishOverhead
	fileSize := pieceSize * 10
	fileMode := os.FileMode(777)
	source := string(fastrand.Bytes(8))

	if err := os.MkdirAll("renter", 0600); err != nil {
		panic(err)
	}
	siaFilePath := filepath.Join(os.TempDir(), "siafiles", siaPath)
	sf, err := New(siaFilePath, siaPath, source, newTestWAL(), []modules.ErasureCoder{rc}, pieceSize, fileSize, fileMode)
	if err != nil {
		panic(err)
	}
	return sf
}

// newTestWal is a helper method to create a WAL for testing.
func newTestWAL() *writeaheadlog.WAL {
	// Create the wal.
	_, wal, err := writeaheadlog.New(hex.EncodeToString(fastrand.Bytes(8)))
	if err != nil {
		panic(err)
	}
	return wal
}

// TestCreateReadUpdate tests if an update can be created using createUpdate
// and if the created update can be read using readUpdate.
func TestCreateReadUpdate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	sf := newTestFile()
	// Create update randomly
	index := int64(fastrand.Intn(100))
	data := fastrand.Bytes(10)
	update := sf.createUpdate(index, data)
	// Read update
	readPath, readIndex, readData, err := readUpdate(update)
	if err != nil {
		t.Fatal("Failed to read update", err)
	}
	// Compare values
	if readPath != sf.siaFilePath {
		t.Error("paths doesn't match")
	}
	if readIndex != index {
		t.Error("index doesn't match")
	}
	if !bytes.Equal(readData, data) {
		t.Error("data doesn't match")
	}
}

// TestApplyUpdates tests the exported ApplyUpdates method.
func TestApplyUpdates(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	siaFile := newTestFile()

	// Create an update that writes random data to a random index i.
	index := fastrand.Intn(100) + 1
	data := fastrand.Bytes(100)
	update := siaFile.createUpdate(int64(index), data)

	// Apply update.
	if err := ApplyUpdates(update); err != nil {
		t.Fatal("Failed to apply update", err)
	}

	// Check if file has correct size after update.
	file, err := os.Open(siaFile.siaFilePath)
	if err != nil {
		t.Fatal("Failed to open file", err)
	}
	fi, err := file.Stat()
	if err != nil {
		t.Fatal("Failed to get fileinfo", err)
	}
	if fi.Size() != int64(index+len(data)) {
		t.Errorf("File's size should be %v but was %v", index+len(data), fi.Size())
	}

	// Check if correct data was written.
	readData := make([]byte, len(data))
	if _, err := file.ReadAt(readData, int64(index)); err != nil {
		t.Fatal("Failed to read written data back from disk", err)
	}
	if !bytes.Equal(data, readData) {
		t.Fatal("Read data doesn't equal written data")
	}
	// Make sure that we didn't write anything before the specified index.
	readData = make([]byte, index)
	expectedData := make([]byte, index)
	if _, err := file.ReadAt(readData, 0); err != nil {
		t.Fatal("Failed to read data back from disk", err)
	}
	if !bytes.Equal(expectedData, readData) {
		t.Fatal("ApplyUpdates corrupted the data before the specified index")
	}
}

// TestSiaFileApplyUpdates tests the siafile.applyUpdates method.
func TestSiaFileApplyUpdates(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	siaFile := newTestFile()

	// Create an update that writes random data to a random index i.
	index := fastrand.Intn(100) + 1
	data := fastrand.Bytes(100)
	update := siaFile.createUpdate(int64(index), data)

	// Apply update.
	if err := siaFile.applyUpdates(update); err != nil {
		t.Fatal("Failed to apply update", err)
	}

	// Check if file has correct size after update.
	file, err := os.Open(siaFile.siaFilePath)
	if err != nil {
		t.Fatal("Failed to open file", err)
	}
	fi, err := file.Stat()
	if err != nil {
		t.Fatal("Failed to get fileinfo", err)
	}
	if fi.Size() != int64(index+len(data)) {
		t.Errorf("File's size should be %v but was %v", index+len(data), fi.Size())
	}

	// Check if correct data was written.
	readData := make([]byte, len(data))
	if _, err := file.ReadAt(readData, int64(index)); err != nil {
		t.Fatal("Failed to read written data back from disk", err)
	}
	if !bytes.Equal(data, readData) {
		t.Fatal("Read data doesn't equal written data")
	}
	// Make sure that we didn't write anything before the specified index.
	readData = make([]byte, index)
	expectedData := make([]byte, index)
	if _, err := file.ReadAt(readData, 0); err != nil {
		t.Fatal("Failed to read data back from disk", err)
	}
	if !bytes.Equal(expectedData, readData) {
		t.Fatal("ApplyUpdates corrupted the data before the specified index")
	}
}
