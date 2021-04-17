package modules

import (
	"fmt"
	"os"
	"reflect"
	"testing"
	"text/tabwriter"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/build"
)

const (
	// kib is a kilobyte multiplier.
	kib uint64 = 1 << 10
	// mib is a megabyte multiplier.
	mib uint64 = 1 << 20
)

// TestPackFiles tests that files are packed in the correct order.
func TestPackFiles(t *testing.T) {
	// Test using the production sector size.
	SectorSize = SectorSizeStandard
	// Change the scaling as well.
	alignmentScaling = uint64(1 << alignmentScalingStandard)

	tests := []struct {
		in  map[string]uint64
		out []FilePlacement
		num uint64
		err error
	}{
		{
			in: map[string]uint64{
				"test1": 10 * kib,
				"test2": 20 * kib,
				"test3": 15 * kib,
			},
			out: []FilePlacement{
				{
					FileID:       "test2",
					Size:         20 * kib,
					SectorIndex:  0,
					SectorOffset: 0,
				},
				{
					FileID:       "test3",
					Size:         15 * kib,
					SectorIndex:  0,
					SectorOffset: 20 * kib,
				},
				{
					FileID:       "test1",
					Size:         10 * kib,
					SectorIndex:  0,
					SectorOffset: 36 * kib,
				},
			},
			num: 1,
		},
		// Test case to ensure that the smallest file, despite being packed
		// last, does not have the highest offset of all the files if there are
		// small previous buckets available.
		//
		// NOTE: The packer picks the first of the *largest* available buckets,
		// so we must first fill up the whole sector before smaller previous
		// buckets can be considered.
		{
			in: map[string]uint64{
				"test1": 100,
				"test2": 2*mib + 499*kib,
				"test3": 1*mib + 499*kib,
			},
			out: []FilePlacement{
				{
					FileID:       "test2",
					Size:         2*mib + 499*kib,
					SectorIndex:  0,
					SectorOffset: 0 * kib,
				},
				{
					FileID:       "test3",
					Size:         1*mib + 499*kib,
					SectorIndex:  0,
					SectorOffset: 2*mib + 512*kib,
				},
				{
					FileID:       "test1",
					Size:         100,
					SectorIndex:  0,
					SectorOffset: 2*mib + 500*kib,
				},
			},
			num: 1,
		},
		{
			in: map[string]uint64{
				"test1": 1,
				"test2": 0,
				"test3": 1,
			},
			num: 0,
			err: ErrZeroSize,
		},
		{
			in: map[string]uint64{
				"test1": 3 * mib,
				"test2": 4 * mib,
				"test3": 1 * mib,
				"test4": 2 * mib,
				"test5": 2e3 * kib,
				"test6": 1,
				"test7": 2,
			},
			out: []FilePlacement{
				{
					FileID:       "test2",
					Size:         4 * mib,
					SectorIndex:  0,
					SectorOffset: 0,
				},
				{
					FileID:       "test1",
					Size:         3 * mib,
					SectorIndex:  1,
					SectorOffset: 0,
				},
				{
					FileID:       "test4",
					Size:         2 * mib,
					SectorIndex:  2,
					SectorOffset: 0,
				},
				{
					FileID:       "test5",
					Size:         2e3 * kib,
					SectorIndex:  2,
					SectorOffset: 2 * mib,
				},
				{
					FileID:       "test3",
					Size:         1 * mib,
					SectorIndex:  1,
					SectorOffset: 3 * mib,
				},
				{
					FileID:       "test7",
					Size:         2,
					SectorIndex:  2,
					SectorOffset: 2*mib + 2e3*kib,
				},
				{
					FileID:       "test6",
					Size:         1,
					SectorIndex:  2,
					SectorOffset: 2*mib + 2_004*kib,
				},
			},
			num: 3,
		},
	}

	for _, test := range tests {
		res, num, err := PackFiles(test.in)
		if !reflect.DeepEqual(res, test.out) || num != test.num || err != test.err {
			t.Errorf("PackFiles(%v): expected %v %v %v, got %v %v %v", test.in, test.out, test.num, test.err, res, num, err)
		}
	}
}

