package siafile

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

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
	// Create arguments for new file.
	sk := crypto.GenerateSiaKey(crypto.RandomCipherType())
	pieceSize := modules.SectorSize - sk.Type().Overhead()
	siaPath := string(hex.EncodeToString(fastrand.Bytes(8)))
	rc, err := NewRSCode(10, 20)
	if err != nil {
		panic(err)
	}
	fileSize := pieceSize * 10
	fileMode := os.FileMode(777)
	source := string(hex.EncodeToString(fastrand.Bytes(8)))

	// Create the path to the file.
	siaFilePath := filepath.Join(os.TempDir(), "siafiles", siaPath)
	dir, _ := filepath.Split(siaFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		panic(err)
	}
	// Create the file.
	sf, err := New(siaFilePath, siaPath, source, newTestWAL(), []modules.ErasureCoder{rc}, sk, fileSize, fileMode)
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

// TestNewFile tests that a new file has the correct contents and size and that
// loading it from disk also works.
func TestNewFile(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	sf := newTestFile()

	// Marshal the metadata.
	md, err := marshalMetadata(sf.metadata)
	if err != nil {
		t.Fatal(err)
	}
	// Marshal the pubKeyTable.
	pkt, err := marshalPubKeyTable(sf.pubKeyTable)
	if err != nil {
		t.Fatal(err)
	}
	// Marshal the chunks.
	chunks, err := marshalChunks(sf.staticChunks)
	if err != nil {
		t.Fatal(err)
	}
	// Open the file.
	f, err := os.OpenFile(sf.siaFilePath, os.O_RDWR, 777)
	if err != nil {
		t.Fatal("Failed to open file", err)
	}
	defer f.Close()
	// Check the filesize.
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != sf.MDChunkOffset+int64(len(chunks)) {
		t.Fatal("file doesn't have right size")
	}
	// Compare the metadata to the on-disk metadata.
	readMD := make([]byte, len(md))
	_, err = f.ReadAt(readMD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readMD, md) {
		t.Fatal("metadata doesn't equal on-disk metadata")
	}
	// Compare the pubKeyTable to the on-disk pubKeyTable.
	readPKT := make([]byte, len(pkt))
	_, err = f.ReadAt(readPKT, sf.MDPubKeyTableOffset)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readPKT, pkt) {
		t.Fatal("pubKeyTable doesn't equal on-disk pubKeyTable")
	}
	// Compare the chunks to the on-disk chunks.
	readChunks := make([]byte, len(chunks))
	_, err = f.ReadAt(readChunks, sf.MDChunkOffset)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readChunks, chunks) {
		t.Fatal("readChunks don't equal on-disk readChunks")
	}
	// Load the file from disk and check that they are the same.
	sf2, err := LoadSiaFile(sf.siaFilePath, sf.wal)
	if err != nil {
		t.Fatal("failed to load SiaFile from disk", err)
	}
	// Compare the timestamps first since they can't be compared with
	// DeepEqual.
	if sf.MDAccessTime.Unix() != sf2.MDAccessTime.Unix() {
		t.Fatal("AccessTime's don't match")
	}
	if sf.MDChangeTime.Unix() != sf2.MDChangeTime.Unix() {
		t.Fatal("ChangeTime's don't match")
	}
	if sf.MDCreateTime.Unix() != sf2.MDCreateTime.Unix() {
		t.Fatal("CreateTime's don't match")
	}
	if sf.MDModTime.Unix() != sf2.MDModTime.Unix() {
		t.Fatal("ModTime's don't match")
	}
	// Set the timestamps to zero for DeepEqual.
	sf.MDAccessTime = time.Time{}
	sf.MDChangeTime = time.Time{}
	sf.MDCreateTime = time.Time{}
	sf.MDModTime = time.Time{}
	sf2.MDAccessTime = time.Time{}
	sf2.MDChangeTime = time.Time{}
	sf2.MDCreateTime = time.Time{}
	sf2.MDModTime = time.Time{}
	// Compare the rest of sf and sf2.
	if !reflect.DeepEqual(sf.metadata, sf2.metadata) {
		fmt.Println(sf.metadata)
		fmt.Println(sf2.metadata)
		t.Error("sf metadata doesn't equal sf2 metadata")
	}
	if !reflect.DeepEqual(sf.pubKeyTable, sf2.pubKeyTable) {
		t.Error("sf pubKeyTable doesn't equal sf2 pubKeyTable")
	}
	if !reflect.DeepEqual(sf.staticChunks, sf2.staticChunks) {
		t.Error("sf chunks don't equal sf2 chunks")
	}
	if sf.siaFilePath != sf2.siaFilePath {
		t.Errorf("sf2 filepath was %v but should be %v",
			sf2.siaFilePath, sf.siaFilePath)
	}
}

