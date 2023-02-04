package consensus

import (
	"bytes"
	"testing"
	"time"
	"unsafe"

	"gitlab.com/NebulousLabs/bolt"
	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"

	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/types"
)

var (
	// validateBlockParamsGot stores the parameters passed to the most recent call
	// to mockBlockValidator.ValidateBlock.
	validateBlockParamsGot validateBlockParams

	mockValidBlock = types.Block{
		Timestamp: 100,
		ParentID:  mockParentID(),
	}
	mockInvalidBlock = types.Block{
		Timestamp: 500,
		ParentID:  mockParentID(),
	}
	// parentBlockSerialized is a mock serialized form of a processedBlock.
	parentBlockSerialized = []byte{3, 2, 1}

	parentBlockUnmarshaler = mockBlockMarshaler{
		[]predefinedBlockUnmarshal{
			{parentBlockSerialized, mockParent(), nil},
		},
	}

	parentBlockHighTargetUnmarshaler = mockBlockMarshaler{
		[]predefinedBlockUnmarshal{
			{parentBlockSerialized, mockParentHighTarget(), nil},
		},
	}

	parentBlockLowTargetUnmarshaler = mockBlockMarshaler{
		[]predefinedBlockUnmarshal{
			{parentBlockSerialized, mockParentLowTarget(), nil},
		},
	}

	unmarshalFailedErr = errors.New("mock unmarshal failed")

	failingBlockUnmarshaler = mockBlockMarshaler{
		[]predefinedBlockUnmarshal{
			{parentBlockSerialized, processedBlock{}, unmarshalFailedErr},
		},
	}

	serializedParentBlockMap = []blockMapPair{
		{mockValidBlock.ParentID[:], parentBlockSerialized},
	}
)

type (
	// mockDbBucket is an implementation of dbBucket for unit testing.
	mockDbBucket struct {
		values map[string][]byte
	}

	// mockDbTx is an implementation of dbTx for unit testing. It uses an
	// in-memory key/value store to mock a database.
	mockDbTx struct {
		buckets map[string]dbBucket
	}

	// predefinedBlockUnmarshal is a predefined response from mockBlockMarshaler.
	// It defines the unmarshaled processedBlock and error code that
	// mockBlockMarshaler should return in response to an input serialized byte
	// slice.
	predefinedBlockUnmarshal struct {
		serialized  []byte
		unmarshaled processedBlock
		err         error
	}

	// mockBlockMarshaler is an implementation of the encoding.GenericMarshaler
	// interface for unit testing. It allows clients to specify mappings of
	// serialized bytes into unmarshaled blocks.
	mockBlockMarshaler struct {
		p []predefinedBlockUnmarshal
	}

	// mockBlockRuleHelper is an implementation of the blockRuleHelper interface
	// for unit testing.
	mockBlockRuleHelper struct {
		minTimestamp types.Timestamp
	}

	// mockBlockValidator is an implementation of the blockValidator interface for
	// unit testing.
	mockBlockValidator struct {
		err error
	}

	// validateBlockParams stores the set of parameters passed to ValidateBlock.
	validateBlockParams struct {
		called       bool
		b            types.Block
		minTimestamp types.Timestamp
		target       types.Target
		height       types.BlockHeight
	}

	// blockMapPair represents a key-value pair in the mock block map.
	blockMapPair struct {
		key []byte
		val []byte
	}
)

// Get returns the value associated with a given key.
func (bucket mockDbBucket) Get(key []byte) []byte {
	return bucket.values[string(key)]
}

// Set adds a named value to a mockDbBucket.
func (bucket mockDbBucket) Set(key []byte, value []byte) {
	bucket.values[string(key)] = value
}

// Bucket returns a mock dbBucket object associated with the given bucket name.
func (db mockDbTx) Bucket(name []byte) dbBucket {
	return db.buckets[string(name)]
}

// Marshal is not implemented and panics if called.
func (m mockBlockMarshaler) Marshal(interface{}) []byte {
	panic("not implemented")
}

// Unmarshal unmarshals a byte slice into an object based on a pre-defined map
// of deserialized objects.
func (m mockBlockMarshaler) Unmarshal(b []byte, v interface{}) error {
	for _, pu := range m.p {
		if bytes.Equal(b[:], pu.serialized[:]) {
			pv, ok := v.(*processedBlock)
			if !ok {
				panic("mockBlockMarshaler.Unmarshal expected v to be of type processedBlock")
			}
			*pv = pu.unmarshaled
			return pu.err
		}
	}
	panic("unmarshal failed: predefined unmarshal not found")
}

// AddPredefinedUnmarshal adds a predefinedBlockUnmarshal to mockBlockMarshaler.
func (m *mockBlockMarshaler) AddPredefinedUnmarshal(u predefinedBlockUnmarshal) {
	m.p = append(m.p, u)
}

// minimumValidChildTimestamp returns the minimum timestamp of pb that can be
// considered a valid block.
func (brh mockBlockRuleHelper) minimumValidChildTimestamp(blockMap dbBucket, pb *processedBlock) types.Timestamp {
	return brh.minTimestamp
}

// ValidateBlock stores the parameters it receives and returns the mock error
// defined by mockBlockValidator.err.
func (bv mockBlockValidator) ValidateBlock(b types.Block, id types.BlockID, minTimestamp types.Timestamp, target types.Target, height types.BlockHeight, log *persist.Logger) error {
	validateBlockParamsGot = validateBlockParams{true, b, minTimestamp, target, height}
	return bv.err
}

// mockParentID returns a mock BlockID value.
func mockParentID() (parentID types.BlockID) {
	parentID[0] = 42
	return parentID
}

// mockParent returns a mock processedBlock with its ChildTarget member
// initialized to a dummy value.
func mockParent() (parent processedBlock) {
	var mockTarget types.Target
	mockTarget[0] = 56
	parent.ChildTarget = mockTarget
	return parent
}

// mockParent returns a mock processedBlock with its ChildTarget member
// initialized to a the maximum value.
func mockParentHighTarget() (parent processedBlock) {
	parent.ChildTarget = types.RootDepth
	return parent
}

