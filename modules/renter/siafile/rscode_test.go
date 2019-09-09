package siafile

import (
	"bytes"
	"io/ioutil"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"

	"gitlab.com/NebulousLabs/errors"
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

// TestIdentifierAndCombinedSiaPath checks that different erasure coders produce
// unique identifiers and that CombinedSiaFilePath also produces unique siapaths
// using the identifiers.
func TestIdentifierAndCombinedSiaPath(t *testing.T) {
	ec1, err1 := NewRSCode(1, 2)
	ec2, err2 := NewRSCode(1, 2)
	ec3, err3 := NewRSCode(1, 3)
	ec4, err4 := NewRSSubCode(1, 2, 64)
	if err := errors.Compose(err1, err2, err3, err4); err != nil {
		t.Fatal(err)
	}
	if ec1.Identifier() != "1+1+2" {
		t.Error("wrong identifier for ec1")
	}
	if ec2.Identifier() != "1+1+2" {
		t.Error("wrong identifier for ec2")
	}
	if ec3.Identifier() != "1+1+3" {
		t.Error("wrong identifier for ec3")
	}
	if ec4.Identifier() != "2+1+2" {
		t.Error("wrong identifier for ec4")
	}
	sp1 := modules.CombinedSiaFilePath(ec1)
	sp2 := modules.CombinedSiaFilePath(ec2)
	sp3 := modules.CombinedSiaFilePath(ec3)
	sp4 := modules.CombinedSiaFilePath(ec4)
	if !sp1.Equals(sp2) {
		t.Error("sp1 and sp2 should have the same path")
	}
	if sp1.Equals(sp3) {
		t.Error("sp1 and sp3 should have different path")
	}
	if sp1.Equals(sp4) {
		t.Error("sp1 and sp4 should have different path")
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