// TestCreateReadInsertUpdate tests if an update can be created using createInsertUpdate
// and if the created update can be read using readInsertUpdate.
func TestCreateReadInsertUpdate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	sf := newTestFile()
	// Create update randomly
	index := int64(fastrand.Intn(100))
	data := fastrand.Bytes(10)
	update := sf.createInsertUpdate(index, data)
	// Read update
	readPath, readIndex, readData, err := readInsertUpdate(update)
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

// TestCreateReadDeleteUpdate tests if an update can be created using
// createDeleteUpdate and if the created update can be read using
// readDeleteUpdate.
func TestCreateReadDeleteUpdate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	sf := newTestFile()
	update := sf.createDeleteUpdate()
	// Read update
	path := readDeleteUpdate(update)
	// Compare values
	if path != sf.siaFilePath {
		t.Error("paths doesn't match")
	}
}

// TestDelete tests if deleting a siafile removes the file from disk and sets
// the deleted flag correctly.
func TestDelete(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	sf := newTestFile()
	// Delete file.
	if err := sf.Delete(); err != nil {
		t.Fatal("Failed to delete file", err)
	}
	// Check if file was deleted and if deleted flag was set.
	if !sf.Deleted() {
		t.Fatal("Deleted flag was not set correctly")
	}
	if _, err := os.Open(sf.siaFilePath); !os.IsNotExist(err) {
		t.Fatal("Expected a file doesn't exist error but got", err)
	}
}