// mockParent returns a mock processedBlock with its ChildTarget member
// initialized to the minimum value.
func mockParentLowTarget() (parent processedBlock) {
	return parent
}

// TestUnitValidateHeaderAndBlock runs a series of unit tests for validateHeaderAndBlock.
func TestUnitValidateHeaderAndBlock(t *testing.T) {
	var tests = []struct {
		block                  types.Block
		dosBlocks              map[types.BlockID]struct{}
		blockMapPairs          []blockMapPair
		earliestValidTimestamp types.Timestamp
		marshaler              mockBlockMarshaler
		useNilBlockMap         bool
		validateBlockErr       error
		errWant                error
		msg                    string
	}{
		{
			block:                  mockValidBlock,
			dosBlocks:              make(map[types.BlockID]struct{}),
			useNilBlockMap:         true,
			earliestValidTimestamp: mockValidBlock.Timestamp,
			marshaler:              parentBlockUnmarshaler,
			errWant:                errNoBlockMap,
			msg:                    "validateHeaderAndBlock should fail when no block map is found in the database",
		},
		{
			block: mockValidBlock,
			// Create a dosBlocks map where mockValidBlock is marked as a bad block.
			dosBlocks: map[types.BlockID]struct{}{
				mockValidBlock.ID(): {},
			},
			earliestValidTimestamp: mockValidBlock.Timestamp,
			marshaler:              parentBlockUnmarshaler,
			errWant:                errDoSBlock,
			msg:                    "validateHeaderAndBlock should reject known bad blocks",
		},
		{
			block:                  mockValidBlock,
			dosBlocks:              make(map[types.BlockID]struct{}),
			earliestValidTimestamp: mockValidBlock.Timestamp,
			marshaler:              parentBlockUnmarshaler,
			errWant:                errOrphan,
			msg:                    "validateHeaderAndBlock should reject a block if its parent block does not appear in the block database",
		},
		{
			block:                  mockValidBlock,
			dosBlocks:              make(map[types.BlockID]struct{}),
			blockMapPairs:          serializedParentBlockMap,
			earliestValidTimestamp: mockValidBlock.Timestamp,
			marshaler:              failingBlockUnmarshaler,
			errWant:                unmarshalFailedErr,
			msg:                    "validateHeaderAndBlock should fail when unmarshaling the parent block fails",
		},
		{
			block:     mockInvalidBlock,
			dosBlocks: make(map[types.BlockID]struct{}),
			blockMapPairs: []blockMapPair{
				{mockInvalidBlock.ParentID[:], parentBlockSerialized},
			},
			earliestValidTimestamp: mockInvalidBlock.Timestamp,
			marshaler:              parentBlockUnmarshaler,
			validateBlockErr:       ErrBadMinerPayouts,
			errWant:                ErrBadMinerPayouts,
			msg:                    "validateHeaderAndBlock should reject a block if ValidateBlock returns an error for the block",
		},
		{
			block:                  mockValidBlock,
			dosBlocks:              make(map[types.BlockID]struct{}),
			blockMapPairs:          serializedParentBlockMap,
			earliestValidTimestamp: mockValidBlock.Timestamp,
			marshaler:              parentBlockUnmarshaler,
			errWant:                nil,
			msg:                    "validateHeaderAndBlock should accept a valid block",
		},
	}
	for _, tt := range tests {
		// Initialize the blockmap in the tx.
		bucket := mockDbBucket{map[string][]byte{}}
		for _, mapPair := range tt.blockMapPairs {
			bucket.Set(mapPair.key, mapPair.val)
		}
		dbBucketMap := map[string]dbBucket{}
		if tt.useNilBlockMap {
			dbBucketMap[string(BlockMap)] = nil
		} else {
			dbBucketMap[string(BlockMap)] = bucket
		}
		tx := mockDbTx{dbBucketMap}

		mockParent := mockParent()
		cs := ConsensusSet{
			dosBlocks: tt.dosBlocks,
			marshaler: tt.marshaler,
			blockRuleHelper: mockBlockRuleHelper{
				minTimestamp: tt.earliestValidTimestamp,
			},
			blockValidator: mockBlockValidator{tt.validateBlockErr},
		}
		// Reset the stored parameters to ValidateBlock.
		validateBlockParamsGot = validateBlockParams{}
		_, err := cs.validateHeaderAndBlock(tx, tt.block, tt.block.ID())
		if err != tt.errWant && !errors.Contains(err, tt.errWant) {
			t.Errorf("%s: expected to fail with `%v', got: `%v'", tt.msg, tt.errWant, err)
		}
		if err == nil || validateBlockParamsGot.called {
			if validateBlockParamsGot.b.ID() != tt.block.ID() {
				t.Errorf("%s: incorrect parameter passed to ValidateBlock - got: %v, want: %v", tt.msg, validateBlockParamsGot.b, tt.block)
			}
			if validateBlockParamsGot.minTimestamp != tt.earliestValidTimestamp {
				t.Errorf("%s: incorrect parameter passed to ValidateBlock - got: %v, want: %v", tt.msg, validateBlockParamsGot.minTimestamp, tt.earliestValidTimestamp)
			}
			if validateBlockParamsGot.target != mockParent.ChildTarget {
				t.Errorf("%s: incorrect parameter passed to ValidateBlock - got: %v, want: %v", tt.msg, validateBlockParamsGot.target, mockParent.ChildTarget)
			}
		}
	}
}

// TestCheckHeaderTarget probes the checkHeaderTarget function and checks that
// the result matches the result of checkTarget.
func TestCheckHeaderTarget(t *testing.T) {
	var b types.Block
	var h types.BlockHeader

	tests := []struct {
		target   types.Target
		expected bool
		msg      string
	}{
		{types.RootDepth, true, "checkHeaderTarget failed for a low target"},
		{types.Target{}, false, "checkHeaderTarget passed for a high target"},
		{types.Target(h.ID()), true, "checkHeaderTarget failed for a same target"},
	}
	for _, tt := range tests {
		if checkHeaderTarget(h, tt.target) != tt.expected {
			t.Error(tt.msg)
		}
		if checkHeaderTarget(h, tt.target) != checkTarget(b, b.ID(), tt.target) {
			t.Errorf("checkHeaderTarget and checkTarget do not match for target %v", tt.target)
		}
	}
}

