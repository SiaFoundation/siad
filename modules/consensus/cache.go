package consensus

import (
	"go.sia.tech/siad/types"
)

// Defined cache sizes.
const (
	scoCacheSize   = 2e5
	fcCacheSize    = 1e5
	blockCacheSize = 32
	idCacheSize    = 32
)

// siacoinOutputInfo is a helper type for siacoinOutputCache.
type siacoinOutputInfo struct {
	id  types.SiacoinOutputID
	sco types.SiacoinOutput
}

// siacoinOutputCache is a storage for the most recently accessed
// Siacoin outputs.
type siacoinOutputCache struct {
	index   map[types.SiacoinOutputID]int
	outputs []siacoinOutputInfo
	tip     int
}

// newSiacoinOutputCache returns an initialized siacoinOutputCache
// object.
func newSiacoinOutputCache() *siacoinOutputCache {
	return &siacoinOutputCache{
		index:   make(map[types.SiacoinOutputID]int),
		outputs: make([]siacoinOutputInfo, scoCacheSize),
	}
}

// Lookup tries to find a Siacoin output in the cache.
func (cache *siacoinOutputCache) Lookup(id types.SiacoinOutputID) (types.SiacoinOutput, bool) {
	i, exists := cache.index[id]
	if !exists {
		return types.SiacoinOutput{}, false
	}
	return cache.outputs[i].sco, true
}

// Push adds a new Siacoin output to the cache. If the output exists,
// it is replaced. If the cache is full, the oldest item is deleted.
func (cache *siacoinOutputCache) Push(id types.SiacoinOutputID, sco types.SiacoinOutput) {
	cache.tip += 1
	if cache.tip >= scoCacheSize {
		cache.tip = 0
	}
	old := cache.outputs[cache.tip].id
	delete(cache.index, old)
	cache.outputs[cache.tip] = siacoinOutputInfo{
		id:  id,
		sco: sco,
	}
	cache.index[id] = cache.tip
}

// Delete removes an existing SiacoinOutput from the cache.
func (cache *siacoinOutputCache) Delete(id types.SiacoinOutputID) {
	delete(cache.index, id)
}

// Reset resets the Siacoin output cache.
func (cache *siacoinOutputCache) Reset() {
	cache.index = make(map[types.SiacoinOutputID]int)
	cache.tip = 0
}

// fileContractInfo is a helper type for fileContractCache.
type fileContractInfo struct {
	id types.FileContractID
	fc types.FileContract
}

// fileContractCache is a storage for the most recently accessed
// storage contracts.
type fileContractCache struct {
	index     map[types.FileContractID]int
	contracts []fileContractInfo
	tip       int
}

// newFileContractCache returns an initialized fileContractCache
// object.
func newFileContractCache() *fileContractCache {
	return &fileContractCache{
		index:     make(map[types.FileContractID]int),
		contracts: make([]fileContractInfo, fcCacheSize),
	}
}

// Lookup tries to find a file contract in the cache.
func (cache *fileContractCache) Lookup(id types.FileContractID) (types.FileContract, bool) {
	i, exists := cache.index[id]
	if !exists {
		return types.FileContract{}, false
	}
	return cache.contracts[i].fc, true
}

// Push adds a new file contract to the cache. If the contract exists,
// it is replaced. If the cache is full, the oldest item is deleted.
func (cache *fileContractCache) Push(id types.FileContractID, fc types.FileContract) {
	cache.tip += 1
	if cache.tip >= fcCacheSize {
		cache.tip = 0
	}
	old := cache.contracts[cache.tip].id
	delete(cache.index, old)
	cache.contracts[cache.tip] = fileContractInfo{
		id: id,
		fc: fc,
	}
	cache.index[id] = cache.tip
}

// Delete removes an existing file contract from the cache.
func (cache *fileContractCache) Delete(id types.FileContractID) {
	delete(cache.index, id)
}

// Reset resets the file contract cache.
func (cache *fileContractCache) Reset() {
	cache.index = make(map[types.FileContractID]int)
	cache.tip = 0
}

// blockInfo is a helper type for blockCache.
type blockInfo struct {
	id types.BlockID
	pb processedBlock
}

// blockCache is a storage for the most recently processed blocks.
type blockCache struct {
	index  map[types.BlockID]int
	blocks []blockInfo
	tip    int
}

// newBlockCache returns an initialized blockCache object.
func newBlockCache() *blockCache {
	return &blockCache{
		index:  make(map[types.BlockID]int),
		blocks: make([]blockInfo, blockCacheSize),
	}
}

// Lookup tries to find a processed block in the cache.
func (cache *blockCache) Lookup(id types.BlockID) (processedBlock, bool) {
	i, exists := cache.index[id]
	if !exists {
		return processedBlock{}, false
	}
	return cache.blocks[i].pb, true
}

// Push adds a new processed block to the cache. If the block exists,
// it is replaced. If the cache is full, the oldest item is deleted.
func (cache *blockCache) Push(id types.BlockID, pb processedBlock) {
	cache.tip += 1
	if cache.tip >= blockCacheSize {
		cache.tip = 0
	}
	old := cache.blocks[cache.tip].id
	delete(cache.index, old)
	cache.blocks[cache.tip] = blockInfo{
		id: id,
		pb: pb,
	}
	cache.index[id] = cache.tip
}

// Reset resets the block cache.
func (cache *blockCache) Reset() {
	cache.index = make(map[types.BlockID]int)
	cache.tip = 0
}

// blockIDInfo is a helper type for blockIDCache.
type blockIDInfo struct {
	nonce types.BlockNonce
	id    types.BlockID
}

// blockIDCache is a storage for the most recently calculated block IDs.
type blockIDCache struct {
	index map[types.BlockNonce]int
	ids   []blockIDInfo
	tip   int
}

// newBlockIDCache returns an initialized blockIDCache object.
func newBlockIDCache() *blockIDCache {
	return &blockIDCache{
		index: make(map[types.BlockNonce]int),
		ids:   make([]blockIDInfo, idCacheSize),
	}
}

// Lookup tries to find a block ID in the cache.
func (cache *blockIDCache) Lookup(nonce types.BlockNonce) (types.BlockID, bool) {
	i, exists := cache.index[nonce]
	if !exists {
		return types.BlockID{}, false
	}
	return cache.ids[i].id, true
}

// Push adds a new block ID to the cache. If the block ID exists,
// it is replaced. If the cache is full, the oldest item is deleted.
func (cache *blockIDCache) Push(nonce types.BlockNonce, id types.BlockID) {
	cache.tip += 1
	if cache.tip >= idCacheSize {
		cache.tip = 0
	}
	old := cache.ids[cache.tip].nonce
	delete(cache.index, old)
	cache.ids[cache.tip] = blockIDInfo{
		nonce: nonce,
		id:    id,
	}
	cache.index[nonce] = cache.tip
}

// Reset resets the block ID cache.
func (cache *blockIDCache) Reset() {
	cache.index = make(map[types.BlockNonce]int)
	cache.tip = 0
}