// TestRename tests if renaming a siafile moves the file correctly and also
// updates the metadata.
func TestRename(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	sf := newTestFile()

	// Create new paths for the file.
	newSiaPath := sf.MDSiaPath + "1"
	newSiaFilePath := sf.siaFilePath + "1"
	oldSiaFilePath := sf.siaFilePath

	// Rename file
	if err := sf.Rename(newSiaPath, newSiaFilePath); err != nil {
		t.Fatal("Failed to rename file", err)
	}

	// Check if the file was moved.
	if _, err := os.Open(oldSiaFilePath); !os.IsNotExist(err) {
		t.Fatal("Expected a file doesn't exist error but got", err)
	}
	f, err := os.Open(newSiaFilePath)
	if err != nil {
		t.Fatal("Failed to open file at new location", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// Check the metadata.
	if sf.siaFilePath != newSiaFilePath {
		t.Fatal("SiaFilePath wasn't updated correctly")
	}
	if sf.MDSiaPath != newSiaPath {
		t.Fatal("SiaPath wasn't updated correctly")
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

// TestMarshalUnmarshalChunks tests marshaling and unmarshaling the chunks of a
// SiaFile.
func TestMarshalUnmarshalChunks(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	sf := newTestFile()
	// The testing file has a chunk. Duplicate that to get more chunks for
	// testing.
	for i := 0; i < 3; i++ {
		sf.staticChunks = append(sf.staticChunks, sf.staticChunks...)
	}
	if len(sf.staticChunks) < 8 {
		t.Fatal("Not enough chunks for testing")
	}
	// Marshal the chunks.
	raw, err := marshalChunks(sf.staticChunks)
	if err != nil {
		t.Fatal("Failed to marshal chunks", err)
	}
	// Unmarshal the chunks again.
	chunks, err := unmarshalChunks(raw)
	if err != nil {
		t.Fatal("Failed to unmarshal chunks", err)
	}
	// Compare them to the original ones.
	if !reflect.DeepEqual(sf.staticChunks, chunks) {
		t.Fatal("Unmarshaled chunks don't equal origin chunks")
	}
}

// TestMarshalUnmarshalErasureCoder tests marshaling and unmarshaling an
// ErasureCoder.
func TestMarshalUnmarshalErasureCoder(t *testing.T) {
	rc, err := NewRSCode(10, 20)
	if err != nil {
		t.Fatal("failed to create reed solomon coder", err)
	}
	// Get the minimum pieces and the total number of pieces.
	numPieces, minPieces := rc.NumPieces(), rc.MinPieces()
	// Marshal the erasure coder.
	ecType, ecParams := marshalErasureCoder(rc)
	// Unmarshal it.
	rc2, err := unmarshalErasureCoder(ecType, ecParams)
	if err != nil {
		t.Fatal("failed to unmarshal reed solomon coder", err)
	}
	// Check if the settings are still the same.
	if numPieces != rc2.NumPieces() {
		t.Errorf("expected %v numPieces but was %v", numPieces, rc2.NumPieces())
	}
	if minPieces != rc2.MinPieces() {
		t.Errorf("expected %v minPieces but was %v", minPieces, rc2.MinPieces())
	}
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
	raw, err := marshalMetadata(sf.metadata)
	if err != nil {
		t.Fatal("Failed to marshal metadata", err)
	}
	// Unmarshal metadata
	md, err := unmarshalMetadata(raw)
	if err != nil {
		t.Fatal("Failed to unmarshal metadata", err)
	}
	// Compare the timestamps first since they can't be compared with
	// DeepEqual.
	if sf.MDAccessTime.Unix() != md.MDAccessTime.Unix() {
		t.Fatal("AccessTime's don't match")
	}
	if sf.MDChangeTime.Unix() != md.MDChangeTime.Unix() {
		t.Fatal("ChangeTime's don't match")
	}
	if sf.MDCreateTime.Unix() != md.MDCreateTime.Unix() {
		t.Fatal("CreateTime's don't match")
	}
	if sf.MDModTime.Unix() != md.MDModTime.Unix() {
		t.Fatal("ModTime's don't match")
	}
	// Set the timestamps to zero for DeepEqual.
	sf.MDAccessTime = time.Time{}
	sf.MDChangeTime = time.Time{}
	sf.MDCreateTime = time.Time{}
	sf.MDModTime = time.Time{}
	md.MDAccessTime = time.Time{}
	md.MDChangeTime = time.Time{}
	md.MDCreateTime = time.Time{}
	md.MDModTime = time.Time{}
	// Compare result to original
	if !reflect.DeepEqual(md, sf.metadata) {
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

	// Make sure the metadata was written to disk correctly.
	rawMetadata, err := marshalMetadata(sf.metadata)
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
	if _, err := f.ReadAt(readPubKeyTable, sf.MDPubKeyTableOffset); err != nil {
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

	// Open the file.
	f, err := os.OpenFile(sf.siaFilePath, os.O_RDWR, 777)
	if err != nil {
		t.Fatal("Failed to open file", err)
	}
	defer f.Close()

	// Write some data right after the ChunkOffset as a checksum.
	chunkData := fastrand.Bytes(100)
	_, err = f.WriteAt(chunkData, sf.MDChunkOffset)
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
	if sf.MDChunkOffset != 2*pageSize {
		t.Fatal("ChunkOffset wasn't updated correctly")
	}

	// Make sure that the checksum was moved correctly.
	readChunkData := make([]byte, len(chunkData))
	if _, err := f.ReadAt(readChunkData, sf.MDChunkOffset); err != nil {
		t.Fatal("Checksum wasn't moved correctly")
	}

	// Make sure the metadata was written to disk correctly.
	rawMetadata, err := marshalMetadata(sf.metadata)
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
	if _, err := f.ReadAt(readPubKeyTable, sf.MDPubKeyTableOffset); err != nil {
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
	update := siaFile.createInsertUpdate(int64(index), data)

	// Apply update.
	if err := apply(update); err != nil {
		t.Fatal("Failed to apply update", err)
	}
	// Open file.
	file, err := os.Open(siaFile.siaFilePath)
	if err != nil {
		t.Fatal("Failed to open file", err)
	}
	// Check if correct data was written.
	readData := make([]byte, len(data))
	if _, err := file.ReadAt(readData, int64(index)); err != nil {
		t.Fatal("Failed to read written data back from disk", err)
	}
	if !bytes.Equal(data, readData) {
		t.Fatal("Read data doesn't equal written data")
	}
}