// TestUnitValidateHeader runs a series of unit tests for validateHeader.
func TestUnitValidateHeader(t *testing.T) {
	mockValidBlockID := mockValidBlock.ID()

	var tests = []struct {
		header                 types.BlockHeader
		dosBlocks              map[types.BlockID]struct{}
		blockMapPairs          []blockMapPair
		earliestValidTimestamp types.Timestamp
		marshaler              mockBlockMarshaler
		useNilBlockMap         bool
		errWant                error
		msg                    string
	}{
		// Test that known dos blocks are rejected.
		{
			header: mockValidBlock.Header(),
			// Create a dosBlocks map where mockValidBlock is marked as a bad block.
			dosBlocks: map[types.BlockID]struct{}{
				mockValidBlock.ID(): {},
			},
			blockMapPairs:          serializedParentBlockMap,
			earliestValidTimestamp: mockValidBlock.Timestamp,
			marshaler:              parentBlockUnmarshaler,
			errWant:                errDoSBlock,
			msg:                    "validateHeader should reject known bad blocks",
		},
		// Test that blocks are rejected if a block map doesn't exist.
		{
			header:                 mockValidBlock.Header(),
			dosBlocks:              make(map[types.BlockID]struct{}),
			blockMapPairs:          serializedParentBlockMap,
			earliestValidTimestamp: mockValidBlock.Timestamp,
			marshaler:              parentBlockUnmarshaler,
			useNilBlockMap:         true,
			errWant:                errNoBlockMap,
			msg:                    "validateHeader should fail when no block map is found in the database",
		},
		// Test that known blocks are rejected.
		{
			header:                 mockValidBlock.Header(),
			dosBlocks:              make(map[types.BlockID]struct{}),
			blockMapPairs:          []blockMapPair{{mockValidBlockID[:], []byte{}}},
			earliestValidTimestamp: mockValidBlock.Timestamp,
			marshaler:              parentBlockUnmarshaler,
			errWant:                modules.ErrBlockKnown,
			msg:                    "validateHeader should fail when the block has been seen before",
		},
		// Test that blocks with unknown parents (orphans) are rejected.
		{
			header:                 mockValidBlock.Header(),
			dosBlocks:              make(map[types.BlockID]struct{}),
			earliestValidTimestamp: mockValidBlock.Timestamp,
			marshaler:              parentBlockUnmarshaler,
			errWant:                errOrphan,
			msg:                    "validateHeader should reject a block if its parent block does not appear in the block database",
		},
		// Test that blocks whose parents don't unmarshal are rejected.
		{
			header:                 mockValidBlock.Header(),
			dosBlocks:              make(map[types.BlockID]struct{}),
			blockMapPairs:          serializedParentBlockMap,
			earliestValidTimestamp: mockValidBlock.Timestamp,
			marshaler:              failingBlockUnmarshaler,
			errWant:                unmarshalFailedErr,
			msg:                    "validateHeader should fail when unmarshaling the parent block fails",
		},
		// Test that blocks with too early of a timestamp are rejected.
		{
			header:                 mockValidBlock.Header(),
			dosBlocks:              make(map[types.BlockID]struct{}),
			blockMapPairs:          serializedParentBlockMap,
			earliestValidTimestamp: mockValidBlock.Timestamp + 1,
			marshaler:              parentBlockHighTargetUnmarshaler,
			errWant:                ErrEarlyTimestamp,
			msg:                    "validateHeader should fail when the header's timestamp is too early",
		},
		// Test that headers in the extreme future are rejected.
		{
			header: types.BlockHeader{
				Timestamp: types.CurrentTimestamp() + types.ExtremeFutureThreshold + 2,
				ParentID:  mockParentID(),
			},
			dosBlocks:     make(map[types.BlockID]struct{}),
			blockMapPairs: serializedParentBlockMap,
			marshaler:     parentBlockHighTargetUnmarshaler,
			errWant:       ErrExtremeFutureTimestamp,
			msg:           "validateHeader should fail when the header's timestamp is in the extreme future",
		},
		// Test that headers in the near future are not rejected.
		{
			header: types.BlockHeader{
				Timestamp: types.CurrentTimestamp() + types.FutureThreshold + 2,
				ParentID:  mockParentID(),
			},
			dosBlocks:     make(map[types.BlockID]struct{}),
			blockMapPairs: serializedParentBlockMap,
			marshaler:     parentBlockHighTargetUnmarshaler,
			errWant:       nil,
			msg:           "validateHeader should not reject headers whose timestamps are in the near future",
		},
		// Test that blocks with too large of a target are rejected.
		{
			header:                 mockValidBlock.Header(),
			dosBlocks:              make(map[types.BlockID]struct{}),
			blockMapPairs:          serializedParentBlockMap,
			earliestValidTimestamp: mockValidBlock.Timestamp,
			marshaler:              parentBlockLowTargetUnmarshaler,
			errWant:                modules.ErrBlockUnsolved,
			msg:                    "validateHeader should reject blocks with an insufficiently low target",
		},
		// Test that valid blocks are accepted.
		{
			header:                 mockValidBlock.Header(),
			dosBlocks:              make(map[types.BlockID]struct{}),
			blockMapPairs:          serializedParentBlockMap,
			earliestValidTimestamp: mockValidBlock.Timestamp,
			marshaler:              parentBlockHighTargetUnmarshaler,
			errWant:                nil,
			msg:                    "validateHeader should accept a valid block",
		},
	}
	for _, tt := range tests {
		// Initialize the blockmap in the tx.
		bucket := mockDbBucket{map[string][]byte{}}
		for _, mapPair := range tt.blockMapPairs {
			bucket.Set(mapPair.key, mapPair.val)
		}
		dbBucketMap := map[string]dbBucket{}
		if tt.useNilBlockMap {
			dbBucketMap[string(BlockMap)] = nil
		} else {
			dbBucketMap[string(BlockMap)] = bucket
		}
		tx := mockDbTx{dbBucketMap}

		cs := ConsensusSet{
			dosBlocks: tt.dosBlocks,
			marshaler: tt.marshaler,
			blockRuleHelper: mockBlockRuleHelper{
				minTimestamp: tt.earliestValidTimestamp,
			},
		}
		err := cs.validateHeader(tx, tt.header)
		if err != tt.errWant {
			t.Errorf("%s: expected to fail with `%v', got: `%v'", tt.msg, tt.errWant, err)
		}
	}
}