// TestPackFilesRandom tries packing a large amount of random files not bigger
// than the sector size and makes sure no errors are returned.
func TestPackFilesRandom(t *testing.T) {
	// Test using the production sector size.
	SectorSize = SectorSizeStandard
	// Change the scaling as well.
	alignmentScaling = uint64(1 << alignmentScalingStandard)

	numFiles := int(5e3)

	files := randomFileMap(numFiles)

	placements, _, err := PackFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != numFiles {
		t.Errorf("expected %v placements, got %v", numFiles, len(placements))
	}

	// Check that all alignments are correct.
	for _, p := range placements {
		size := p.Size
		requiredAlignment, err := requiredAlignment(size)
		if err != nil {
			t.Fatal(err)
		}

		i, j := p.SectorOffset, p.SectorOffset+size-1
		if i%requiredAlignment != 0 {
			t.Errorf("invalid alignment for file size %v", size)
		}
		if j > SectorSize {
			t.Errorf("placement outside sector: (%v, %v)", i, j)
		}
	}

	// Check that there are no overlapping files.
	for i, p1 := range placements {
		for _, p2 := range placements[i+1:] {
			s1, s2 := p1.SectorIndex, p2.SectorIndex
			if s1 != s2 {
				continue
			}

			i1, i2 := p1.SectorOffset, p1.SectorOffset+p1.Size-1
			j1, j2 := p2.SectorOffset, p2.SectorOffset+p2.Size-1
			if overlaps(i1, i2, j1, j2) {
				t.Errorf("overlapping files at sector%v:(%v, %v) and sector%v:(%v, %v)", s1, i1, i2, s2, j1, j2)
			}
		}
	}
}

func overlaps(i1, i2, j1, j2 uint64) bool {
	return i1 <= j2 && j1 <= i2
}

