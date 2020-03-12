package siafile

import (
	"bytes"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/crypto"
)

// TestPartialEncodeRecover checks that individual segments of an encoded piece
// can be recovered.
func TestPartialEncodeRecover(t *testing.T) {
	segmentSize := crypto.SegmentSize
	pieceSize := 4096
	dataPieces := 10
	parityPieces := 20
	data := fastrand.Bytes(pieceSize * dataPieces)
	originalData := make([]byte, len(data))
	copy(originalData, data)
	// Create the erasure coder.
	rsc, err := NewRSSubCode(dataPieces, parityPieces, uint64(segmentSize))
	if err != nil {
		t.Fatal(err)
	}
	// Allocate space for the pieces.
	pieces := make([][]byte, dataPieces)
	for i := range pieces {
		pieces[i] = make([]byte, pieceSize)
	}
	// Write the data to the pieces.
	buf := bytes.NewBuffer(data)
	for i := range pieces {
		if buf.Len() < pieceSize {
			t.Fatal("Buffer is empty")
		}
		pieces[i] = make([]byte, pieceSize)
		copy(pieces[i], buf.Next(pieceSize))
	}
	// Encode the pieces.
	encodedPieces, err := rsc.EncodeShards(pieces)
	if err != nil {
		t.Fatal(err)
	}
	// Check that the parity shards have been created.
	if len(encodedPieces) != rsc.NumPieces() {
		t.Fatalf("encodedPieces should've length %v but was %v", rsc.NumPieces(), len(encodedPieces))
	}
	// Every piece should have pieceSize.
	for _, piece := range encodedPieces {
		if len(piece) != pieceSize {
			t.Fatalf("expecte len(piece) to be %v but was %v", pieceSize, len(piece))
		}
	}
	// Delete as many random pieces as possible.
	for _, i := range fastrand.Perm(len(encodedPieces))[:parityPieces] {
		encodedPieces[i] = nil
	}
	// Recover every segment individually.
	dataOffset := 0
	decodedSegmentSize := segmentSize * dataPieces
	for segmentIndex := 0; segmentIndex < pieceSize/segmentSize; segmentIndex++ {
		buf := new(bytes.Buffer)
		segment := ExtractSegment(encodedPieces, segmentIndex, uint64(segmentSize))
		err = rsc.Recover(segment, uint64(segmentSize*rsc.MinPieces()), buf)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf.Bytes(), originalData[dataOffset:dataOffset+decodedSegmentSize]) {
			t.Fatal("decoded bytes don't equal original segment")
		}
		dataOffset += decodedSegmentSize
	}
	// Recover all segments at once.
	buf = new(bytes.Buffer)
	err = rsc.Recover(encodedPieces, uint64(len(data)), buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), originalData) {
		t.Fatal("decoded bytes don't equal original data")
	}
}

func BenchmarkRSSubCodeRecover(b *testing.B) {
	segmentSize := crypto.SegmentSize
	pieceSize := 4096
	dataPieces := 10
	parityPieces := 30
	data := fastrand.Bytes(pieceSize * dataPieces)
	originalData := make([]byte, len(data))
	copy(originalData, data)
	// Create the erasure coder.
	rsc, err := NewRSSubCode(dataPieces, parityPieces, uint64(segmentSize))
	if err != nil {
		b.Fatal(err)
	}
	// Allocate space for the pieces.
	pieces := make([][]byte, dataPieces)
	for i := range pieces {
		pieces[i] = make([]byte, pieceSize)
	}
	// Write the data to the pieces.
	buf := bytes.NewBuffer(data)
	for i := range pieces {
		if buf.Len() < pieceSize {
			b.Fatal("Buffer is empty")
		}
		pieces[i] = make([]byte, pieceSize)
		copy(pieces[i], buf.Next(pieceSize))
	}
	// Encode the pieces.
	encodedPieces, err := rsc.EncodeShards(pieces)
	if err != nil {
		b.Fatal(err)
	}
	// Check that the parity shards have been created.
	if len(encodedPieces) != rsc.NumPieces() {
		b.Fatalf("encodedPieces should've length %v but was %v", rsc.NumPieces(), len(encodedPieces))
	}
	// Every piece should have pieceSize.
	for _, piece := range encodedPieces {
		if len(piece) != pieceSize {
			b.Fatalf("expecte len(piece) to be %v but was %v", pieceSize, len(piece))
		}
	}
	// Delete all data shards
	for i := range encodedPieces[:dataPieces+1] {
		encodedPieces[i] = nil
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		buf.Reset()
		err = rsc.Recover(encodedPieces, uint64(len(data)), buf)
		if err != nil {
			b.Fatal(err)
		}
		if !bytes.Equal(buf.Bytes(), originalData) {
			b.Fatal("decoded bytes don't equal original data")
		}
	}
}