// TestIntegrationDoSBlockHandling checks that saved bad blocks are correctly
// ignored.
func TestIntegrationDoSBlockHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	cst, err := createConsensusSetTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cst.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Mine a block that is valid except for containing a buried invalid
	// transaction. The transaction has more siacoin inputs than outputs.
	txnBuilder, err := cst.wallet.StartTransaction()
	if err != nil {
		t.Fatal(err)
	}
	err = txnBuilder.FundSiacoins(types.NewCurrency64(50))
	if err != nil {
		t.Fatal(err)
	}
	txnSet, err := txnBuilder.Sign(true) // true sets the 'wholeTransaction' flag
	if err != nil {
		t.Fatal(err)
	}

	// Mine and submit the invalid block to the consensus set. The first time
	// around, the complaint should be about the rule-breaking transaction.
	block, target, err := cst.miner.BlockForWork()
	if err != nil {
		t.Fatal(err)
	}
	block.Transactions = append(block.Transactions, txnSet...)
	dosBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.AcceptBlock(dosBlock)
	if !errors.Contains(err, errSiacoinInputOutputMismatch) {
		t.Fatalf("expected %v, got %v", errSiacoinInputOutputMismatch, err)
	}

	// Submit the same block a second time. The complaint should be that the
	// block is already known to be invalid.
	err = cst.cs.AcceptBlock(dosBlock)
	if !errors.Contains(err, errDoSBlock) {
		t.Fatalf("expected %v, got %v", errDoSBlock, err)
	}
}

// TestBlockKnownHandling submits known blocks to the consensus set.
func TestBlockKnownHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	cst, err := createConsensusSetTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cst.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Get a block destined to be stale.
	block, target, err := cst.miner.BlockForWork()
	if err != nil {
		t.Fatal(err)
	}
	staleBlock, _ := cst.miner.SolveBlock(block, target)

	// Add two new blocks to the consensus set to block the stale block.
	block1, err := cst.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}
	block2, err := cst.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Submit the stale block.
	err = cst.cs.AcceptBlock(staleBlock)
	if err != nil && !errors.Contains(err, modules.ErrNonExtendingBlock) {
		t.Fatal(err)
	}

	// Submit all the blocks again, looking for a 'stale block' error.
	err = cst.cs.AcceptBlock(block1)
	if err == nil {
		t.Fatal("expected an error upon submitting the block")
	}
	err = cst.cs.AcceptBlock(block2)
	if err == nil {
		t.Fatal("expected an error upon submitting the block")
	}
	err = cst.cs.AcceptBlock(staleBlock)
	if err == nil {
		t.Fatal("expected an error upon submitting the block")
	}

	// Try submitting the genesis block.
	id, err := cst.cs.dbGetPath(0)
	if err != nil {
		t.Fatal(err)
	}
	genesisBlock, err := cst.cs.dbGetBlockMap(id)
	if err != nil {
		t.Fatal(err)
	}
	err = cst.cs.AcceptBlock(genesisBlock.Block)
	if err == nil {
		t.Fatal("expected an error upon submitting the block")
	}
}

// TestOrphanHandling passes an orphan block to the consensus set.
func TestOrphanHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	cst, err := createConsensusSetTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cst.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Try submitting an orphan block to the consensus set. The empty block can
	// be used, because looking for a parent is one of the first checks the
	// consensus set performs.
	orphan := types.Block{}
	err = cst.cs.AcceptBlock(orphan)
	if !errors.Contains(err, errOrphan) {
		t.Fatalf("expected %v, got %v", errOrphan, err)
	}
	err = cst.cs.AcceptBlock(orphan)
	if !errors.Contains(err, errOrphan) {
		t.Fatalf("expected %v, got %v", errOrphan, err)
	}
}

// TestMissedTarget submits a block that does not meet the required target.
func TestMissedTarget(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	cst, err := createConsensusSetTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cst.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Mine a block that doesn't meet the target.
	block, target, err := cst.miner.BlockForWork()
	if err != nil {
		t.Fatal(err)
	}
	for checkTarget(block, block.ID(), target) {
		*(*uint64)(unsafe.Pointer(&block.Nonce)) += types.ASICHardforkFactor
	}
	if checkTarget(block, block.ID(), target) {
		t.Fatal("unable to find a failing target")
	}
	err = cst.cs.AcceptBlock(block)
	if !errors.Contains(err, modules.ErrBlockUnsolved) {
		t.Fatalf("expected %v, got %v", modules.ErrBlockUnsolved, err)
	}
}

// TestMinerPayoutHandling checks that blocks with incorrect payouts are
// rejected.
func TestMinerPayoutHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	cst, err := createConsensusSetTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cst.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create a block with the wrong miner payout structure - testing can be
	// light here because there is heavier testing in the 'types' package,
	// where the logic is defined.
	block, target, err := cst.miner.BlockForWork()
	if err != nil {
		t.Fatal(err)
	}
	block.MinerPayouts = append(block.MinerPayouts, types.SiacoinOutput{Value: types.NewCurrency64(1)})
	solvedBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.AcceptBlock(solvedBlock)
	if !errors.Contains(err, ErrBadMinerPayouts) {
		t.Fatalf("expected %v, got %v", ErrBadMinerPayouts, err)
	}
}

// TestEarlyTimestampHandling checks that blocks too far in the past are
// rejected.
func TestEarlyTimestampHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	cst, err := createConsensusSetTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cst.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	minTimestamp := types.CurrentTimestamp()
	cst.cs.blockRuleHelper = mockBlockRuleHelper{
		minTimestamp: minTimestamp,
	}

	// Submit a block with a timestamp in the past, before minTimestamp.
	block, target, err := cst.miner.BlockForWork()
	if err != nil {
		t.Fatal(err)
	}
	block.Timestamp = minTimestamp - 1
	solvedBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.AcceptBlock(solvedBlock)
	if !errors.Contains(err, ErrEarlyTimestamp) {
		t.Fatalf("expected %v, got %v", ErrEarlyTimestamp, err)
	}
}

