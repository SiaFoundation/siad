package modules

import (
	"bytes"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// newTestSkyfileLayout is a helper that returns a SkyfileLayout with some
// default settings for testing.
func newTestSkyfileLayout() SkyfileLayout {
	return SkyfileLayout{
		Version:            SkyfileVersion,
		Filesize:           1e6,
		MetadataSize:       14e3,
		FanoutSize:         75e3,
		FanoutDataPieces:   1,
		FanoutParityPieces: 10,
		CipherType:         crypto.TypePlain,
	}
}

// TestSkyfileLayoutEncoding checks that encoding and decoding a skyfile
// layout always results in the same struct.
func TestSkyfileLayoutEncoding(t *testing.T) {
	t.Parallel()
	// Try encoding an decoding a simple example.
	llOriginal := newTestSkyfileLayout()
	rand := fastrand.Bytes(64)
	copy(llOriginal.KeyData[:], rand)
	encoded := llOriginal.Encode()
	var llRecovered SkyfileLayout
	llRecovered.Decode(encoded)
	if llOriginal != llRecovered {
		t.Fatal("encoding and decoding of skyfileLayout does not match")
	}
}

// TestSkyfileLayout_DecodeFanoutIntoChunks verifies the functionality of
// 'DecodeFanoutIntoChunks' on the SkyfileLayout object.
func TestSkyfileLayout_DecodeFanoutIntoChunks(t *testing.T) {
	t.Parallel()

	// no bytes
	sl := newTestSkyfileLayout()
	chunks, err := sl.DecodeFanoutIntoChunks(make([]byte, 0))
	if chunks != nil || err != nil {
		t.Fatal("unexpected")
	}

	// not even number of chunks
	fanoutBytes := fastrand.Bytes(crypto.HashSize + 1)
	_, err = sl.DecodeFanoutIntoChunks(fanoutBytes)
	if err == nil || !strings.Contains(err.Error(), "the fanout bytes do not contain an even number of chunks") {
		t.Fatal("unexpected")
	}

	// valid fanout bytes
	fanoutBytes = fastrand.Bytes(3 * crypto.HashSize)
	chunks, err = sl.DecodeFanoutIntoChunks(fanoutBytes)
	if err != nil {
		t.Fatal("unexpected")
	}
	if len(chunks) != 3 || len(chunks[0]) != 1 {
		t.Fatal("unexpected")
	}
	if !bytes.Equal(chunks[0][0][:], fanoutBytes[:crypto.HashSize]) {
		t.Fatal("unexpected")
	}

	// not 1-N
	sl.FanoutDataPieces = 4
	ppc := int(sl.FanoutDataPieces + sl.FanoutParityPieces) // pieces per chunk
	fanoutBytes = fastrand.Bytes(3 * ppc * crypto.HashSize) // 3 chunks
	chunks, err = sl.DecodeFanoutIntoChunks(fanoutBytes)
	if err != nil {
		t.Fatal("unexpected", err)
	}
	if len(chunks) != 3 || len(chunks[0]) != ppc {
		t.Fatal("unexpected")
	}
}

// TestSkyfileMetadata_ForPath tests the behaviour of the ForPath method.
func TestSkyfileMetadata_ForPath(t *testing.T) {
	filePath1 := "/foo/file1.txt"
	filePath2 := "/foo/file2.txt"
	filePath3 := "/file3.txt"
	filePath4 := "/bar/file4.txt"
	filePath5 := "/bar/baz/file5.txt"
	fullMeta := SkyfileMetadata{
		Subfiles: SkyfileSubfiles{
			filePath1: SkyfileSubfileMetadata{Filename: filePath1, Offset: 1, Len: 1},
			filePath2: SkyfileSubfileMetadata{Filename: filePath2, Offset: 2, Len: 2},
			filePath3: SkyfileSubfileMetadata{Filename: filePath3, Offset: 3, Len: 3},
			filePath4: SkyfileSubfileMetadata{Filename: filePath4, Offset: 4, Len: 4},
			filePath5: SkyfileSubfileMetadata{Filename: filePath5, Offset: 5, Len: 5},
		},
	}
	emptyMeta := SkyfileMetadata{}

	// Find an exact match.
	subMeta, isSubFile, offset, size := fullMeta.ForPath(filePath1)
	if _, exists := subMeta.Subfiles[filePath1]; !exists {
		t.Fatal("Expected to find a file by its full path and name.")
	}
	if !isSubFile {
		t.Fatal("Expected to find a file, got a dir.")
	}
	if offset != 1 {
		t.Fatalf("Expected offset %d, got %d", 1, offset)
	}
	if size != 1 {
		t.Fatalf("Expected size %d, got %d", 1, size)
	}

	// Find files by their directory.
	subMeta, isSubFile, offset, size = fullMeta.ForPath("/foo")
	subfile1, exists1 := subMeta.Subfiles[filePath1]
	subfile2, exists2 := subMeta.Subfiles[filePath2]
	// Expect to find files 1 and 2 and nothing else.
	if !(exists1 && exists2 && len(subMeta.Subfiles) == 2) {
		t.Fatal("Expected to find two files by their directory.")
	}
	if isSubFile {
		t.Fatal("Expected to find a dir, got a file.")
	}
	if offset != 1 {
		t.Fatalf("Expected offset %d, got %d", 1, offset)
	}
	if subfile1.Offset != 0 {
		t.Fatalf("Expected offset %d, got %d", 0, subfile1.Offset)
	}
	if subfile2.Offset != 1 {
		t.Fatalf("Expected offset %d, got %d", 1, subfile2.Offset)
	}

	if size != 3 {
		t.Fatalf("Expected size %d, got %d", 3, size)
	}

	// Find files in the given directory and its subdirectories.
	subMeta, isSubFile, offset, size = fullMeta.ForPath("/bar")
	subfile4, exists4 := subMeta.Subfiles[filePath4]
	subfile5, exists5 := subMeta.Subfiles[filePath5]
	// Expect to find files 4 and 5 and nothing else.
	if !(exists4 && exists5 && len(subMeta.Subfiles) == 2) {
		t.Fatal("Expected to find two files by their directory.")
	}
	if isSubFile {
		t.Fatal("Expected to find a dir, got a file.")
	}
	if offset != 4 {
		t.Fatalf("Expected offset %d, got %d", 4, offset)
	}
	if subfile4.Offset != 0 {
		t.Fatalf("Expected offset %d, got %d", 0, subfile4.Offset)
	}
	if subfile5.Offset != 1 {
		t.Fatalf("Expected offset %d, got %d", 1, subfile5.Offset)
	}
	if size != 9 {
		t.Fatalf("Expected size %d, got %d", 9, size)
	}

	// Find files in the given directory.
	subMeta, isSubFile, offset, size = fullMeta.ForPath("/bar/baz/")
	subfile5, exists5 = subMeta.Subfiles[filePath5]
	// Expect to find file 5 and nothing else.
	if !(exists5 && len(subMeta.Subfiles) == 1) {
		t.Fatal("Expected to find one file by its directory.")
	}
	if isSubFile {
		t.Fatal("Expected to find a dir, got a file.")
	}
	if offset != 5 {
		t.Fatalf("Expected offset %d, got %d", 5, offset)
	}
	if subfile5.Offset != 0 {
		t.Fatalf("Expected offset %d, got %d", 0, subfile4.Offset)
	}
	if size != 5 {
		t.Fatalf("Expected size %d, got %d", 5, size)
	}

	// Expect no files found on nonexistent path.
	for _, path := range []string{"/nonexistent", "/fo", "/file", "/b", "/bar/ba"} {
		subMeta, _, offset, size = fullMeta.ForPath(path)
		if len(subMeta.Subfiles) > 0 {
			t.Fatalf("Expected to not find any files on nonexistent path %s but found %v", path, len(subMeta.Subfiles))
		}
		if offset != 0 {
			t.Fatalf("Expected offset %d, got %d", 0, offset)
		}
		if size != 0 {
			t.Fatalf("Expected size %d, got %d", 0, size)
		}
	}

	// Find files by their directory, even if it contains a trailing slash.
	subMeta, _, _, _ = fullMeta.ForPath("/foo/")
	if _, exists := subMeta.Subfiles[filePath1]; !exists {
		t.Fatal(`Expected to find a file by its directory, even when followed by a "/".`)
	}
	subMeta, _, _, _ = fullMeta.ForPath("foo/")
	if _, exists := subMeta.Subfiles[filePath1]; !exists {
		t.Fatal(`Expected to find a file by its directory, even when missing leading "/" and followed by a "/".`)
	}

	// Find files by their directory, even if it's missing its leading slash.
	subMeta, _, _, _ = fullMeta.ForPath("foo")
	if _, exists := subMeta.Subfiles[filePath1]; !exists {
		t.Fatal(`Expected to find a file by its directory, even when it's missing its leading "/".`)
	}

	// Try to find a file in an empty metadata struct.
	subMeta, _, offset, _ = emptyMeta.ForPath("foo")
	if len(subMeta.Subfiles) != 0 {
		t.Fatal(`Expected to not find any files, found`, len(subMeta.Subfiles))
	}
	if offset != 0 {
		t.Fatal(`Expected offset to be zero, got`, offset)
	}
}

// TestSkyfileMetadata_IsDirectory is a table test for the IsDirectory method.
func TestSkyfileMetadata_IsDirectory(t *testing.T) {
	tests := []struct {
		name           string
		meta           SkyfileMetadata
		expectedResult bool
	}{
		{
			name: "nil subfiles",
			meta: SkyfileMetadata{
				Filename: "foo",
				Length:   10,
				Mode:     0644,
				Subfiles: nil,
			},
			expectedResult: false,
		},
		{
			name: "empty subfiles struct",
			meta: SkyfileMetadata{
				Filename: "foo",
				Length:   10,
				Mode:     0644,
				Subfiles: SkyfileSubfiles{},
			},
			expectedResult: false,
		},
		{
			name: "one subfile",
			meta: SkyfileMetadata{
				Filename: "foo",
				Length:   10,
				Mode:     0644,
				Subfiles: SkyfileSubfiles{
					"foo": SkyfileSubfileMetadata{
						FileMode:    10,
						Filename:    "foo",
						ContentType: "text/plain",
						Offset:      0,
						Len:         10,
					},
				},
			},
			expectedResult: false,
		},
		{
			name: "two subfiles",
			meta: SkyfileMetadata{
				Filename: "foo",
				Length:   20,
				Mode:     0644,
				Subfiles: SkyfileSubfiles{
					"foo": SkyfileSubfileMetadata{
						FileMode:    10,
						Filename:    "foo",
						ContentType: "text/plain",
						Offset:      0,
						Len:         10,
					},
					"bar": SkyfileSubfileMetadata{
						FileMode:    10,
						Filename:    "bar",
						ContentType: "text/plain",
						Offset:      10,
						Len:         10,
					},
				},
			},
			expectedResult: true,
		},
	}

	for _, test := range tests {
		result := test.meta.IsDirectory()
		if result != test.expectedResult {
			t.Fatalf("'%s' failed: expected '%t', got '%t'", test.name, test.expectedResult, result)
		}
	}
}

// TestComputeMonetizationPayout is a unit test for ComputeMonetizationPayout.
func TestComputeMonetizationPayout(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	tests := []struct {
		amt    uint64
		base   uint64
		n      uint64
		payout uint64
	}{
		{
			// Don't pay if amt is 0.
			amt:    0,
			base:   1,
			payout: 0,
		},
		{
			// Don't pay if base is 0.
			amt:    1,
			base:   0,
			payout: 0,
		},
		{
			// Pay if amt and base are the same.
			amt:    1,
			base:   1,
			payout: 1,
		},
		{
			// Pay if amt > base.
			amt:    2,
			base:   1,
			payout: 2,
		},
		{
			// 50% chance - drawing 49 should pass.
			amt:    1,
			base:   2,
			n:      49,
			payout: 2,
		},
		{
			// 50% chance - drawing 50 should fail.
			amt:    1,
			base:   2,
			n:      50,
			payout: 0,
		},
		{
			// 50% chance - drawing 51 should fail.
			amt:    1,
			base:   2,
			n:      51,
			payout: 0,
		},
		{
			// 99% chance - drawing 98 should pass.
			amt:    99,
			base:   100,
			n:      98,
			payout: 100,
		},
		{
			// 99% chance - drawing 99 should fail.
			amt:    99,
			base:   100,
			n:      99,
			payout: 0,
		},
		{
			// 99% chance - drawing 100 should pass.
			// 100 mod 100 == 0
			amt:    99,
			base:   100,
			n:      100,
			payout: 100,
		},
		{
			// 99% chance - drawing 1 should pass.
			amt:    99,
			base:   100,
			n:      1,
			payout: 100,
		},
	}

	for i, test := range tests {
		amt := types.NewCurrency64(test.amt)
		base := types.NewCurrency64(test.base)
		nBytes := types.NewCurrency64(test.n).Mul(monetizationLotteryPrecision).Div64(100).Big().Bytes()
		if len(nBytes) < monetizationLotteryEntropy {
			nBytes = append(make([]byte, monetizationLotteryEntropy-len(nBytes)), nBytes...)
		}
		p, err := ComputeMonetizationPayout(amt, base, bytes.NewReader(nBytes))
		if err != nil {
			t.Fatal(err)
		}
		payout, err := p.Uint64()
		if err != nil {
			t.Fatal(err)
		}
		if payout != test.payout {
			t.Log("amt", test.amt)
			t.Log("base", test.base)
			t.Log("n", test.n)
			t.Log("expected payout", test.payout)
			t.Log("actual pay", payout)
			t.Fatalf("Test %v failed", i)
		}
	}

	// Do a run with an actual random number generator and a 30% chance.
	payout := types.ZeroCurrency
	nTotal := uint64(500000)
	amt := types.NewCurrency64(30)
	base := types.NewCurrency64(100)
	for i := uint64(0); i < nTotal; i++ {
		p, err := ComputeMonetizationPayout(amt, base, fastrand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		payout = payout.Add(p)
	}

	// The payout should be off by less than 1%.
	idealPayout := types.NewCurrency64(nTotal).Mul(amt)
	tolerance := idealPayout.MulFloat(0.01)

	var diff types.Currency
	if idealPayout.Cmp(payout) > 0 {
		diff = idealPayout.Sub(payout)
	} else {
		diff = payout.Sub(idealPayout)
	}
	if diff.Cmp(tolerance) > 0 {
		t.Fatal("diff exceeds the 0.5% tolerance", diff, tolerance)
	}

	t.Log("Total executions:", nTotal)
	t.Log("Ideal payout:    ", idealPayout)
	t.Log("Actual payout:   ", payout)
}
