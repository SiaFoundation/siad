package host

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// fingerprintSize is the fixed fingerprint size in bytes
	fingerprintSize = 1 << 6

	// bucketSize defines the blockrange a bucket spans, buckets rotate every
	// 'bucketSize' blocks
	bucketSize = 20

	// filenames for fingerprint buckets
	currentBucketFilename = "fingerprints_current.txt"
	nextBucketFilename    = "fingerprints_next.txt"

	// bucketFlag specifies the flags to Openfile for fingerprint buckets, which
	// are created if they do not exist and are append only
	bucketFlag = os.O_RDWR | os.O_CREATE | os.O_APPEND
)

var (
	errKnownFingerprint         = errors.New("cannot re-use an ephemeral account withdrawal transaction")
	errExpiredFingerprint       = errors.New("ephemeral account withdrawal transaction expired, the current blockheight exceeds its expiry blockheight")
	errExtremeFutureFingerprint = errors.New("ephemeral account withdrawal transaction expires too far into the future")
)

type (
	// fingerprint (TODO)
	fingerprint struct {
		Hash   crypto.Hash       `json:"hash"`
		Expiry types.BlockHeight `json:"expiry"`
	}
)

// validateFingerprint returns an error if the fingerprint is either expired or
// it has been processed before
func (fp *fingerprint) validate(cbh types.BlockHeight) error {
	if fp.Expiry < cbh {
		return errExpiredFingerprint
	}

	if fp.Expiry > cbh+bucketSize {
		return errExtremeFutureFingerprint
	}

	return nil
}

type (
	// memoryBucket is a struct which holds two buckets which are accessed
	// through two pointers, current and next. When we rotate a bucket we simply
	// swap the pointers and recreate one of the buckets effectively pruning in
	// constant time
	memoryBucket struct {
		current      *map[crypto.Hash]struct{}
		next         *map[crypto.Hash]struct{}
		bucketOne    map[crypto.Hash]struct{}
		bucketTwo    map[crypto.Hash]struct{}
		bucketSize   uint64
		bucketHeight types.BlockHeight
	}
)

// newMemoryBucket will create a new in-memory bucket
func newMemoryBucket(s uint64, bh types.BlockHeight) *memoryBucket {
	mb := &memoryBucket{
		bucketOne:    make(map[crypto.Hash]struct{}),
		bucketTwo:    make(map[crypto.Hash]struct{}),
		bucketSize:   s,
		bucketHeight: bh + (bucketSize - (bh % bucketSize)),
	}
	mb.current = &mb.bucketOne
	mb.next = &mb.bucketTwo
	return mb
}

// save will add the given fingerprint to the appropriate bucket
func (mb *memoryBucket) save(fp *fingerprint) {
	if fp.Expiry <= mb.bucketHeight {
		(*mb.current)[fp.Hash] = struct{}{}
	} else {
		(*mb.next)[fp.Hash] = struct{}{}
	}
}

// has will return true when the fingerprint was present in either of the two
// buckets
func (mb *memoryBucket) has(fp *fingerprint) bool {
	_, exists := mb.bucketOne[fp.Hash]
	if !exists {
		_, exists = mb.bucketTwo[fp.Hash]
	}
	return exists
}

// tryRotate will rotate the bucket if necessary, depending on the current block
// height. It swaps the current and next pointer and recreate the map,
// effectively pruning an entire bucket in O(1)
func (mb *memoryBucket) tryRotate(bh types.BlockHeight) {
	if bh <= mb.bucketHeight {
		return
	}

	tmp := mb.current
	mb.current = mb.next
	mb.next = tmp

	*mb.next = make(map[crypto.Hash]struct{})
	mb.bucketHeight = types.BlockHeight(mb.bucketHeight + bucketSize)
}

type (
	// fileBucket is a helper struct that wraps both buckets and specifies the
	// threshold blockheight at which they need to rotate
	fileBucket struct {
		path         string
		current      modules.File
		next         modules.File
		bucketHeight types.BlockHeight
	}
)

// newFileBucket will create a new bucket for given size and blockheight
func newFileBucket(path string, fileA modules.File, fileB modules.File, cbh types.BlockHeight) *fileBucket {
	return &fileBucket{
		path:         path,
		current:      fileA,
		next:         fileB,
		bucketHeight: cbh + (bucketSize - (cbh % bucketSize)),
	}
}

// save will add the given fingerprint to the appropriate bucket
func (fb *fileBucket) save(fp *fingerprint) error {
	fpb := make([]byte, fingerprintSize)
	copy(fpb, encoding.Marshal(*fp))

	var err error
	if fp.Expiry <= fb.bucketHeight {
		_, err = fb.current.Write(fpb)
	} else {
		_, err = fb.next.Write(fpb)
	}

	return err
}

// all returns all fingerprints in the bucket
func (fb *fileBucket) all() ([]fingerprint, error) {
	bc, err := ioutil.ReadAll(fb.current)
	if err != nil {
		return nil, err
	}

	bn, err := ioutil.ReadAll(fb.next)
	if err != nil {
		return nil, err
	}

	fps := make([]fingerprint, 0)
	buf := append(bc, bn...)
	for i := headerOffset; i < len(buf); i += fingerprintSize {
		fp := fingerprint{}
		_ = encoding.Unmarshal(buf[i:i+fingerprintSize], &fp)
		fps = append(fps, fp)
	}

	return fps, nil
}

// tryRotate will rotate the bucket if necessary, depending on the current block
// height. It rotates by removing a bucket and renaming the other, after which
// it reops the next bucket
func (fb *fileBucket) tryRotate(bh types.BlockHeight) error {
	if bh <= fb.bucketHeight {
		return nil
	}

	fb.current.Close()
	err := os.Remove(filepath.Join(fb.path, currentBucketFilename))
	if err != nil {
		return err
	}

	err = os.Rename(filepath.Join(fb.path, nextBucketFilename), filepath.Join(fb.path, currentBucketFilename))
	if err != nil {
		return err
	}

	fb.current = fb.next
	fb.next, err = os.OpenFile(filepath.Join(fb.path, nextBucketFilename), bucketFlag, 0600)
	if err != nil {
		return err
	}

	fb.bucketHeight = types.BlockHeight(fb.bucketHeight + bucketSize)

	return nil
}