// testFutureTimestampHandling checks that blocks in the future (but not
// extreme future) are handled correctly.
func TestFutureTimestampHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	cst, err := createConsensusSetTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cst.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Submit a block with a timestamp in the future, but not the extreme
	// future.
	block, target, err := cst.miner.BlockForWork()
	if err != nil {
		t.Fatal(err)
	}
	block.Timestamp = types.CurrentTimestamp() + 2 + types.FutureThreshold
	solvedBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.AcceptBlock(solvedBlock)
	if !errors.Contains(err, ErrFutureTimestamp) {
		t.Fatalf("expected %v, got %v", ErrFutureTimestamp, err)
	}

	// Poll the consensus set until the future block appears.
	for i := 0; i < 30; i++ {
		time.Sleep(time.Second * 3)
		_, err = cst.cs.dbGetBlockMap(solvedBlock.ID())
		if err == nil {
			break
		}
	}
	_, err = cst.cs.dbGetBlockMap(solvedBlock.ID())
	if err != nil {
		t.Errorf("Future block not added to consensus set.\nCurrent Timestamp %v\nFutureThreshold: %v\nBlock Timestamp %v\n", types.CurrentTimestamp(), types.FutureThreshold, block.Timestamp)
	}
}

// TestExtremeFutureTimestampHandling checks that blocks in the extreme future
// are rejected.
func TestExtremeFutureTimestampHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	cst, err := createConsensusSetTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cst.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Submit a block with a timestamp in the extreme future.
	block, target, err := cst.miner.BlockForWork()
	if err != nil {
		t.Fatal(err)
	}
	block.Timestamp = types.CurrentTimestamp() + 2 + types.ExtremeFutureThreshold
	solvedBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.AcceptBlock(solvedBlock)
	if !errors.Contains(err, ErrExtremeFutureTimestamp) {
		t.Fatalf("expected %v, got %v", ErrFutureTimestamp, err)
	}
}

// TestBuriedBadTransaction tries submitting a block with a bad transaction
// that is buried under good transactions.
func TestBuriedBadTransaction(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	cst, err := createConsensusSetTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cst.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	pb := cst.cs.dbCurrentProcessedBlock()

	// Create a good transaction using the wallet.
	txnValue := types.NewCurrency64(1200)
	txnBuilder, err := cst.wallet.StartTransaction()
	if err != nil {
		t.Fatal(err)
	}
	err = txnBuilder.FundSiacoins(txnValue)
	if err != nil {
		t.Fatal(err)
	}
	txnBuilder.AddSiacoinOutput(types.SiacoinOutput{Value: txnValue})
	txnSet, err := txnBuilder.Sign(true)
	if err != nil {
		t.Fatal(err)
	}
	err = cst.tpool.AcceptTransactionSet(txnSet)
	if err != nil {
		t.Fatal(err)
	}

	// Create a bad transaction
	badTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{}},
	}
	txns := append(cst.tpool.TransactionList(), badTxn)

	// Create a block with a buried bad transaction.
	block := types.Block{
		ParentID:     pb.Block.ID(),
		Timestamp:    types.CurrentTimestamp(),
		MinerPayouts: []types.SiacoinOutput{{Value: types.CalculateCoinbase(pb.Height + 1)}},
		Transactions: txns,
	}
	block, _ = cst.miner.SolveBlock(block, pb.ChildTarget)
	err = cst.cs.AcceptBlock(block)
	if err == nil {
		t.Error("buried transaction didn't cause an error")
	}
}

// TestInconsistencyCheck puts the consensus set in to an inconsistent state
// and makes sure that the santiy checks are triggering panics.
func TestInconsistentCheck(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	cst, err := createConsensusSetTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the consensus set by adding a new siafund output.
	sfo := types.SiafundOutput{
		Value: types.NewCurrency64(1),
	}
	cst.cs.dbAddSiafundOutput(types.SiafundOutputID{}, sfo)

	// Catch a panic that should be caused by the inconsistency check after a
	// block is mined.
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("inconsistency panic not triggered by corrupted database")
		}
	}()
	_, err = cst.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}
}

