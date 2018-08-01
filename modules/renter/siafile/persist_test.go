package siafile

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
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

// addRandomHostKeys adds n random host keys to the SiaFile's pubKeyTable. It
// doesn't write them to disk.
func (sf *SiaFile) addRandomHostKeys(n int) {
	for i := 0; i < n; i++ {
		// Create random specifier and key.
		algorithm := types.Specifier{}
		fastrand.Read(algorithm[:])

		// Create random key.
		key := fastrand.Bytes(32)

		// Append new key to slice.
		sf.pubKeyTable = append(sf.pubKeyTable, types.SiaPublicKey{
			Algorithm: algorithm,
			Key:       key,
		})
	}
}

// newTestFile is a helper method to create a SiaFile for testing.
func newTestFile() *SiaFile {
	siaPath := string(hex.EncodeToString(fastrand.Bytes(8)))
	rc := NewRSCode(10, 20)
	pieceSize := modules.SectorSize - crypto.TwofishOverhead
	fileSize := pieceSize * 10
	fileMode := os.FileMode(777)
	source := string(hex.EncodeToString(fastrand.Bytes(8)))

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
	walsDir := filepath.Join(os.TempDir(), "wals")
	if err := os.MkdirAll(walsDir, 0700); err != nil {
		panic(err)
	}
	walFilePath := filepath.Join(walsDir, hex.EncodeToString(fastrand.Bytes(8)))
	_, wal, err := writeaheadlog.New(walFilePath)
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
	t.Parallel()

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

// TestApplyUpdates tests a variety of functions that are used to apply
// updates.
func TestApplyUpdates(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	t.Run("TestApplyUpdates", func(t *testing.T) {
		siaFile := newTestFile()
		testApply(t, siaFile, ApplyUpdates)
	})
	t.Run("TestSiaFileApplyUpdates", func(t *testing.T) {
		siaFile := newTestFile()
		testApply(t, siaFile, siaFile.applyUpdates)
	})
	t.Run("TestCreateAndApplyTransaction", func(t *testing.T) {
		siaFile := newTestFile()
		testApply(t, siaFile, siaFile.createAndApplyTransaction)
	})
}

// TestMarshalUnmarshalMetadata tests marshaling and unmarshaling the metadata
// of a SiaFile.
func TestMarshalUnmarshalMetadata(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	sf := newTestFile()

	// Marshal metadata
	raw, err := marshalMetadata(sf.staticMetadata)
	if err != nil {
		t.Fatal("Failed to marshal metadata", err)
	}
	// Unmarshal metadata
	md, err := unmarshalMetadata(raw)
	if err != nil {
		t.Fatal("Failed to unmarshal metadata", err)
	}
	// Compare result to original
	if !reflect.DeepEqual(md, sf.staticMetadata) {
		t.Fatal("Unmarshaled metadata not equal to marshaled metadata:", err)
	}
}

// TestMarshalUnmarshalMetadata tests marshaling and unmarshaling the
// publicKeyTable of a SiaFile.
func TestMarshalUnmarshalPubKeyTAble(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	sf := newTestFile()
	sf.addRandomHostKeys(10)

	// Marshal pubKeyTable.
	raw, err := marshalPubKeyTable(sf.pubKeyTable)
	if err != nil {
		t.Fatal("Failed to marshal pubKeyTable", err)
	}
	// Unmarshal pubKeyTable.
	pubKeyTable, err := unmarshalPubKeyTable(raw)
	if err != nil {
		t.Fatal("Failed to unmarshal pubKeyTable", err)
	}
	// Compare them.
	if len(sf.pubKeyTable) != len(pubKeyTable) {
		t.Fatalf("Lengths of tables don't match %v vs %v",
			len(sf.pubKeyTable), len(pubKeyTable))
	}
	for i, spk := range pubKeyTable {
		if spk.Algorithm != sf.pubKeyTable[i].Algorithm {
			t.Fatal("Algorithms don't match")
		}
		if !bytes.Equal(spk.Key, sf.pubKeyTable[i].Key) {
			t.Fatal("Keys don't match")
		}
	}
}

// TestSaveSmallHeader tests the saveHeader method for a header that is not big
// enough to need more than a single page on disk.
func TestSaveSmallHeader(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	sf := newTestFile()

	// Add some host keys.
	sf.addRandomHostKeys(10)

	// Set the chunkOffset manually since we don't store any actual chunks.
	sf.staticMetadata.ChunkOffset = pageSize

	// Save the header.
	updates, err := sf.saveHeader()
	if err != nil {
		t.Fatal("Failed to create updates to save header", err)
	}
	if err := sf.createAndApplyTransaction(updates...); err != nil {
		t.Fatal("Failed to save header", err)
	}

	// Manually open the file to check its contents.
	f, err := os.Open(sf.siaFilePath)
	if err != nil {
		t.Fatal("Failed to open file", err)
	}
	defer f.Close()

	// The file should be exactly 1 page large.
	fi, err := f.Stat()
	if err != nil {
		t.Fatal("Failed to get fileinfo", err)
	}
	if fi.Size() != pageSize {
		t.Fatalf("Filesize should be %v but was %v", pageSize, fi.Size())
	}

	// Make sure the metadata was written to disk correctly.
	rawMetadata, err := marshalMetadata(sf.staticMetadata)
	if err != nil {
		t.Fatal("Failed to marshal metadata", err)
	}
	readMetadata := make([]byte, len(rawMetadata))
	if _, err := f.ReadAt(readMetadata, 0); err != nil {
		t.Fatal("Failed to read metadata", err)
	}
	if !bytes.Equal(rawMetadata, readMetadata) {
		fmt.Println(string(rawMetadata))
		fmt.Println(string(readMetadata))
		t.Fatal("Metadata on disk doesn't match marshaled metadata")
	}

	// Make sure that the pubKeyTable was written to disk correctly.
	rawPubKeyTAble, err := marshalPubKeyTable(sf.pubKeyTable)
	if err != nil {
		t.Fatal("Failed to marshal pubKeyTable", err)
	}
	readPubKeyTable := make([]byte, len(rawPubKeyTAble))
	if _, err := f.ReadAt(readPubKeyTable, sf.staticMetadata.PubKeyTableOffset); err != nil {
		t.Fatal("Failed to read pubKeyTable", err)
	}
	if !bytes.Equal(rawPubKeyTAble, readPubKeyTable) {
		t.Fatal("pubKeyTable on disk doesn't match marshaled pubKeyTable")
	}
}

// TestSaveLargeHeader tests the saveHeader method for a header that uses more than a single page on disk and forces a call to allocateHeaderPage
func TestSaveLargeHeader(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	sf := newTestFile()

	// Add some host keys.
	sf.addRandomHostKeys(100)

	// Set the chunkOffset manually since we don't store any actual chunks.
	sf.staticMetadata.ChunkOffset = pageSize

	// Open the file.
	f, err := os.OpenFile(sf.siaFilePath, os.O_RDWR, 777)
	if err != nil {
		t.Fatal("Failed to open file", err)
	}
	defer f.Close()

	// Write some data right after the ChunkOffset as a checksum.
	chunkData := fastrand.Bytes(100)
	_, err = f.WriteAt(chunkData, sf.staticMetadata.ChunkOffset)
	if err != nil {
		t.Fatal("Failed to write random chunk data", err)
	}

	// Save the header.
	updates, err := sf.saveHeader()
	if err != nil {
		t.Fatal("Failed to create updates to save header", err)
	}
	if err := sf.createAndApplyTransaction(updates...); err != nil {
		t.Fatal("Failed to save header", err)
	}

	// Make sure the chunkOffset was updated correctly.
	if sf.staticMetadata.ChunkOffset != 2*pageSize {
		t.Fatal("ChunkOffset wasn't updated correctly")
	}

	// The file should be exactly 2 pages + chunkData large.
	fi, err := f.Stat()
	if err != nil {
		t.Fatal("Failed to get fileinfo", err)
	}
	if fi.Size() != int64(2*pageSize+len(chunkData)) {
		t.Fatalf("Filesize should be %v but was %v", 2*pageSize+len(chunkData), fi.Size())
	}

	// Make sure that the checksum was moved correctly.
	readChunkData := make([]byte, len(chunkData))
	if _, err := f.ReadAt(readChunkData, sf.staticMetadata.ChunkOffset); err != nil {
		t.Fatal("Checksum wasn't moved correctly")
	}

	// Make sure the metadata was written to disk correctly.
	rawMetadata, err := marshalMetadata(sf.staticMetadata)
	if err != nil {
		t.Fatal("Failed to marshal metadata", err)
	}
	readMetadata := make([]byte, len(rawMetadata))
	if _, err := f.ReadAt(readMetadata, 0); err != nil {
		t.Fatal("Failed to read metadata", err)
	}
	if !bytes.Equal(rawMetadata, readMetadata) {
		fmt.Println(string(rawMetadata))
		fmt.Println(string(readMetadata))
		t.Fatal("Metadata on disk doesn't match marshaled metadata")
	}

	// Make sure that the pubKeyTable was written to disk correctly.
	rawPubKeyTAble, err := marshalPubKeyTable(sf.pubKeyTable)
	if err != nil {
		t.Fatal("Failed to marshal pubKeyTable", err)
	}
	readPubKeyTable := make([]byte, len(rawPubKeyTAble))
	if _, err := f.ReadAt(readPubKeyTable, sf.staticMetadata.PubKeyTableOffset); err != nil {
		t.Fatal("Failed to read pubKeyTable", err)
	}
	if !bytes.Equal(rawPubKeyTAble, readPubKeyTable) {
		t.Fatal("pubKeyTable on disk doesn't match marshaled pubKeyTable")
	}
}

// testApply tests if a given method applies a set of updates correctly.
func testApply(t *testing.T, siaFile *SiaFile, apply func(...writeaheadlog.Update) error) {
	// Create an update that writes random data to a random index i.
	index := fastrand.Intn(100) + 1
	data := fastrand.Bytes(100)
	update := siaFile.createUpdate(int64(index), data)

	// Apply update.
	if err := apply(update); err != nil {
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
