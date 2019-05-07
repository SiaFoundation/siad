package siafile

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/fastrand"
)

// TestNumChunkPagesRequired tests numChunkPagesRequired.
func TestNumChunkPagesRequired(t *testing.T) {
	for numPieces := 0; numPieces < 1000; numPieces++ {
		chunkSize := marshaledChunkSize(numPieces)
		expectedPages := uint8(chunkSize / pageSize)
		if chunkSize%pageSize != 0 {
			expectedPages++
		}
		if numChunkPagesRequired(numPieces) != expectedPages {
			t.Fatalf("expected %v pages but got %v", expectedPages, numChunkPagesRequired(numPieces))
		}
	}
}

// TestMarshalUnmarshalChunk tests marshaling and unmarshaling a single chunk.
func TestMarshalUnmarshalChunk(t *testing.T) {
	// Get random chunk.
	chunk := randomChunk()
	numPieces := uint32(len(chunk.Pieces))

	// Marshal the chunk.
	chunkBytes := marshalChunk(chunk)
	// Check the length of the marshaled chunk.
	if int64(len(chunkBytes)) != marshaledChunkSize(chunk.numPieces()) {
		t.Fatalf("ChunkBytes should have len %v but was %v",
			marshaledChunkSize(chunk.numPieces()), len(chunkBytes))
	}
	// Add some random bytes to the chunkBytes. It should be possible to
	// unmarshal chunks even if we pass in data that is padded at the end.
	chunkBytes = append(chunkBytes, fastrand.Bytes(100)...)

	// Unmarshal the chunk.
	unmarshaledChunk, err := unmarshalChunk(numPieces, chunkBytes)
	if err != nil {
		t.Fatal(err)
	}
	// Compare unmarshaled chunk to original.
	if !reflect.DeepEqual(chunk, unmarshaledChunk) {
		t.Log("original", chunk)
		t.Log("unmarshaled", unmarshaledChunk)
		t.Fatal("Unmarshaled chunk doesn't equal marshaled chunk")
	}
}

// TestMarshalUnmarshalErasureCoder tests marshaling and unmarshaling an
// ErasureCoder.
func TestMarshalUnmarshalErasureCoder(t *testing.T) {
	for i := 0; i < 10; i++ {
		for j := 0; j < 20; j++ {
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
	raw, err := marshalMetadata(sf.staticMetadata)
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
	if sf.staticMetadata.AccessTime.Unix() != md.AccessTime.Unix() {
		t.Fatal("AccessTime's don't match")
	}
	if sf.staticMetadata.ChangeTime.Unix() != md.ChangeTime.Unix() {
		t.Fatal("ChangeTime's don't match")
	}
	if sf.staticMetadata.CreateTime.Unix() != md.CreateTime.Unix() {
		t.Fatal("CreateTime's don't match")
	}
	if sf.staticMetadata.ModTime.Unix() != md.ModTime.Unix() {
		t.Fatal("ModTime's don't match")
	}
	if sf.staticMetadata.LastHealthCheckTime.Unix() != md.LastHealthCheckTime.Unix() {
		t.Fatal("LastHealthCheckTime's don't match")
	}
	// Set the timestamps to zero for DeepEqual.
	sf.staticMetadata.AccessTime = time.Time{}
	sf.staticMetadata.ChangeTime = time.Time{}
	sf.staticMetadata.CreateTime = time.Time{}
	sf.staticMetadata.ModTime = time.Time{}
	sf.staticMetadata.LastHealthCheckTime = time.Time{}
	md.AccessTime = time.Time{}
	md.ChangeTime = time.Time{}
	md.CreateTime = time.Time{}
	md.ModTime = time.Time{}
	md.LastHealthCheckTime = time.Time{}
	// Compare result to original
	if !reflect.DeepEqual(md, sf.staticMetadata) {
		t.Fatal("Unmarshaled metadata not equal to marshaled metadata:", err)
	}
}

// TestMarshalUnmarshalPubKeyTable tests marshaling and unmarshaling the
// publicKeyTable of a SiaFile.
func TestMarshalUnmarshalPubKeyTable(t *testing.T) {
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
		if spk.Used != sf.pubKeyTable[i].Used {
			t.Fatal("Use fields don't match")
		}
		if spk.PublicKey.Algorithm != sf.pubKeyTable[i].PublicKey.Algorithm {
			t.Fatal("Algorithms don't match")
		}
		if !bytes.Equal(spk.PublicKey.Key, sf.pubKeyTable[i].PublicKey.Key) {
			t.Fatal("Keys don't match")
		}
	}
}

// TestMarshalUnmarshalPiece tests marshaling and unmarshaling a single piece
// of a chunk.
func TestMarshalUnmarshalPiece(t *testing.T) {
	// Create random piece.
	piece := randomPiece()
	pieceIndex := uint32(fastrand.Intn(100))

	// Marshal the piece.
	pieceBytes := make([]byte, marshaledPieceSize)
	putPiece(pieceBytes, pieceIndex, piece)

	// Unmarshal the piece.
	unmarshaledPieceIndex, unmarshaledPiece, err := unmarshalPiece(pieceBytes)
	if err != nil {
		t.Fatal(err)
	}
	// Compare the pieceIndex.
	if unmarshaledPieceIndex != pieceIndex {
		t.Fatalf("Piece index should be %v but was %v", pieceIndex, unmarshaledPieceIndex)
	}
	// Compare the piece to the original.
	if !reflect.DeepEqual(unmarshaledPiece, piece) {
		t.Fatal("Piece doesn't equal unmarshaled piece")
	}
}
