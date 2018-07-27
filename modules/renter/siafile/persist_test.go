package siafile

import (
	"bytes"
	"io"
	"os"
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
	siaPath := string(fastrand.Bytes(8))
	rc := NewRSCode(10, 20)
	pieceSize := modules.SectorSize - crypto.TwofishOverhead
	fileSize := pieceSize * 10
	fileMode := os.FileMode(777)
	source := string(fastrand.Bytes(8))

	return New(siaPath, []modules.ErasureCoder{rc}, pieceSize, fileSize, fileMode, source)
}

// newTestWal is a helper method to create a WAL for testing.
func newTestWAL() *writeaheadlog.WAL {
	_, wal, err := writeaheadlog.New(string(fastrand.Bytes(8)))
	if err != nil {
		panic(err)
	}
	return wal
}

// TestCreateReadUpdate tests if an update can be created using createUpdate
// and if the created update can be read using readUpdate.
func TestCreateReadUpdate(t *testing.T) {
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
