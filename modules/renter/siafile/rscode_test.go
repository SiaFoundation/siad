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
	// Create the RSCode
	rsc, err := NewRSCode(10, 20)
	if err != nil {
		t.Fatal(err)
	}
	// Encode 1000 bytes of data into 10 pieces.
	data := fastrand.Bytes(1000)
	shards, err := rsc.Encode(data)
	if err != nil {
		t.Fatal(err)
	}
	// Pick 10 random shards to use for recovery.
	pickedShards := make(map[int]struct{})
	for len(pickedShards) < rsc.MinPieces() {
		pickedShards[fastrand.Intn(rsc.NumPieces())] = struct{}{}
	}
	// Try to only use the first 50 bytes of each shard for the recovery and
	// see what happens.
	recoverShards := make([][]byte, len(shards))
	for i := range pickedShards {
		recoverShards[i] = shards[i][:50]
	}
	buf := new(bytes.Buffer)
	if err := rsc.Recover(recoverShards, 777, buf); err != nil {
		t.Fatal(err)
	}
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
