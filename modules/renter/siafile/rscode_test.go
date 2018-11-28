package siafile

import (
	"bytes"
	"io/ioutil"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"
)

// TestRSEncode tests the rsCode type.
func TestRSEncode(t *testing.T) {
	badParams := []struct {
		data, parity int
	}{
		{-1, -1},
		{-1, 0},
		{0, -1},
		{0, 0},
		{0, 1},
		{1, 0},
	}
	for _, ps := range badParams {
		if _, err := NewRSCode(ps.data, ps.parity); err == nil {
			t.Error("expected bad parameter error, got nil")
		}
	}

	rsc, err := NewRSCode(10, 3)
	if err != nil {
		t.Fatal(err)
	}

	data := fastrand.Bytes(777)

	pieces, err := rsc.Encode(data)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rsc.Encode(nil)
	if err == nil {
		t.Fatal("expected nil data error, got nil")
	}

	buf := new(bytes.Buffer)
	err = rsc.Recover(pieces, 777, buf)
	if err != nil {
		t.Fatal(err)
	}
	err = rsc.Recover(nil, 777, buf)
	if err == nil {
		t.Fatal("expected nil pieces error, got nil")
	}

	if !bytes.Equal(data, buf.Bytes()) {
		t.Fatal("recovered data does not match original")
	}
}

func TestPartialEncodeRecover(t *testing.T) {
	segmentSize := 64
	pieceSize := 4096
	dataPieces := 10
	parityPieces := 20
	data := fastrand.Bytes(pieceSize * dataPieces)
	// Create the erasure coder.
	rsc, err := NewRSCode(dataPieces, parityPieces)
	if err != nil {
		t.Fatal(err)
	}
	// Allocate space for the pieces.
	pieces := make([][][]byte, pieceSize/segmentSize)
	for i := range pieces {
		pieces[i] = make([][]byte, dataPieces)
		for j := range pieces[i] {
			pieces[i][j] = make([]byte, segmentSize)
		}
	}
	// Write the data to the pieces.
	buf := bytes.NewBuffer(data)
	for i := 0; i < pieceSize/segmentSize; i++ {
		for j := 0; j < dataPieces; j++ {
			if buf.Len() < segmentSize {
				t.Fatal("Buffer is empty")
			}
			pieces[i][j] = buf.Next(segmentSize)
		}
	}
	// Encode the pieces.
	encodedPieces, err := rsc.EncodeSubShards(pieces, uint64(pieceSize), uint64(segmentSize))
	if err != nil {
		t.Fatal(err)
	}
	// Check that the parity shards have been created.
	for _, piece := range encodedPieces {
		if len(piece) != rsc.NumPieces() {
			t.Fatalf("Piece should've length %v but was %v", rsc.NumPieces(), len(piece))
		}
	}
	// TODO Recover random segments.
}

func BenchmarkRSEncode(b *testing.B) {
	rsc, err := NewRSCode(80, 20)
	if err != nil {
		b.Fatal(err)
	}
	data := fastrand.Bytes(1 << 20)

	b.SetBytes(1 << 20)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rsc.Encode(data)
	}
}

func BenchmarkRSRecover(b *testing.B) {
	rsc, err := NewRSCode(50, 200)
	if err != nil {
		b.Fatal(err)
	}
	data := fastrand.Bytes(1 << 20)
	pieces, err := rsc.Encode(data)
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(1 << 20)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < len(pieces)/2; j += 2 {
			pieces[j] = nil
		}
		rsc.Recover(pieces, 1<<20, ioutil.Discard)
	}
}