// COMPATv0.4.0
//
// This test checks that the hardfork scheduled for block 21,000 rolls through
// smoothly.
func TestTaxHardfork(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	cst, err := createConsensusSetTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cst.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create a file contract with a payout that is put into the blockchain
	// before the hardfork block but expires after the hardfork block.
	payout := types.NewCurrency64(400e6)
	outputSize := types.PostTax(cst.cs.dbBlockHeight(), payout)
	fc := types.FileContract{
		WindowStart:        cst.cs.dbBlockHeight() + 12,
		WindowEnd:          cst.cs.dbBlockHeight() + 14,
		Payout:             payout,
		ValidProofOutputs:  []types.SiacoinOutput{{Value: outputSize}},
		MissedProofOutputs: []types.SiacoinOutput{{Value: outputSize}},
		UnlockHash:         types.UnlockConditions{}.UnlockHash(), // The empty UC is anyone-can-spend
	}

	// Create and fund a transaction with a file contract.
	txnBuilder, err := cst.wallet.StartTransaction()
	if err != nil {
		t.Fatal(err)
	}
	err = txnBuilder.FundSiacoins(payout)
	if err != nil {
		t.Fatal(err)
	}
	fcIndex := txnBuilder.AddFileContract(fc)
	txnSet, err := txnBuilder.Sign(true)
	if err != nil {
		t.Fatal(err)
	}
	err = cst.tpool.AcceptTransactionSet(txnSet)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cst.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Check that the siafund pool was increased by the faulty float amount.
	siafundPool := cst.cs.dbGetSiafundPool()
	if !siafundPool.Equals64(15590e3) {
		t.Fatal("siafund pool was not increased correctly")
	}

	// Mine blocks until the hardfork is reached.
	for i := 0; i < 10; i++ {
		_, err = cst.miner.AddBlock()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Submit a file contract revision and check that the payouts are able to
	// be the same.
	fcid := txnSet[len(txnSet)-1].FileContractID(fcIndex)
	fcr := types.FileContractRevision{
		ParentID:          fcid,
		UnlockConditions:  types.UnlockConditions{},
		NewRevisionNumber: 1,

		NewFileSize:           1,
		NewWindowStart:        cst.cs.dbBlockHeight() + 2,
		NewWindowEnd:          cst.cs.dbBlockHeight() + 4,
		NewValidProofOutputs:  fc.ValidProofOutputs,
		NewMissedProofOutputs: fc.MissedProofOutputs,
	}
	txnBuilder, err = cst.wallet.StartTransaction()
	if err != nil {
		t.Fatal(err)
	}
	txnBuilder.AddFileContractRevision(fcr)
	txnSet, err = txnBuilder.Sign(true)
	if err != nil {
		t.Fatal(err)
	}
	err = cst.tpool.AcceptTransactionSet(txnSet)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cst.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Mine blocks until the revision goes through, such that the sanity checks
	// can be run.
	for i := 0; i < 6; i++ {
		_, err = cst.miner.AddBlock()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Check that the siafund pool did not change after the submitted revision.
	siafundPool = cst.cs.dbGetSiafundPool()
	if !siafundPool.Equals64(15590e3) {
		t.Fatal("siafund pool was not increased correctly")
	}
}

// mockGatewayDoesBroadcast implements modules.Gateway to mock the Broadcast
// method.
type mockGatewayDoesBroadcast struct {
	modules.Gateway
	broadcastCalled chan struct{}
}

// Broadcast is a mock implementation of modules.Gateway.Broadcast that
// sends a sentinel value down a channel to signal it's been called.
func (g *mockGatewayDoesBroadcast) Broadcast(name string, obj interface{}, peers []modules.Peer) {
	g.Gateway.Broadcast(name, obj, peers)
	g.broadcastCalled <- struct{}{}
}

// TestAcceptBlockBroadcasts tests that AcceptBlock broadcasts valid blocks and
// that managedAcceptBlock does not.
func TestAcceptBlockBroadcasts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	cst, err := blankConsensusSetTester(t.Name(), modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cst.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	mg := &mockGatewayDoesBroadcast{
		Gateway:         cst.cs.gateway,
		broadcastCalled: make(chan struct{}),
	}
	cst.cs.gateway = mg

	// Test that Broadcast is called for valid blocks.
	b, _ := cst.miner.FindBlock()
	err = cst.cs.AcceptBlock(b)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mg.broadcastCalled:
	case <-time.After(time.Second):
		t.Error("expected AcceptBlock to broadcast a valid block")
	}

	// Test that Broadcast is not called for invalid blocks.
	err = cst.cs.AcceptBlock(types.Block{})
	if err == nil {
		t.Fatal("expected AcceptBlock to error on an invalid block")
	}
	select {
	case <-mg.broadcastCalled:
		t.Error("AcceptBlock broadcasted an invalid block")
	case <-time.After(time.Second):
	}

	// Test that Broadcast is not called in managedAcceptBlock.
	b, _ = cst.miner.FindBlock()
	_, err = cst.cs.managedAcceptBlocks([]types.Block{b})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mg.broadcastCalled:
		t.Errorf("managedAcceptBlock should not broadcast blocks")
	case <-time.After(10 * time.Millisecond):
	}
}

// blockCountingSubscriber counts the number of blocks that get submitted to the
// subscriber, as well as the number of times that the subscriber has been given
// changes at all.
type blockCountingSubscriber struct {
	changes []modules.ConsensusChangeID

	appliedBlocks  int
	revertedBlocks int
}

// ProcessConsensusChange fills the subscription interface for the
// blockCountingSubscriber.
func (bcs *blockCountingSubscriber) ProcessConsensusChange(cc modules.ConsensusChange) {
	bcs.changes = append(bcs.changes, cc.ID)
	bcs.revertedBlocks += len(cc.RevertedBlocks)
	bcs.appliedBlocks += len(cc.AppliedBlocks)
}

