package crypto

import (
	"bytes"

	"gitlab.com/NebulousLabs/merkletree/merkletree-blake"

	"gitlab.com/NebulousLabs/Sia/encoding"
)

const (
	// SegmentSize is the chunk size that is used when taking the Merkle root
	// of a file. 64 is chosen because bandwidth is scarce and it optimizes for
	// the smallest possible storage proofs. Using a larger base, even 256
	// bytes, would result in substantially faster hashing, but the bandwidth
	// tradeoff was deemed to be more important, as blockchain space is scarce.
	SegmentSize = 64
)

// MerkleTree wraps merkletree.Tree, changing some of the function definitions
// to assume sia-specific constants and return sia-specific types.
type MerkleTree struct {
	merkletree.Tree
}

// NewTree returns a MerkleTree, which can be used for getting Merkle roots and
// Merkle proofs on data. See merkletree.Tree for more details.
func NewTree() *MerkleTree {
	return &MerkleTree{*merkletree.New()}
}

// PushObject encodes and adds the hash of the encoded object to the tree as a
// leaf.
func (t *MerkleTree) PushObject(obj interface{}) {
	t.Push(encoding.Marshal(obj))
}

// Root is a redefinition of merkletree.Tree.Root, returning a Hash instead of
// a []byte.
func (t *MerkleTree) Root() (h Hash) {
	return Hash(t.Tree.Root())
}

// CachedMerkleTree wraps merkletree.CachedTree, changing some of the function
// definitions to assume sia-specific constants and return sia-specific types.
type CachedMerkleTree struct {
	merkletree.CachedTree
}

// NewCachedTree returns a CachedMerkleTree, which can be used for getting
// Merkle roots and proofs from data that has cached subroots. See
// merkletree.CachedTree for more details.
func NewCachedTree(height uint64) *CachedMerkleTree {
	return &CachedMerkleTree{*merkletree.NewCachedTree(height)}
}

// Prove is a redefinition of merkletree.CachedTree.Prove, so that Sia-specific
// types are used instead of the generic types used by the parent package. The
// base is not a return value because the base is used as input.
func (ct *CachedMerkleTree) Prove(base []byte, cachedHashSet []Hash) []Hash {
	// Turn the input in to a proof set that will be recognized by the high
	// level tree.
	cachedProofSet := make([][32]byte, len(cachedHashSet)+1)
	cachedProofSet[0] = [32]byte(HashBytes(base))
	for i := range cachedHashSet {
		cachedProofSet[i+1] = cachedHashSet[i]
	}
	_, proofSet, _, _ := ct.CachedTree.Prove(cachedProofSet)

	// convert proofSet to base and hashSet
	hashSet := make([]Hash, len(proofSet)-1)
	for i, proof := range proofSet[1:] {
		copy(hashSet[i][:], proof[:])
	}
	return hashSet
}

// Push is a redefinition of merkletree.CachedTree.Push, with the added type
// safety of only accepting a hash.
func (ct *CachedMerkleTree) Push(h Hash) {
	ct.CachedTree.Push(h[:])
}

// PushSubTree is a redefinition of merkletree.CachedTree.PushSubTree, with the
// added type safety of only accepting a hash.
func (ct *CachedMerkleTree) PushSubTree(height int, h Hash) error {
	return ct.CachedTree.PushSubTree(height, h)
}

// Root is a redefinition of merkletree.CachedTree.Root, returning a Hash
// instead of a []byte.
func (ct *CachedMerkleTree) Root() (h Hash) {
	return Hash(ct.CachedTree.Root())
}

// CalculateLeaves calculates the number of leaves that would be pushed from
// data of size 'dataSize'.
func CalculateLeaves(dataSize uint64) uint64 {
	numSegments := dataSize / SegmentSize
	if dataSize == 0 || dataSize%SegmentSize != 0 {
		numSegments++
	}
	return numSegments
}

// MerkleRoot returns the Merkle root of the input data.
func MerkleRoot(b []byte) Hash {
	t := NewTree()
	buf := bytes.NewBuffer(b)
	for buf.Len() > 0 {
		t.Push(buf.Next(SegmentSize))
	}
	return t.Root()
}

// MerkleProof builds a Merkle proof that the data at segment 'proofIndex' is a
// part of the Merkle root formed by 'b'.
//
// MerkleProof is NOT equivalent to MerkleRangeProof for a single segment.
func MerkleProof(b []byte, proofIndex uint64) (base []byte, hashSet []Hash) {
	// Create the tree.
	t := NewTree()
	t.SetIndex(proofIndex)

	// Fill the tree.
	buf := bytes.NewBuffer(b)
	for buf.Len() > 0 {
		t.Push(buf.Next(SegmentSize))
	}

	// Get the proof and convert it to a base + hash set.
	// _, base, proof, _, _ := t.Prove()
	root, base, proof, proofIndex, numLeaves := t.Prove()
	if len(proof) == 0 {
		// There's no proof, because there's no data. Return blank values.
		return nil, nil
	}

	if !merkletree.VerifyProof(root, proof, proofIndex, numLeaves) {
		panic("mismatch")
	}

	proof = proof[1:]
	hashSet = make([]Hash, len(proof))
	for i, p := range proof {
		hashSet[i] = Hash(p)
	}
	return base, hashSet
}

