package siafile

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// TestSnapshot tests if a snapshot is created correctly from a SiaFile.
func TestSnapshot(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a random file for testing and create a snapshot from it.
	sf := dummyEntry(newTestFile())
	snap := sf.Snapshot()

	// Make sure the snapshot has the same fields as the SiaFile.
	if len(sf.staticChunks) != len(snap.staticChunks) {
		t.Errorf("expected %v chunks but got %v", len(sf.staticChunks), len(snap.staticChunks))
	}
	if sf.staticMetadata.StaticFileSize != snap.staticFileSize {
		t.Errorf("staticFileSize was %v but should be %v",
			snap.staticFileSize, sf.staticMetadata.StaticFileSize)
	}
	if sf.staticMetadata.StaticPieceSize != snap.staticPieceSize {
		t.Errorf("staticPieceSize was %v but should be %v",
			snap.staticPieceSize, sf.staticMetadata.StaticPieceSize)
	}
	if sf.staticMetadata.staticErasureCode.MinPieces() != snap.staticErasureCode.MinPieces() {
		t.Errorf("minPieces was %v but should be %v",
			sf.staticMetadata.staticErasureCode.MinPieces(), snap.staticErasureCode.MinPieces())
	}
	if sf.staticMetadata.staticErasureCode.NumPieces() != snap.staticErasureCode.NumPieces() {
		t.Errorf("numPieces was %v but should be %v",
			sf.staticMetadata.staticErasureCode.NumPieces(), snap.staticErasureCode.NumPieces())
	}
	if !bytes.Equal(sf.staticMetadata.StaticMasterKey, snap.staticMasterKey.Key()) {
		t.Error("masterkeys don't match")
	}
	if sf.staticMetadata.StaticMasterKeyType != snap.staticMasterKey.Type() {
		t.Error("masterkey types don't match")
	}
	if sf.staticMetadata.Mode != snap.staticMode {
		t.Error("modes don't match")
	}
	if !reflect.DeepEqual(sf.pubKeyTable, snap.staticPubKeyTable) {
		t.Error("pubkeytables don't match")
	}
	if sf.SiaPath() != snap.staticSiaPath {
		t.Error("siapaths don't match")
	}
	// Compare the pieces.
	for i := range sf.staticChunks {
		sfPieces, err1 := sf.Pieces(uint64(i))
		snapPieces, err2 := snap.Pieces(uint64(i))
		if err := errors.Compose(err1, err2); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(sfPieces, snapPieces) {
			t.Error("Pieces don't match")
		}
	}
}

// BenchmarkSnapshot10MB benchmarks the creation of snapshots for Siafiles
// which hold the metadata of a 10 MB file.
func BenchmarkSnapshot10MB(b *testing.B) {
	benchmarkSnapshot(b, uint64(1e7))
}

// BenchmarkSnapshot100MB benchmarks the creation of snapshots for Siafiles
// which hold the metadata of a 100 MB file.
func BenchmarkSnapshot100MB(b *testing.B) {
	benchmarkSnapshot(b, uint64(1e8))
}

// BenchmarkSnapshot1GB benchmarks the creation of snapshots for Siafiles
// which hold the metadata of a 1 GB file.
func BenchmarkSnapshot1GB(b *testing.B) {
	benchmarkSnapshot(b, uint64(1e9))
}

// BenchmarkSnapshot10GB benchmarks the creation of snapshots for Siafiles
// which hold the metadata of a 10 GB file.
func BenchmarkSnapshot10GB(b *testing.B) {
	benchmarkSnapshot(b, uint64(1e10))
}

// benchmarkSnapshot is a helper function for benchmarking the creation of
// snapshots of Siafiles with different sizes.
func benchmarkSnapshot(b *testing.B, fileSize uint64) {
	// Setup the file.
	siaFilePath, siaPath, source, rc, sk, _, _, fileMode := newTestFileParams()

	// Create the path to the file.
	dir, _ := filepath.Split(siaFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		b.Fatal(err)
	}
	// Create the file.
	chunkSize := (modules.SectorSize - crypto.TypeDefaultRenter.Overhead()) * uint64(rc.MinPieces())
	numChunks := fileSize / chunkSize
	if fileSize%chunkSize != 0 {
		numChunks++
	}
	wal, _ := newTestWAL()
	siafile, err := New(siaPath, siaFilePath, source, wal, rc, sk, fileSize, fileMode)
	if err != nil {
		b.Fatal(err)
	}
	sf := dummyEntry(siafile)
	// Add a host key to the table.
	sf.addRandomHostKeys(1)
	// Add numPieces to each chunks.
	for i := uint64(0); i < sf.NumChunks(); i++ {
		for j := uint64(0); j < uint64(rc.NumPieces()); j++ {
			sf.staticChunks[i].Pieces[j] = append(sf.staticChunks[i].Pieces[j], piece{})
		}
	}
	// Save the file to disk.
	if err := sf.saveFile(); err != nil {
		b.Fatal(err)
	}
	// Reset the timer.
	b.ResetTimer()

	// Create snapshots as fast as possible.
	for i := 0; i < b.N; i++ {
		_ = sf.Snapshot()
	}
}