func benchmarkPackFiles(i int, b *testing.B) {
	for n := 0; n <= b.N; n++ {
		files := randomFileMap(i)
		_, _, err := PackFiles(files)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchPackFilesRandom benchmarks packing 1k random files.
func BenchmarkPackFiles1000(b *testing.B) { benchmarkPackFiles(1e3, b) }

// BenchPackFilesRandom benchmarks packing 10k random files.
func BenchmarkPackFiles10000(b *testing.B) { benchmarkPackFiles(10e3, b) }

// BenchPackFilesRandom benchmarks packing 100k random files.
func BenchmarkPackFiles100000(b *testing.B) { benchmarkPackFiles(100e3, b) }

// TestPackingUtilization generates a report of average % utilization (space
// used / total space * 100) for large numbers of input files, randomly and
// uniformly-distributed in size.
func TestPackingUtilization(t *testing.T) {
	if testing.Short() || !build.VLONG {
		t.SkipNow()
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	iterations := 5
	fileAmounts := []int{10, 100, 1e3, 2e3, 5e3, 10e3, 20e3}

	fmt.Fprintln(w, "TestPackingUtilization Report:")
	fmt.Fprintln(w, "------- \t -------------- \t ------------------")
	fmt.Fprintln(w, "# Files \t Avg. # Sectors \t Avg. % Utilization")
	fmt.Fprintln(w, "------- \t -------------- \t ------------------")

	for _, amount := range fileAmounts {
		sumSectors := float64(0)
		sumUtil := float64(0)

		for i := 0; i < iterations; i++ {
			files := randomFileMap(amount)
			placements, numSectors, err := PackFiles(files)
			if err != nil {
				t.Errorf("Error: %v", err)
			}

			totalSpace := uint64(0)
			for _, p := range placements {
				totalSpace += p.Size
			}
			utilization := float64(totalSpace) / float64(numSectors*SectorSize)
			sumUtil += utilization
			sumSectors += float64(numSectors)
		}

		avgSectors := sumSectors / float64(iterations)
		avgUtil := sumUtil / float64(iterations)
		fmt.Fprintln(w, amount, "\t", avgSectors, "\t", avgUtil*100)
	}

	if err := w.Flush(); err != nil {
		t.Error("failed to flush writer")
	}
}

// randomFileMap generates a map of random files for testing purposes.
func randomFileMap(numFiles int) map[string]uint64 {
	files := make(map[string]uint64, numFiles)

	// Construct a map of random files and sizes.
	for i := 0; i < numFiles; i++ {
		name := string(fastrand.Bytes(16))
		size := fastrand.Uint64n(SectorSize) + 1 // Prevent values of 0.
		files[name] = size
	}

	return files
}

// TestSortByFileSizeDescending tests that sorting a map by descending values
// works correctly.
func TestSortByFileSizeDescending(t *testing.T) {
	tests := []struct {
		in  map[string]uint64
		out fileList
	}{
		{
			in: map[string]uint64{
				"test1": 10,
				"test2": 20,
				"test3": 15,
			},
			out: fileList{
				packingFile{
					"test2", 20,
				},
				packingFile{
					"test3", 15,
				},
				packingFile{
					"test1", 10,
				},
			},
		},
	}

	for _, test := range tests {
		res := sortByFileSizeDescending(test.in)
		if !reflect.DeepEqual(res, test.out) {
			t.Errorf("SortByFileSizeDescending(%v): expected %v, got %v", test.in, test.out, res)
		}
	}
}

// TestFindBucket tests that the correct bucket is chosen given a file size and
// a list of buckets.
func TestFindBucket(t *testing.T) {
	tests := []struct {
		fileSize   uint64
		buckets    bucketList
		numSectors uint64
		out        int
		err        error
	}{
		{
			fileSize:   32,
			buckets:    bucketList{&bucket{0, 0, SectorSize}},
			numSectors: 0,
			out:        0,
		},
		{
			// Huge file, should error.
			fileSize:   100 * mib,
			buckets:    bucketList{&bucket{0, 0, SectorSize}},
			numSectors: 0,
			out:        0,
			err:        errBucketNotFound,
		},
	}

	for _, test := range tests {
		res, err := findBucket(test.fileSize, test.buckets)
		if res != test.out || err != test.err {
			t.Errorf("findBucket(%v, %v, %v): expected %v %v, got %v %v", test.fileSize, test.buckets, test.numSectors, test.out, test.err, res, err)
		}
	}
}

// TestRequiredAlignment tests that the correct alignment is chosen based on the
// size of the file.
func TestRequiredAlignment(t *testing.T) {
	// Test using the production sector size.
	SectorSize = SectorSizeStandard
	// Change the scaling as well.
	alignmentScaling = uint64(1 << alignmentScalingStandard)

	tests := []struct {
		fileSize, out uint64
		err           error
	}{
		{32 * kib, 4 * kib, nil},
		{32*kib + 1, 8 * kib, nil},
		{32 * mib, 0, ErrSizeTooLarge},
	}

	for _, test := range tests {
		res, err := requiredAlignment(test.fileSize)
		if res != test.out || err != test.err {
			t.Errorf("requiredAlignment(%v): expected %v %v, got %v %v", test.fileSize, test.out, test.err, res, err)
		}
	}
}

// TestAlignFileInBucket tests that the correct alignment in buckets is chosen.
func TestAlignFileInBucket(t *testing.T) {
	// Test using the production sector size.
	SectorSize = SectorSizeStandard
	// Change the scaling as well.
	alignmentScaling = uint64(1 << alignmentScalingStandard)

	tests := []struct {
		fileSize, sectorOffset, out uint64
		err                         error
	}{
		{fileSize: 16 * kib, sectorOffset: 5 * kib, out: 3 * kib},
		{fileSize: 16 * kib, sectorOffset: 8 * kib, out: 0 * kib},
		{fileSize: 32*kib + 1, sectorOffset: 0, out: 0},
		{fileSize: 32*kib + 1, sectorOffset: 8*kib + 1, out: 8*kib - 1},
		{fileSize: 16 * mib, sectorOffset: 5 * mib, err: ErrSizeTooLarge},
	}

	for _, test := range tests {
		res, err := alignFileInBucket(test.fileSize, test.sectorOffset)
		if res != test.out || err != test.err {
			t.Errorf("AlignFileInBucket(%v, %v): expected %v %v, got %v %v", test.fileSize, test.sectorOffset, test.out, test.err, res, err)
		}
	}
}