// VerifySegment will verify that a segment, given the proof, is a part of a
// Merkle root.
//
// VerifySegment is NOT equivalent to VerifyRangeProof for a single segment.
func VerifySegment(base []byte, hashSet []Hash, numSegments, proofIndex uint64, root Hash) bool {
	// convert base and hashSet to proofSet
	proofSet := make([][32]byte, len(hashSet)+1)
	proofSet[0] = merkletree.LeafSum(base)
	for i := range hashSet {
		proofSet[i+1] = [32]byte(hashSet[i])
	}
	return merkletree.VerifyProof(root, proofSet, proofIndex, numSegments)
}

// MerkleRangeProof builds a Merkle proof for the segment range [start,end).
//
// MerkleRangeProof for a single segment is NOT equivalent to MerkleProof.
func MerkleRangeProof(b []byte, start, end int) []Hash {
	proof, _ := merkletree.BuildRangeProof(start, end, merkletree.NewReaderSubtreeHasher(bytes.NewReader(b), SegmentSize))
	proofHashes := make([]Hash, len(proof))
	for i := range proofHashes {
		proofHashes[i] = Hash(proof[i])
	}
	return proofHashes
}

// VerifyRangeProof verifies a proof produced by MerkleRangeProof.
//
// VerifyRangeProof for a single segment is NOT equivalent to VerifySegment.
func VerifyRangeProof(segments []byte, proof []Hash, start, end int, root Hash) bool {
	proofBytes := make([][32]byte, len(proof))
	for i := range proof {
		proofBytes[i] = [32]byte(proof[i])
	}
	result, _ := merkletree.VerifyRangeProof(merkletree.NewReaderLeafHasher(bytes.NewReader(segments), SegmentSize), start, end, proofBytes, [32]byte(root))
	return result
}

// MerkleSectorRangeProof builds a Merkle proof for the sector range [start,end).
func MerkleSectorRangeProof(roots []Hash, start, end int) []Hash {
	leafHashes := make([][32]byte, len(roots))
	for i := range leafHashes {
		leafHashes[i] = [32]byte(roots[i])
	}
	sh := merkletree.NewCachedSubtreeHasher(leafHashes)
	proof, _ := merkletree.BuildRangeProof(start, end, sh)
	proofHashes := make([]Hash, len(proof))
	for i := range proofHashes {
		proofHashes[i] = Hash(proof[i])
	}
	return proofHashes
}

// VerifySectorRangeProof verifies a proof produced by MerkleSectorRangeProof.
func VerifySectorRangeProof(roots []Hash, proof []Hash, start, end int, root Hash) bool {
	leafHashes := make([][32]byte, len(roots))
	for i := range leafHashes {
		leafHashes[i] = [32]byte(roots[i])
	}
	lh := merkletree.NewCachedLeafHasher(leafHashes)
	proofBytes := make([][32]byte, len(proof))
	for i := range proof {
		proofBytes[i] = [32]byte(proof[i])
	}
	result, _ := merkletree.VerifyRangeProof(lh, start, end, proofBytes, [32]byte(root))
	return result
}

// A ProofRange is a contiguous range of segments or sectors.
type ProofRange = merkletree.LeafRange

// MerkleDiffProof builds a Merkle proof for multiple segment ranges.
func MerkleDiffProof(ranges []ProofRange, numLeaves uint64, updatedSectors [][]byte, sectorRoots []Hash) []Hash {
	leafHashes := make([][32]byte, len(sectorRoots))
	for i := range leafHashes {
		leafHashes[i] = [32]byte(sectorRoots[i])
	}
	sh := merkletree.NewCachedSubtreeHasher(leafHashes) // TODO: needs to include updatedSectors somehow
	proof, _ := merkletree.BuildDiffProof(ranges, sh, numLeaves)
	proofHashes := make([]Hash, len(proof))
	for i := range proofHashes {
		proofHashes[i] = Hash(proof[i])
	}
	return proofHashes
}

// VerifyDiffProof verifies a proof produced by MerkleDiffProof.
func VerifyDiffProof(ranges []ProofRange, numLeaves uint64, proofHashes, leafHashes []Hash, root Hash) bool {
	proofBytes := make([][32]byte, len(proofHashes))
	for i := range proofHashes {
		proofBytes[i] = [32]byte(proofHashes[i])
	}
	leafBytes := make([][32]byte, len(leafHashes))
	for i := range leafHashes {
		leafBytes[i] = [32]byte(leafHashes[i])
	}
	ok, _ := merkletree.VerifyDiffProof(leafBytes, numLeaves, ranges, proofBytes, [32]byte(root))
	return ok
}
