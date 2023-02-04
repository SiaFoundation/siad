package modules

import (
	"sort"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
)

var (
	// ErrSizeTooLarge is returned for file sizes that exceed the sector size.
	ErrSizeTooLarge = errors.New("file size exceeds sector size")
	// ErrZeroSize is returned for zero-length files.
	ErrZeroSize = errors.New("file size of zero")

	// errBucketNotFound is returned when no applicable bucket exists.
	errBucketNotFound = errors.New("no bucket was found")

	// alignmentScaling scales the alignments with respect to SectorSize.
	//
	// Without this scaling, we could end up with alignments that don't fit in a
	// sector, especially in Testing builds where small sectors are used.
	alignmentScaling = build.Select(build.Var{
		Dev:      uint64(1 << (alignmentScalingStandard - (SectorSizeScalingStandard - SectorSizeScalingDev))),
		Standard: uint64(1 << alignmentScalingStandard), // 1 KiB
		Testnet:  uint64(1 << alignmentScalingStandard), // 1 KiB
		Testing:  uint64(1 << (alignmentScalingStandard - (SectorSizeScalingStandard - SectorSizeScalingTesting))),
	}).(uint64)
	alignmentScalingStandard = 10
)

type (
	// FilePlacement contains the sector of a file and its offset in the sector.
	FilePlacement struct {
		FileID       string
		Size         uint64
		SectorIndex  uint64
		SectorOffset uint64
	}

	// bucket defines a temporary bucket used when packing files.
	bucket struct {
		sectorIndex  uint64
		sectorOffset uint64
		length       uint64
	}

	// bucketList is a list of buckets.
	bucketList []*bucket

	// fileList is a list of packing files.
	fileList []packingFile

	// packingFile contains the minimum amount of information to track a packed
	// file.
	packingFile struct {
		id   string
		size uint64
	}
)

// PackFiles packs files, given as a map (id => size), into sectors in an
// efficient manner. Returns a FilePlacement slice and the number of sectors.
//
// 1. Sort the files by size in descending order.
//
// 2. Going from larger to smaller files, try to fit each file into an available
// bucket in a sector.
//
//	a. The first of the largest available buckets should be chosen.
//
//	b. The first byte of the file must be aligned to a certain multiple of KiB,
//	based on its size.
//
//	  i. For a file size up to 32*2^n KiB, the file must align to 4*2^n KiB,
//	  for 0 <= n <= 7.
//
//	  ii. Alignment is based on the start of the sector, not the bucket.
//
//	  iii. Alignment may cause a file not to fit into an otherwise large-enough
//	  bucket.
//
//	c. If there are no suitable buckets, create a new sector and a new bucket
//	in that sector that fills the whole sector.
//
// 3. Pack the file into the bucket at the correct alignment.
//
//	a. Delete the bucket and make up to 2 new buckets. The new buckets, if any,
//	should stay ordered with regards to their positions in the sectors:
//
//	  i. If the file could not align to the start of the bucket, make a new
//	  bucket from the start of the old bucket to the start of the file.
//
//	  ii. If the file does not go to the end of the bucket, make a new bucket
//	  that goes from the end of the file to the end of the old bucket.
//
// 4. Return the slice of file placements in the order that they were packed
// chronologically. Note that they may be out of order positionally, as smaller
// files may be packed in lower offsets than larger files despite appearing
// later in the slice.
func PackFiles(files map[string]uint64) ([]FilePlacement, uint64, error) {
	filesSorted := sortByFileSizeDescending(files)

	// We can end up with a maximum of 2 buckets created for every file packed,
	// so set the capacity accordingly.
	buckets := bucketList(make([]*bucket, 0, 2*len(files)))
	filePlacements := make([]FilePlacement, 0, len(files))

	var numSectors uint64 = 0
	for _, file := range filesSorted {
		// Make sure the file fits in a sector.
		if file.size > SectorSize {
			return nil, 0, ErrSizeTooLarge
		}
		// Zero-sized files are a pathological case and shouldn't be allowed.
		if file.size == 0 {
			return nil, 0, ErrZeroSize
		}

		bucketIndex, err := findBucket(file.size, buckets)
		if errors.Contains(err, errBucketNotFound) {
			// Create a new sector and bucket. We have already ensured above
			// that the file will fit into a sector.
			buckets, numSectors = extendSectors(buckets, numSectors)
			bucketIndex = len(buckets) - 1
		} else if err != nil {
			return nil, 0, err
		}

		var filePlacement FilePlacement
		filePlacement, buckets, err = packBucket(file, bucketIndex, buckets)
		if err != nil {
			return nil, 0, err
		}
		filePlacements = append(filePlacements, filePlacement)
	}

	return filePlacements, numSectors, nil
}

// findBucket selects the most appropriate bucket for the file and returns the
// index of the bucket.
//
// Return an error if no valid bucket was found.
func findBucket(fileSize uint64, buckets bucketList) (int, error) {
	var currentBucket *bucket = nil
	currentBucketIndex := -1

	// Find the largest bucket that the file fits into, and return the first of
	// them.
	for i, bucket := range buckets {
		// If no bucket has been found yet, accept a bucket at least as big as
		// the file size, otherwise only accept a bucket bigger than the current
		// bucket.
		if !(currentBucket == nil && bucket.length >= fileSize ||
			currentBucket != nil && bucket.length > currentBucket.length) {
			continue
		}

		// Try to find an alignment for the file in the bucket.
		alignment, err := alignFileInBucket(fileSize, bucket.sectorOffset)
		if err != nil {
			return 0, err
		}

		// Check that the file still fits into the bucket after alignment.
		if bucket.length-alignment >= fileSize {
			currentBucket = bucket
			currentBucketIndex = i
		}
	}

	if currentBucket != nil {
		return currentBucketIndex, nil
	}

	// No bucket found.
	return 0, errBucketNotFound
}

