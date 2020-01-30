package modules

import (
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"
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
					fileID:       "test2",
					sectorIndex:  0,
					sectorOffset: 0,
				},
				{
					fileID:       "test3",
					sectorIndex:  0,
					sectorOffset: 20 * kib,
				},
				{
					fileID:       "test1",
					sectorIndex:  0,
					sectorOffset: 36 * kib,
				},
			},
			err: nil,
		},
		{
			in: map[string]uint64{
				"test1": 1,
				"test2": 0,
				"test3": 1,
			},
			out: nil,
			err: ErrZeroSize,
		},
		{
			in: map[string]uint64{
				"test1": 3 * mib,
				"test2": 4 * mib,
				"test3": 1 * mib,
				"test4": 2 * mib,
				"test5": 2000 * kib,
				"test6": 1,
				"test7": 2,
			},
			out: []FilePlacement{
				{
					fileID:       "test2",
					sectorIndex:  0,
					sectorOffset: 0,
				},
				{
					fileID:       "test1",
					sectorIndex:  1,
					sectorOffset: 0,
				},
				{
					fileID:       "test4",
					sectorIndex:  2,
					sectorOffset: 0,
				},
				{
					fileID:       "test5",
					sectorIndex:  2,
					sectorOffset: 2 * mib,
				},
				{
					fileID:       "test3",
					sectorIndex:  1,
					sectorOffset: 3 * mib,
				},
				{
					fileID:       "test7",
					sectorIndex:  2,
					sectorOffset: 2*mib + 2000*kib,
				},
				{
					fileID:       "test6",
					sectorIndex:  2,
					sectorOffset: 2*mib + 2004*kib,
				},
			},
		},
	}

	for _, test := range tests {
		res, err := PackFiles(test.in)
		if !reflect.DeepEqual(res, test.out) || err != test.err {
			t.Errorf("PackFiles(%v): expected %v %v, got %v %v", test.in, test.out, test.err, res, err)
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

	numFiles := 5_000
	files := make(map[string]uint64, numFiles)

	// Construct a map of random files and sizes.
	for i := 0; i < numFiles; i++ {
		name := string(fastrand.Bytes(16))
		size := fastrand.Uint64n(SectorSize) + 1 // Prevent values of 0.
		files[name] = size
	}

	placements, err := PackFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != numFiles {
		t.Errorf("expected %v placements, got %v", numFiles, len(placements))
	}

	// Check that all alignments are correct.
	for _, placement := range placements {
		size := files[placement.fileID]
		requiredAlignment, err := requiredAlignment(size)
		if err != nil {
			t.Fatal(err)
		}

		if placement.sectorOffset%requiredAlignment != 0 {
			t.Errorf("invalid alignment for file size %v", size)
		}
	}
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
			err:        nil,
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
		{fileSize: 16 * kib, sectorOffset: 5 * kib, out: 3 * kib, err: nil},
		{fileSize: 16 * kib, sectorOffset: 8 * kib, out: 0 * kib, err: nil},
		{fileSize: 32*kib + 1, sectorOffset: 0, out: 0, err: nil},
		{fileSize: 32*kib + 1, sectorOffset: 8*kib + 1, out: 8*kib - 1, err: nil},
		{fileSize: 16 * mib, sectorOffset: 5 * mib, out: 0, err: ErrSizeTooLarge},
	}

	for _, test := range tests {
		res, err := alignFileInBucket(test.fileSize, test.sectorOffset)
		if res != test.out || err != test.err {
			t.Errorf("AlignFileInBucket(%v, %v): expected %v %v, got %v %v", test.fileSize, test.sectorOffset, test.out, test.err, res, err)
		}
	}
}
