package modules

import (
	"bytes"
	"io/ioutil"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
)

// TestErasureCode groups all of the tests the three implementations of the
// erasure code interface, as defined in erasure.go.
func TestErasureCode(t *testing.T) {
	t.Run("RSCode", testRSCode)
	t.Run("RSSubCode", testRSSubCode)
	t.Run("Passthrough", testPassthrough)
	t.Run("UniqueIdentifier", testUniqueIdentifier)
	t.Run("DefaultConstructors", testDefaultConstructors)
}

// testRSCode tests the RSCode EC.
func testRSCode(t *testing.T) {
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

// testRSSubCode checks that individual segments of an encoded piece can be
// recovered using the RSSub Code.
func testRSSubCode(t *testing.T) {
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

// testPassthrough verifies the functionality of the Passthrough EC.
func testPassthrough(t *testing.T) {
	ptec := NewPassthroughErasureCoder()

	if ptec.NumPieces() != 1 {
		t.Fatal("unexpected")
	}
	if ptec.MinPieces() != 1 {
		t.Fatal("unexpected")
	}

	data := fastrand.Bytes(777)
	pieces, err := ptec.Encode(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(pieces) != 1 {
		t.Fatal("unexpected amount of pieces")
	}
	if !bytes.Equal(pieces[0], data) {
		t.Fatal("unexpected piece")
	}

	if ptec.Identifier() != "ECPassthrough" {
		t.Fatal("unexpected")
	}
	pieces = [][]byte{data}
	encoded, err := ptec.EncodeShards(pieces)
	if err != nil {
		t.Fatal(err)
	}
	if len(pieces) != len(encoded) || len(pieces) != 1 {
		t.Fatal("unexpected")
	}
	if !bytes.Equal(pieces[0], encoded[0]) {
		t.Fatal("unexpected data")
	}

	err = ptec.Reconstruct(pieces)
	if err != nil {
		t.Fatal(err)
	}

	buf := new(bytes.Buffer)
	err = ptec.Recover(encoded, uint64(len(data)), buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Fatal("decoded bytes don't equal original data")
	}

	size, supported := ptec.SupportsPartialEncoding()
	if !supported || size != crypto.SegmentSize {
		t.Fatal("unexpected")
	}
}

// testUniqueIdentifier checks that different erasure coders produce unique
// identifiers and that CombinedSiaFilePath also produces unique siapaths using
// the identifiers.
func testUniqueIdentifier(t *testing.T) {
	ec1, err1 := NewRSCode(1, 2)
	ec2, err2 := NewRSCode(1, 2)
	ec3, err3 := NewRSCode(1, 3)
	ec4, err4 := NewRSSubCode(1, 2, 64)
	ec5, err5 := NewRSCode(1, 1)
	ec6 := NewPassthroughErasureCoder()

	if err := errors.Compose(err1, err2, err3, err4, err5); err != nil {
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
	if ec5.Identifier() != "1+1+1" {
		t.Error("wrong identifier for ec5")
	}
	if ec6.Identifier() != "ECPassthrough" {
		t.Error("wrong identifier for ec6")
	}
	sp1 := CombinedSiaFilePath(ec1)
	sp2 := CombinedSiaFilePath(ec2)
	sp3 := CombinedSiaFilePath(ec3)
	sp4 := CombinedSiaFilePath(ec4)
	sp5 := CombinedSiaFilePath(ec5)
	sp6 := CombinedSiaFilePath(ec6)
	if !sp1.Equals(sp2) {
		t.Error("sp1 and sp2 should have the same path")
	}
	if sp1.Equals(sp3) {
		t.Error("sp1 and sp3 should have different path")
	}
	if sp1.Equals(sp4) {
		t.Error("sp1 and sp4 should have different path")
	}
	if sp1.Equals(sp5) {
		t.Error("sp1 and sp5 should have different path")
	}
	if sp1.Equals(sp6) {
		t.Error("sp1 and sp6 should have different path")
	}
}

// testDefaultConstructors verifies the default constructor create erasure codes
// with the correct parameters
func testDefaultConstructors(t *testing.T) {
	rs, err := NewRSCode(RenterDefaultDataPieces, RenterDefaultParityPieces)
	if err != nil {
		t.Fatal(err)
	}
	rsd := NewRSCodeDefault()
	if rs.Identifier() != rsd.Identifier() {
		t.Fatal("Unexpected parameters used in default")
	}

	rss, err := NewRSSubCode(RenterDefaultDataPieces, RenterDefaultParityPieces, crypto.SegmentSize)
	if err != nil {
		t.Fatal(err)
	}
	rssd := NewRSSubCodeDefault()
	if rss.Identifier() != rssd.Identifier() {
		t.Fatal("Unexpected parameters used in default")
	}
}

// BenchmarkRSEncode benchmarks the 'Encode' function of the RSCode EC.
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

// BenchmarkRSEncode benchmarks the 'Recover' function of the RSCode EC.
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

// BenchmarkRSSubCodeRecover benchmarks the 'Recover' function of the RSSubCode
// EC.
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