// extendSectors creates a new sector and adds a new bucket to the list of
// buckets that fills the sector.
func extendSectors(buckets bucketList, numSectors uint64) (bucketList, uint64) {
	return append(buckets, &bucket{
		sectorIndex:  numSectors,
		sectorOffset: 0,
		length:       SectorSize,
	}), numSectors + 1
}

// requiredAlignment returns the byte alignment from the start of a sector that
// the file must start at, based on the size of the file.
func requiredAlignment(fileSize uint64) (uint64, error) {
	// NOTE: We need to scale the required alignments the same way we scale the
	// SectorSize, so that the alignments actually fit inside sectors in Dev and
	// Testing builds.
	for n := 0; n < 8; n++ {
		if fileSize <= 32*(1<<n)*alignmentScaling {
			return 4 * (1 << n) * alignmentScaling, nil
		}
	}

	return 0, ErrSizeTooLarge
}

// alignFileInBucket returns the offset in the bucket that the file aligns to.
func alignFileInBucket(fileSize uint64, sectorOffset uint64) (uint64, error) {
	requiredAlignment, err := requiredAlignment(fileSize)
	if err != nil {
		return 0, err
	}

	alignmentInSector := sectorOffset
	if sectorOffset%requiredAlignment != 0 {
		alignmentInSector = sectorOffset - (sectorOffset % requiredAlignment) + requiredAlignment
	}
	alignmentInBucket := alignmentInSector - sectorOffset
	return alignmentInBucket, nil
}

// packBucket packs the file into the bucket at the correct alignment, replacing
// it with up to 2 new buckets.
func packBucket(file packingFile, bucketIndex int, buckets bucketList) (FilePlacement, bucketList, error) {
	oldBucket := buckets[bucketIndex]
	sectorIndex := oldBucket.sectorIndex
	sectorOffset := oldBucket.sectorOffset

	// Delete the bucket.
	buckets = append(buckets[:bucketIndex], buckets[bucketIndex+1:]...)

	// bucketAlignment is the alignment of the file from the start of the old
	// bucket.
	bucketAlignment, err := alignFileInBucket(file.size, sectorOffset)
	if err != nil {
		return FilePlacement{}, buckets, err
	}

	// bucketBeforeLength is the space from the start of the old bucket to the
	// start of the file.
	bucketBeforeLength := bucketAlignment
	bucketIndex, buckets = createNewBucket(sectorIndex, sectorOffset, bucketBeforeLength, bucketIndex, buckets)

	// bucketAfterLength is the space still available in the old bucket once the
	// file and its alignment are subtracted away.
	bucketAfterLength := oldBucket.length - file.size - bucketAlignment
	bucketAfterSectorOffset := sectorOffset + bucketAlignment + file.size
	_, buckets = createNewBucket(sectorIndex, bucketAfterSectorOffset, bucketAfterLength, bucketIndex, buckets)

	filePlacement := FilePlacement{
		FileID:       file.id,
		Size:         file.size,
		SectorIndex:  sectorIndex,
		SectorOffset: sectorOffset + bucketAlignment,
	}
	return filePlacement, buckets, nil
}

// createNewBucket will actually create a new bucket and add it to the bucket
// list.
func createNewBucket(sectorIndex, sectorOffset, length uint64, bucketIndex int, buckets bucketList) (int, bucketList) {
	if length == 0 {
		return bucketIndex, buckets
	}

	// If it's impossible for *any* file to fit into this bucket, due to the
	// minimum alignment from the start of the bucket landing outside the
	// bucket, do not bother adding the bucket. This will result in less buckets
	// to search through later.
	minimumAlignment, _ := alignFileInBucket(1, sectorOffset)
	if minimumAlignment >= length {
		return bucketIndex, buckets
	}

	newBucket := bucket{
		sectorIndex:  sectorIndex,
		sectorOffset: sectorOffset + minimumAlignment,
		length:       length - minimumAlignment,
	}
	buckets = insertBucket(buckets, newBucket, bucketIndex)
	// Increment the bucket index in case we have to insert another bucket after
	// this one.
	bucketIndex++

	return bucketIndex, buckets
}

// insertBucket inserts a bucket into a slice of buckets.
//
// A modified version of `insert` from
// https://github.com/golang/go/wiki/SliceTricks. I chose the longer version for
// better performance.
func insertBucket(buckets bucketList, b bucket, i int) bucketList {
	buckets = append(buckets, &bucket{})
	copy(buckets[i+1:], buckets[i:])
	buckets[i] = &b
	return buckets
}

// Sorting.

// sortByFileSizeDescending reverses sorts a map by value.
// Function from StackOverflow.
func sortByFileSizeDescending(idToSizeMap map[string]uint64) fileList {
	pl := make(fileList, len(idToSizeMap))
	i := 0

	for k, v := range idToSizeMap {
		pl[i] = packingFile{k, v}
		i++
	}
	sort.Sort(sort.Reverse(pl))

	return pl
}

func (p fileList) Len() int           { return len(p) }
func (p fileList) Less(i, j int) bool { return p[i].size < p[j].size }
func (p fileList) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