// TestChainedAcceptBlock creates series of blocks, some of which are valid,
// some invalid, and submits them to the consensus set, verifying that the
// consensus set updates correctly each time.
func TestChainedAcceptBlock(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// Create a tester to send blocks in a batch to the other tester.
	cst, err := createConsensusSetTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cst.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	cst2, err := blankConsensusSetTester(t.Name()+"2", modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cst2.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Subscribe a blockCountingSubscriber to cst2.
	var bcs blockCountingSubscriber
	cst2.cs.ConsensusSetSubscribe(&bcs, modules.ConsensusChangeBeginning, cst2.cs.tg.StopChan())
	if len(bcs.changes) != 1 || bcs.appliedBlocks != 1 || bcs.revertedBlocks != 0 {
		t.Error("consensus changes do not seem to be getting passed to subscribers correctly")
	}

	// Grab all of the blocks in cst, with the intention of giving them to cst2.
	var blocks []types.Block
	height := cst.cs.Height()
	for i := types.BlockHeight(0); i <= height; i++ {
		id, err := cst.cs.dbGetPath(i)
		if err != nil {
			t.Fatal(err)
		}
		pb, err := cst.cs.dbGetBlockMap(id)
		if err != nil {
			t.Fatal(err)
		}
		blocks = append(blocks, pb.Block)
	}

	// Create a jumbling of the blocks, so that the set is not in order.
	jumble := make([]types.Block, len(blocks))
	jumble[0] = blocks[0]
	jumble[1] = blocks[2]
	jumble[2] = blocks[1]
	for i := 3; i < len(jumble); i++ {
		jumble[i] = blocks[i]
	}
	// Try to submit the blocks out-of-order, which would violate one of the
	// assumptions in managedAcceptBlocks.
	_, err = cst2.cs.managedAcceptBlocks(jumble)
	if !errors.Contains(err, errNonLinearChain) {
		t.Fatal(err)
	}
	if cst2.cs.Height() != 0 {
		t.Fatal("blocks added even though the inputs were jumbled")
	}
	if len(bcs.changes) != 1 || bcs.appliedBlocks != 1 || bcs.revertedBlocks != 0 {
		t.Error("consensus changes do not seem to be getting passed to subscribers correctly")
	}

	// Tag an invalid block onto the end of blocks.
	block, err := cst.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}
	// Adding an invalid transaction to make the block invalid.
	badBlock := block
	badBlock.Transactions = append(badBlock.Transactions, types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID: types.SiacoinOutputID{1},
		}},
	})
	// Append the invalid transaction to the block.
	badBlocks := append(blocks, badBlock)
	// Submit the whole invalid set. Result should be that nothing is added.
	_, err = cst2.cs.managedAcceptBlocks(badBlocks)
	if err == nil {
		t.Fatal(err)
	}
	if cst2.cs.Height() != 0 {
		t.Log(cst2.cs.Height())
		t.Log(cst.cs.Height())
		t.Fatal("height is not correct, seems that blocks were added")
	}
	if bcs.appliedBlocks != 1 || bcs.revertedBlocks != 0 {
		t.Error("consensus changes do not seem to be getting passed to subscribers correctly")
	}

	// Try submitting the good blocks.
	_, err = cst2.cs.managedAcceptBlocks(blocks)
	if err != nil {
		t.Fatal(err)
	}
	if bcs.appliedBlocks != int(cst2.cs.Height()+1) || bcs.revertedBlocks != 0 {
		t.Error("consensus changes do not seem to be getting passed to subscribers correctly")
	}

	// Check that every change recorded in 'bcs' is also available in the
	// consensus set.
	for _, change := range bcs.changes {
		err := cst2.cs.db.Update(func(tx *bolt.Tx) error {
			_, exists := getEntry(tx, change)
			if !exists {
				t.Error("an entry was provided that doesn't exist")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestFoundationUpdateBlocks tests various scenarios involving blocks that
// contain a FoundationUnlockHashUpdate.
func TestFoundationUpdateBlocks(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	cst, err := createConsensusSetTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cst.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Mine until the initial subsidy has matured.
	for cst.cs.Height() < types.FoundationHardforkHeight+types.MaturityDelay {
		if _, err := cst.miner.AddBlock(); err != nil {
			t.Fatal(err)
		}
	}

	foundationOutput := func(height types.BlockHeight) (id types.SiacoinOutputID, sco types.SiacoinOutput, exists bool) {
		err := cst.cs.db.View(func(tx *bolt.Tx) error {
			bid, err := getPath(tx, height)
			if err != nil {
				t.Fatal(err)
			}
			id = bid.FoundationSubsidyID()
			sco, err = getSiacoinOutput(tx, id)
			exists = err == nil
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return
	}

	mineTxn := func(txn types.Transaction) error {
		block, target, err := cst.miner.BlockForWork()
		if err != nil {
			t.Fatal(err)
		}
		block.Transactions = append(block.Transactions, txn)
		block, _ = cst.miner.SolveBlock(block, target)
		return cst.cs.AcceptBlock(block)
	}

	// Sign and submit a transaction that sends the initial subsidy output to the void
	outputHeight := types.FoundationHardforkHeight
	scoid, sco, ok := foundationOutput(outputHeight)
	if !ok {
		t.Fatal("initial subsidy should exist")
	}
	primaryUC, primaryKeys := types.GenerateDeterministicMultisig(2, 3, types.InitialFoundationTestingSalt)
	txn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         scoid,
			UnlockConditions: primaryUC,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      sco.Value,
			UnlockHash: types.UnlockHash{},
		}},
		TransactionSignatures: make([]types.TransactionSignature, primaryUC.SignaturesRequired),
	}
	for i := range txn.TransactionSignatures {
		txn.TransactionSignatures[i].ParentID = crypto.Hash(scoid)
		txn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		txn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(txn.SigHash(i, cst.cs.Height()), primaryKeys[i])
		txn.TransactionSignatures[i].Signature = sig[:]
	}
	if err := mineTxn(txn); err != nil {
		t.Fatal(err)
	}

	// Mine until the next subsidy matures.
	outputHeight += types.FoundationSubsidyFrequency
	for cst.cs.Height() < outputHeight+types.MaturityDelay {
		if _, err := cst.miner.AddBlock(); err != nil {
			t.Fatal(err)
		}
	}

	// Sign and submit a transaction that changes the primary address to the void.
	// Also send 1 SC to the primary and failsafe addresses for later use.
	scoid, sco, ok = foundationOutput(outputHeight)
	if !ok {
		t.Fatal("first subsidy should exist")
	}
	txn = types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         scoid,
			UnlockConditions: primaryUC,
		}},
		SiacoinOutputs: []types.SiacoinOutput{
			{Value: sco.Value.Sub(types.SiacoinPrecision.Mul64(2)), UnlockHash: types.UnlockHash{}},
			{Value: types.SiacoinPrecision, UnlockHash: types.InitialFoundationUnlockHash},
			{Value: types.SiacoinPrecision, UnlockHash: types.InitialFoundationFailsafeUnlockHash},
		},
		ArbitraryData: [][]byte{encoding.MarshalAll(types.SpecifierFoundation, types.FoundationUnlockHashUpdate{
			NewPrimary:  types.UnlockHash{'v', 'o', 'i', 'd'},
			NewFailsafe: types.InitialFoundationFailsafeUnlockHash,
		})},
		TransactionSignatures: make([]types.TransactionSignature, primaryUC.SignaturesRequired),
	}
	for i := range txn.TransactionSignatures {
		txn.TransactionSignatures[i].ParentID = crypto.Hash(scoid)
		txn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		txn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(txn.SigHash(i, cst.cs.Height()), primaryKeys[i])
		txn.TransactionSignatures[i].Signature = sig[:]
	}
	if err := mineTxn(txn); err != nil {
		t.Fatal(err)
	}
	primarySCOID := txn.SiacoinOutputID(1)
	failsafeSCOID := txn.SiacoinOutputID(2)

	// Attempt to change the primary address back.
	txn = types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         primarySCOID,
			UnlockConditions: primaryUC,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      types.SiacoinPrecision,
			UnlockHash: types.UnlockHash{},
		}},
		ArbitraryData: [][]byte{encoding.MarshalAll(types.SpecifierFoundation, types.FoundationUnlockHashUpdate{
			NewPrimary:  types.InitialFoundationUnlockHash,
			NewFailsafe: types.InitialFoundationFailsafeUnlockHash,
		})},
		TransactionSignatures: make([]types.TransactionSignature, primaryUC.SignaturesRequired),
	}
	for i := range txn.TransactionSignatures {
		txn.TransactionSignatures[i].ParentID = crypto.Hash(primarySCOID)
		txn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		txn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(txn.SigHash(i, cst.cs.Height()), primaryKeys[i])
		txn.TransactionSignatures[i].Signature = sig[:]
	}
	if err := mineTxn(txn); err == nil {
		t.Fatal("expected error, got nil")
	}

	// In the next transaction, we will successfully change the primary back
	// using the failsafe. Before we do that, mine a few subsidies. These
	// subsidies will all be sent to the void, but once the primary is updated,
	// they will be transferred back to the primary.
	outputHeight += 3 * types.FoundationSubsidyFrequency
	for cst.cs.Height() < outputHeight+types.MaturityDelay {
		if _, err := cst.miner.AddBlock(); err != nil {
			t.Fatal(err)
		}
	}

	// Change the primary back using the failsafe. Note that, rather than
	// recreate the entire transaction, we simply replace the input and the
	// signatures.
	failsafeUC, failsafeKeys := types.GenerateDeterministicMultisig(3, 5, types.InitialFoundationFailsafeTestingSalt)
	txn.SiacoinInputs[0] = types.SiacoinInput{
		ParentID:         failsafeSCOID,
		UnlockConditions: failsafeUC,
	}
	txn.TransactionSignatures = make([]types.TransactionSignature, failsafeUC.SignaturesRequired)
	for i := range txn.TransactionSignatures {
		txn.TransactionSignatures[i].ParentID = crypto.Hash(failsafeSCOID)
		txn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		txn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(txn.SigHash(i, cst.cs.Height()), failsafeKeys[i])
		txn.TransactionSignatures[i].Signature = sig[:]
	}
	if err := mineTxn(txn); err != nil {
		t.Fatal(err)
	}
	// Confirm that the update was applied.
	if p, f := cst.cs.FoundationUnlockHashes(); p != types.InitialFoundationUnlockHash || f != types.InitialFoundationFailsafeUnlockHash {
		t.Fatal("transaction did not reset foundation unlock hashes")
	}

	// The previous subsidies should have been transferred to the primary.
	scoid, sco, ok = foundationOutput(outputHeight)
	if !ok {
		t.Fatal("output should exist")
	} else if sco.UnlockHash != types.InitialFoundationUnlockHash {
		t.Fatal("output should have been transferred")
	}

	// Confirm that we can actually spend a transferred subsidy.
	txn = types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         scoid,
			UnlockConditions: primaryUC,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      sco.Value,
			UnlockHash: types.UnlockHash{},
		}},
		TransactionSignatures: make([]types.TransactionSignature, primaryUC.SignaturesRequired),
	}
	for i := range txn.TransactionSignatures {
		txn.TransactionSignatures[i].ParentID = crypto.Hash(scoid)
		txn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		txn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(txn.SigHash(i, cst.cs.Height()), primaryKeys[i])
		txn.TransactionSignatures[i].Signature = sig[:]
	}
	if err := mineTxn(txn); err != nil {
		t.Fatal(err)
	}

	// Sign and submit a transaction that contains multiple updates. Only the
	// first should be applied.
	scoid, sco, ok = foundationOutput(outputHeight - types.FoundationSubsidyFrequency)
	if !ok {
		t.Fatal("subsidy should exist")
	}
	txn = types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         scoid,
			UnlockConditions: primaryUC,
		}},
		SiacoinOutputs: []types.SiacoinOutput{
			{Value: sco.Value.Sub(types.SiacoinPrecision.Mul64(2)), UnlockHash: types.UnlockHash{}},
			{Value: types.SiacoinPrecision, UnlockHash: types.InitialFoundationUnlockHash},
			{Value: types.SiacoinPrecision, UnlockHash: types.InitialFoundationFailsafeUnlockHash},
		},
		ArbitraryData: [][]byte{
			encoding.MarshalAll(types.SpecifierFoundation, types.FoundationUnlockHashUpdate{
				NewPrimary:  types.UnlockHash{'v', 'o', 'i', 'd'},
				NewFailsafe: types.UnlockHash{'v', 'o', 'i', 'd'},
			}),
			encoding.MarshalAll(types.SpecifierFoundation, types.FoundationUnlockHashUpdate{
				NewPrimary:  types.UnlockHash{'i', 'g', 'n', 'o', 'r', 'e'},
				NewFailsafe: types.UnlockHash{'i', 'g', 'n', 'o', 'r', 'e'},
			}),
			encoding.MarshalAll(types.SpecifierFoundation, types.FoundationUnlockHashUpdate{
				NewPrimary:  types.UnlockHash{'i', 'g', 'n', 'o', 'r', 'e'},
				NewFailsafe: types.UnlockHash{'i', 'g', 'n', 'o', 'r', 'e'},
			}),
		},
		TransactionSignatures: make([]types.TransactionSignature, primaryUC.SignaturesRequired),
	}
	for i := range txn.TransactionSignatures {
		txn.TransactionSignatures[i].ParentID = crypto.Hash(scoid)
		txn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		txn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(txn.SigHash(i, cst.cs.Height()), primaryKeys[i])
		txn.TransactionSignatures[i].Signature = sig[:]
	}
	if err := mineTxn(txn); err != nil {
		t.Fatal(err)
	}
	// Confirm that the update was applied.
	if p, f := cst.cs.FoundationUnlockHashes(); p != (types.UnlockHash{'v', 'o', 'i', 'd'}) || f != (types.UnlockHash{'v', 'o', 'i', 'd'}) {
		t.Fatal("transaction did not correctly update foundation unlock hashes")
	}
}
