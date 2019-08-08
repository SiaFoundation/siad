package siafile

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// dummyEntry wraps a SiaFile into a siaFileSetEntry.
func dummyEntry(s *SiaFile) *siaFileSetEntry {
	return &siaFileSetEntry{
		SiaFile: s,
		staticSiaFileSet: &SiaFileSet{
			staticSiaFileDir: filepath.Dir(s.SiaFilePath()),
			siaFileMap:       make(map[SiafileUID]*siaFileSetEntry),
			siapathToUID:     make(map[modules.SiaPath]SiafileUID),
			wal:              nil,
		},
		threadMap: make(map[uint64]threadInfo),
	}
}

// randomChunk is a helper method for testing that creates a random chunk.
func randomChunk() chunk {
	numPieces := 30
	chunk := chunk{}
	chunk.Pieces = make([][]piece, numPieces)
	fastrand.Read(chunk.ExtensionInfo[:])

	// Add 0-3 pieces for each pieceIndex within the file.
	for pieceIndex := range chunk.Pieces {
		n := fastrand.Intn(4) // [0;3]
		// Create and add n pieces at pieceIndex i.
		for i := 0; i < n; i++ {
			var piece piece
			piece.HostTableOffset = uint32(fastrand.Intn(100))
			fastrand.Read(piece.MerkleRoot[:])
			chunk.Pieces[pieceIndex] = append(chunk.Pieces[pieceIndex], piece)
		}
	}
	return chunk
}

// randomPiece is a helper method for testing that creates a random piece.
func randomPiece() piece {
	var piece piece
	piece.HostTableOffset = uint32(fastrand.Intn(100))
	fastrand.Read(piece.MerkleRoot[:])
	return piece
}

// setCombinedChunkOfTestFile adds one or two Combined chunks to a SiaFile for
// tests to be able to use a SiaFile that already has its partial chunk
// contained within a combined chunk. If the SiaFile doesn't have a partial
// chunk, this is a no-op. The combined chunk will be stored in the provided
// 'dir'.
func setCombinedChunkOfTestFile(sf *SiaFile) error {
	return setCustomCombinedChunkOfTestFile(sf, fastrand.Intn(2)+1)
}

// setCustomCombinedChunkOfTestFile sets either 1 or 2 combined chunks of a
// SiaFile for testing and changes its status to completed.
func setCustomCombinedChunkOfTestFile(sf *SiaFile, numCombinedChunks int) error {
	if numCombinedChunks != 1 && numCombinedChunks != 2 {
		return errors.New("numCombinedChunks should be 1 or 2")
	}
	partialChunkSize := sf.Size() % sf.ChunkSize()
	if partialChunkSize == 0 {
		// no partial chunk
		return nil
	}
	var combinedChunks []modules.CombinedChunk
	for i := 0; i < numCombinedChunks; i++ {
		combinedChunks = append(combinedChunks, modules.CombinedChunk{
			ChunkID:          modules.CombinedChunkID(hex.EncodeToString(fastrand.Bytes(16))),
			HasPartialsChunk: false,
		})
	}
	var err error
	if numCombinedChunks == 1 {
		combinedChunks[0].Offset = 0
		combinedChunks[0].Length = partialChunkSize
		err = sf.SetCombinedChunk(combinedChunks, nil)
	} else if numCombinedChunks == 2 {
		combinedChunks[0].Offset = sf.ChunkSize() - 1
		combinedChunks[0].Length = 1
		combinedChunks[0].Offset = 0
		combinedChunks[0].Length = partialChunkSize - 1
		err = sf.SetCombinedChunk(combinedChunks, nil)
	}
	if err != nil {
		return err
	}
	// Force the status to completed.
	for i := 0; i < numCombinedChunks; i++ {
		err = errors.Compose(err, sf.SetChunkStatusCompleted(uint64(i)))
	}
	return err
}

// TestFileNumChunks checks the numChunks method of the file type.
func TestFileNumChunks(t *testing.T) {
	fileSize := func(numSectors uint64) uint64 {
		return numSectors*modules.SectorSize + uint64(fastrand.Intn(int(modules.SectorSize)))
	}
	// Since the pieceSize is 'random' now we test a variety of random inputs.
	tests := []struct {
		fileSize   uint64
		dataPieces int
	}{
		{fileSize(10), 10},
		{fileSize(50), 10},
		{fileSize(100), 10},

		{fileSize(11), 10},
		{fileSize(51), 10},
		{fileSize(101), 10},

		{fileSize(10), 100},
		{fileSize(50), 100},
		{fileSize(100), 100},

		{fileSize(11), 100},
		{fileSize(51), 100},
		{fileSize(101), 100},

		{0, 10}, // 0-length
	}

	for _, test := range tests {
		// Create erasure-coder
		rsc, _ := NewRSCode(test.dataPieces, 1) // can't use 0
		// Create the file
		siaFilePath, _, source, _, sk, _, _, fileMode := newTestFileParams(1, true)
		f, _, _ := customTestFileAndWAL(siaFilePath, source, rsc, sk, test.fileSize, -1, fileMode)
		// Make sure the file reports the correct pieceSize.
		if f.PieceSize() != modules.SectorSize-f.MasterKey().Type().Overhead() {
			t.Fatal("file has wrong pieceSize for its encryption type")
		}
		// Check that the number of chunks matches the expected number.
		expectedNumChunks := test.fileSize / (f.PieceSize() * uint64(test.dataPieces))
		if expectedNumChunks == 0 && test.fileSize > 0 {
			// There is at least 1 chunk for non 0-byte files.
			expectedNumChunks = 1
		} else if expectedNumChunks%(f.PieceSize()*uint64(test.dataPieces)) != 0 {
			// If it doesn't divide evenly there will be 1 chunk padding.
			expectedNumChunks++
		}
		if f.NumChunks() != expectedNumChunks {
			t.Errorf("Test %v: expected %v, got %v", test, expectedNumChunks, f.NumChunks())
		}
	}
}

// TestFileRedundancy tests that redundancy is correctly calculated for files
// with varying number of filecontracts and erasure code settings.
func TestFileRedundancy(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	nDatas := []int{1, 2, 10}
	neverOffline := make(map[string]bool)
	goodForRenew := make(map[string]bool)
	for i := 0; i < 6; i++ {
		pk := types.SiaPublicKey{Key: []byte{byte(i)}}
		neverOffline[pk.String()] = false
		goodForRenew[pk.String()] = true
	}
	// Create a testDir.
	dir := filepath.Join(os.TempDir(), t.Name())
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0600); err != nil {
		t.Fatal(err)
	}

	for _, nData := range nDatas {
		rsc, _ := NewRSCode(nData, 10)
		siaFilePath, _, source, _, sk, fileSize, numChunks, fileMode := newTestFileParamsWithRC(2, false, rsc)
		f, _, _ := customTestFileAndWAL(siaFilePath, source, rsc, sk, fileSize, numChunks, fileMode)
		// If the file has a partial chunk, fake a combined chunk to make sure we can
		// add a piece to it.
		if err := setCombinedChunkOfTestFile(f); err != nil {
			t.Fatal(err)
		}
		// Test that an empty file has 0 redundancy.
		r, ur, err := f.Redundancy(neverOffline, goodForRenew)
		if err != nil {
			t.Fatal(err)
		}
		if r != 0 || ur != 0 {
			t.Error("expected 0 and 0 redundancy, got", r, ur)
		}
		// Test that a file with 1 host that has a piece for every chunk but
		// one chunk still has a redundancy of 0.
		for i := uint64(0); i < f.NumChunks()-1; i++ {
			err := f.AddPiece(types.SiaPublicKey{Key: []byte{byte(0)}}, i, 0, crypto.Hash{})
			if err != nil {
				t.Fatal(err)
			}
		}
		r, ur, err = f.Redundancy(neverOffline, goodForRenew)
		if err != nil {
			t.Fatal(err)
		}
		if r != 0 || ur != 0 {
			t.Error("expected 0 and 0 redundancy, got", r, ur)
		}
		// Test that adding another host with a piece for every chunk but one
		// chunk still results in a file with redundancy 0.
		for i := uint64(0); i < f.NumChunks()-1; i++ {
			err := f.AddPiece(types.SiaPublicKey{Key: []byte{byte(1)}}, i, 1, crypto.Hash{})
			if err != nil {
				t.Fatal(err)
			}
		}
		r, ur, err = f.Redundancy(neverOffline, goodForRenew)
		if err != nil {
			t.Fatal(err)
		}
		if r != 0 || ur != 0 {
			t.Error("expected 0 and 0 redundancy, got", r, ur)
		}
		// Test that adding a file contract with a piece for the missing chunk
		// results in a file with redundancy > 0 && <= 1.
		err = f.AddPiece(types.SiaPublicKey{Key: []byte{byte(2)}}, f.NumChunks()-1, 0, crypto.Hash{})
		if err != nil {
			t.Fatal(err)
		}
		// 1.0 / MinPieces because the chunk with the least number of pieces has 1 piece.
		expectedR := 1.0 / float64(f.ErasureCode().MinPieces())
		r, ur, err = f.Redundancy(neverOffline, goodForRenew)
		if err != nil {
			t.Fatal(err)
		}
		if r != expectedR || ur != expectedR {
			t.Errorf("expected %f redundancy, got %f %f", expectedR, r, ur)
		}
		// Test that adding a file contract that has erasureCode.MinPieces() pieces
		// per chunk for all chunks results in a file with redundancy > 1.
		for iChunk := uint64(0); iChunk < f.NumChunks(); iChunk++ {
			for iPiece := uint64(1); iPiece < uint64(f.ErasureCode().MinPieces()); iPiece++ {
				err := f.AddPiece(types.SiaPublicKey{Key: []byte{byte(3)}}, iChunk, iPiece, crypto.Hash{})
				if err != nil {
					t.Fatal(err)
				}
			}
			err := f.AddPiece(types.SiaPublicKey{Key: []byte{byte(4)}}, iChunk, uint64(f.ErasureCode().MinPieces()), crypto.Hash{})
			if err != nil {
				t.Fatal(err)
			}
		}
		// 1+MinPieces / MinPieces because the chunk with the least number of pieces has 1+MinPieces pieces.
		expectedR = float64(1+f.ErasureCode().MinPieces()) / float64(f.ErasureCode().MinPieces())
		r, ur, err = f.Redundancy(neverOffline, goodForRenew)
		if err != nil {
			t.Fatal(err)
		}
		if r != expectedR || ur != expectedR {
			t.Errorf("expected %f redundancy, got %f", expectedR, r)
		}

		// verify offline file contracts are not counted in the redundancy
		for iChunk := uint64(0); iChunk < f.NumChunks(); iChunk++ {
			for iPiece := uint64(0); iPiece < uint64(f.ErasureCode().MinPieces()); iPiece++ {
				err := f.AddPiece(types.SiaPublicKey{Key: []byte{byte(5)}}, iChunk, iPiece, crypto.Hash{})
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		specificOffline := make(map[string]bool)
		for pk := range goodForRenew {
			specificOffline[pk] = false
		}
		specificOffline[string(byte(5))] = true
		r, ur, err = f.Redundancy(specificOffline, goodForRenew)
		if err != nil {
			t.Fatal(err)
		}
		if r != expectedR || ur != expectedR {
			t.Errorf("expected redundancy to ignore offline file contracts, wanted %f got %f", expectedR, r)
		}
	}
}

// TestFileHealth tests that the health of the file is correctly calculated.
//
// Health is equal to (targetParityPieces - actualParityPieces)/targetParityPieces
func TestFileHealth(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a Zero byte file
	rsc, _ := NewRSCode(10, 20)
	siaFilePath, _, source, _, sk, _, _, fileMode := newTestFileParams(1, true)
	zeroFile, _, _ := customTestFileAndWAL(siaFilePath, source, rsc, sk, 0, 0, fileMode)

	// Create offline map
	offlineMap := make(map[string]bool)
	goodForRenewMap := make(map[string]bool)

	// Confirm the health is correct
	health, stuckHealth, userHealth, userStuckHealth, numStuckChunks := zeroFile.Health(offlineMap, goodForRenewMap)
	if health != 0 {
		t.Fatal("Expected health to be 0 but was", health)
	}
	if stuckHealth != 0 {
		t.Fatal("Expected stuck health to be 0 but was", stuckHealth)
	}
	if userHealth != 0 {
		t.Fatal("Expected userHealth to be 0 but was", health)
	}
	if userStuckHealth != 0 {
		t.Fatal("Expected user stuck health to be 0 but was", stuckHealth)
	}
	if numStuckChunks != 0 {
		t.Fatal("Expected no stuck chunks but found", numStuckChunks)
	}

	// Create File with 1 chunk
	siaFilePath, _, source, _, sk, _, _, fileMode = newTestFileParams(1, true)
	f, _, _ := customTestFileAndWAL(siaFilePath, source, rsc, sk, 100, 1, fileMode)

	// Check file health, since there are no pieces in the chunk yet no good
	// pieces will be found resulting in a health of 1.5 with the erasure code
	// settings of 10/30. Since there are no stuck chunks the stuckHealth of the
	// file should be 0
	//
	// 1 - ((0 - 10) / 20)
	health, stuckHealth, _, _, _ = f.Health(offlineMap, goodForRenewMap)
	if health != 1.5 {
		t.Fatalf("Health of file not as expected, got %v expected 1.5", health)
	}
	if stuckHealth != float64(0) {
		t.Fatalf("Stuck Health of file not as expected, got %v expected 0", stuckHealth)
	}

	// Add good pieces to first Piece Set
	if err := setCustomCombinedChunkOfTestFile(f, 1); err != nil {
		t.Fatal(err)
	}
	if f.CombinedChunks()[0].Status != CombinedChunkStatusCompleted {
		t.Fatal("File has wrong combined chunk status")
	}
	for i := 0; i < 2; i++ {
		host := fmt.Sprintln("host", i)
		spk := types.SiaPublicKey{}
		spk.LoadString(host)
		offlineMap[spk.String()] = false
		goodForRenewMap[spk.String()] = true
		if err := f.AddPiece(spk, 0, 0, crypto.Hash{}); err != nil {
			t.Fatal(err)
		}
	}

	// Check health, even though two pieces were added the health should be 1.45
	// since the two good pieces were added to the same pieceSet
	//
	// 1 - ((1 - 10) / 20)
	health, _, _, _, _ = f.Health(offlineMap, goodForRenewMap)
	if health != 1.45 {
		t.Fatalf("Health of file not as expected, got %v expected 1.45", health)
	}

	// Add one good pieces to second piece set, confirm health is now 1.40.
	host := fmt.Sprintln("host", 0)
	spk := types.SiaPublicKey{}
	spk.LoadString(host)
	offlineMap[spk.String()] = false
	goodForRenewMap[spk.String()] = true
	if err := f.AddPiece(spk, 0, 1, crypto.Hash{}); err != nil {
		t.Fatal(err)
	}
	health, _, _, _, _ = f.Health(offlineMap, goodForRenewMap)
	if health != 1.40 {
		t.Fatalf("Health of file not as expected, got %v expected 1.40", health)
	}

	// Add another good pieces to second piece set, confirm health is still 1.40.
	host = fmt.Sprintln("host", 1)
	spk = types.SiaPublicKey{}
	spk.LoadString(host)
	offlineMap[spk.String()] = false
	goodForRenewMap[spk.String()] = true
	if err := f.AddPiece(spk, 0, 1, crypto.Hash{}); err != nil {
		t.Fatal(err)
	}
	health, _, _, _, _ = f.Health(offlineMap, goodForRenewMap)
	if health != 1.40 {
		t.Fatalf("Health of file not as expected, got %v expected 1.40", health)
	}

	// Mark chunk as stuck
	err := f.SetStuck(0, true)
	if err != nil {
		t.Fatal(err)
	}
	health, stuckHealth, _, _, numStuckChunks = f.Health(offlineMap, goodForRenewMap)
	// Health should now be 0 since there are no unstuck chunks
	if health != 0 {
		t.Fatalf("Health of file not as expected, got %v expected 0", health)
	}
	// Stuck Health should now be 1.4
	if stuckHealth != 1.40 {
		t.Fatalf("Stuck Health of file not as expected, got %v expected 1.40", stuckHealth)
	}
	// There should be 1 stuck chunk
	if numStuckChunks != 1 {
		t.Fatalf("Expected 1 stuck chunk but found %v", numStuckChunks)
	}

	// Create File with 2 chunks
	siaFilePath, _, source, _, sk, _, _, fileMode = newTestFileParams(1, true)
	f, _, _ = customTestFileAndWAL(siaFilePath, source, rsc, sk, 5e4, 2, fileMode)
	if err != nil {
		t.Fatal(err)
	}

	// Create offline map
	offlineMap = make(map[string]bool)
	goodForRenewMap = make(map[string]bool)

	// Check file health, since there are no pieces in the chunk yet no good
	// pieces will be found resulting in a health of 1.5
	health, _, _, _, _ = f.Health(offlineMap, goodForRenewMap)
	if health != 1.5 {
		t.Fatalf("Health of file not as expected, got %v expected 1.5", health)
	}

	// Add good pieces to the first chunk
	for i := 0; i < 4; i++ {
		host := fmt.Sprintln("host", i)
		spk := types.SiaPublicKey{}
		spk.LoadString(host)
		offlineMap[spk.String()] = false
		goodForRenewMap[spk.String()] = true
		if err := f.AddPiece(spk, 0, uint64(i%2), crypto.Hash{}); err != nil {
			t.Fatal(err)
		}
	}

	// Check health, should still be 1.5 because other chunk doesn't have any
	// good pieces
	health, stuckHealth, _, _, _ = f.Health(offlineMap, goodForRenewMap)
	if health != 1.5 {
		t.Fatalf("Health of file not as expected, got %v expected 1.5", health)
	}

	// Add good pieces to second chunk, confirm health is 1.40 since both chunks
	// have 2 good pieces.
	if err := setCustomCombinedChunkOfTestFile(f, 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		host := fmt.Sprintln("host", i)
		spk := types.SiaPublicKey{}
		spk.LoadString(host)
		offlineMap[spk.String()] = false
		goodForRenewMap[spk.String()] = true
		if err := f.AddPiece(spk, 1, uint64(i%2), crypto.Hash{}); err != nil {
			t.Fatal(err)
		}
	}
	health, _, _, _, _ = f.Health(offlineMap, goodForRenewMap)
	if health != 1.40 {
		t.Fatalf("Health of file not as expected, got %v expected 1.40", health)
	}

	// Mark second chunk as stuck
	err = f.SetStuck(1, true)
	if err != nil {
		t.Fatal(err)
	}
	health, stuckHealth, _, _, numStuckChunks = f.Health(offlineMap, goodForRenewMap)
	// Since both chunks have the same health, the file health and the file stuck health should be the same
	if health != 1.40 {
		t.Fatalf("Health of file not as expected, got %v expected 1.40", health)
	}
	if stuckHealth != 1.40 {
		t.Fatalf("Stuck Health of file not as expected, got %v expected 1.4", stuckHealth)
	}
	// Check health, verify there is 1 stuck chunk
	if numStuckChunks != 1 {
		t.Fatalf("Expected 1 stuck chunk but found %v", numStuckChunks)
	}
}

// TestGrowNumChunks is a unit test for the SiaFile's GrowNumChunks method.
func TestGrowNumChunks(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a blank file.
	siaFilePath, _, source, rc, sk, fileSize, numChunks, fileMode := newTestFileParams(1, false)
	sf, wal, _ := customTestFileAndWAL(siaFilePath, source, rc, sk, fileSize, numChunks, fileMode)
	expectedChunks := sf.NumChunks()
	expectedSize := sf.Size()

	// Declare a check method.
	checkFile := func(sf *SiaFile, numChunks, size uint64) {
		if numChunks != sf.NumChunks() {
			t.Fatalf("Expected %v chunks but was %v", numChunks, sf.NumChunks())
		}
		if size != sf.Size() {
			t.Fatalf("Expected size to be %v but was %v", size, sf.Size())
		}
	}

	// Increase the size of the file by 1 chunk.
	expectedChunks++
	expectedSize += sf.ChunkSize()
	err := sf.GrowNumChunks(expectedChunks)
	if err != nil {
		t.Fatal(err)
	}
	// Check the file after growing the chunks.
	checkFile(sf, expectedChunks, expectedSize)
	// Load the file from disk again to also check that persistence works.
	sf, err = LoadSiaFile(sf.siaFilePath, wal)
	if err != nil {
		t.Fatal(err)
	}
	// Check that size and chunks still match.
	checkFile(sf, expectedChunks, expectedSize)

	// Call GrowNumChunks with the same argument again. This should be a no-op.
	err = sf.GrowNumChunks(expectedChunks)
	if err != nil {
		t.Fatal(err)
	}
	// Check the file after growing the chunks.
	checkFile(sf, expectedChunks, expectedSize)
	// Load the file from disk again to also check that no wrong persistence
	// happened.
	sf, err = LoadSiaFile(sf.siaFilePath, wal)
	if err != nil {
		t.Fatal(err)
	}
	// Check that size and chunks still match.
	checkFile(sf, expectedChunks, expectedSize)

	// Grow the file by 2 chunks to see if multiple chunks also work.
	expectedChunks += 2
	expectedSize += 2 * sf.ChunkSize()
	err = sf.GrowNumChunks(expectedChunks)
	if err != nil {
		t.Fatal(err)
	}
	// Check the file after growing the chunks.
	checkFile(sf, expectedChunks, expectedSize)
	// Load the file from disk again to also check that persistence works.
	sf, err = LoadSiaFile(sf.siaFilePath, wal)
	if err != nil {
		t.Fatal(err)
	}
	// Check that size and chunks still match.
	checkFile(sf, expectedChunks, expectedSize)
}

// TestPruneHosts is a unit test for the pruneHosts method.
func TestPruneHosts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a siafile without partial chunk since partial chunk.
	siaFilePath, _, source, rc, sk, fileSize, numChunks, fileMode := newTestFileParams(1, false)
	sf, _, _ := customTestFileAndWAL(siaFilePath, source, rc, sk, fileSize, numChunks, fileMode)

	// Add 3 random hostkeys to the file.
	sf.addRandomHostKeys(3)

	// Save changes to disk.
	updates, err := sf.saveHeaderUpdates()
	if err != nil {
		t.Fatal(err)
	}
	if err := sf.createAndApplyTransaction(updates...); err != nil {
		t.Fatal(err)
	}

	// Add one piece for every host to every pieceSet of the
	for _, hk := range sf.HostPublicKeys() {
		err := sf.iterateChunksReadonly(func(chunk chunk) error {
			for pieceIndex := range chunk.Pieces {
				if err := sf.AddPiece(hk, uint64(chunk.Index), uint64(pieceIndex), crypto.Hash{}); err != nil {
					t.Fatal(err)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Mark hostkeys 0 and 2 as unused.
	sf.pubKeyTable[0].Used = false
	sf.pubKeyTable[2].Used = false
	remainingKey := sf.pubKeyTable[1]

	// Prune the file.
	updates, err = sf.pruneHosts()
	if err != nil {
		t.Fatal(err)
	}
	if err := sf.createAndApplyTransaction(updates...); err != nil {
		t.Fatal(err)
	}

	// Check that there is only a single key left.
	if len(sf.pubKeyTable) != 1 {
		t.Fatalf("There should only be 1 key left but was %v", len(sf.pubKeyTable))
	}
	// The last key should be the correct one.
	if !reflect.DeepEqual(remainingKey, sf.pubKeyTable[0]) {
		t.Fatal("Remaining key doesn't match")
	}
	// Loop over all the pieces and make sure that the pieces with missing
	// hosts were pruned and that the remaining pieces have the correct offset
	// now.
	err = sf.iterateChunksReadonly(func(chunk chunk) error {
		for _, pieceSet := range chunk.Pieces {
			if len(pieceSet) != 1 {
				t.Fatalf("Expected 1 piece in the set but was %v", len(pieceSet))
			}
			// The HostTableOffset should always be 0 since the keys at index 0
			// and 2 were pruned which means that index 1 is now index 0.
			for _, piece := range pieceSet {
				if piece.HostTableOffset != 0 {
					t.Fatalf("HostTableOffset should be 0 but was %v", piece.HostTableOffset)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestNumPieces tests the chunk's numPieces method.
func TestNumPieces(t *testing.T) {
	// create a random chunk.
	chunk := randomChunk()

	// get the number of pieces of the chunk.
	totalPieces := 0
	for _, pieceSet := range chunk.Pieces {
		totalPieces += len(pieceSet)
	}

	// compare it to the one reported by numPieces.
	if totalPieces != chunk.numPieces() {
		t.Fatalf("Expected %v pieces but was %v", totalPieces, chunk.numPieces())
	}
}

// TestDefragChunk tests if the defragChunk methods correctly prunes pieces
// from a chunk.
func TestDefragChunk(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// Get a blank
	sf, _, _ := newBlankTestFileAndWAL(2) // make sure we have 1 full chunk at the beginning of sf.fullChunks

	// Use the first chunk of the file for testing.
	chunk, err := sf.chunk(0)
	if err != nil {
		t.Fatal(err)
	}

	// Add 100 pieces to each set of pieces, all belonging to the same unused
	// host.
	sf.pubKeyTable = append(sf.pubKeyTable, HostPublicKey{Used: false})
	for i := range chunk.Pieces {
		for j := 0; j < 100; j++ {
			chunk.Pieces[i] = append(chunk.Pieces[i], piece{HostTableOffset: 0})
		}
	}

	// Defrag the chunk. This should remove all the pieces since the host is
	// unused.
	sf.defragChunk(&chunk)
	if chunk.numPieces() != 0 {
		t.Fatalf("chunk should have 0 pieces after defrag but was %v", chunk.numPieces())
	}

	// Do the same thing again, but this time the host is marked as used.
	sf.pubKeyTable[0].Used = true
	for i := range chunk.Pieces {
		for j := 0; j < 100; j++ {
			chunk.Pieces[i] = append(chunk.Pieces[i], piece{HostTableOffset: 0})
		}
	}

	// Defrag the chunk.
	maxChunkSize := int64(sf.staticMetadata.StaticPagesPerChunk) * pageSize
	maxPieces := (maxChunkSize - marshaledChunkOverhead) / marshaledPieceSize
	maxPiecesPerSet := maxPieces / int64(len(chunk.Pieces))
	sf.defragChunk(&chunk)

	// The chunk should be smaller than maxChunkSize.
	if chunkSize := marshaledChunkSize(chunk.numPieces()); chunkSize > maxChunkSize {
		t.Errorf("chunkSize is too big %v > %v", chunkSize, maxChunkSize)
	}
	// The chunk should have less than maxPieces pieces.
	if int64(chunk.numPieces()) > maxPieces {
		t.Errorf("chunk should have <= %v pieces after defrag but was %v",
			maxPieces, chunk.numPieces())
	}
	// The chunk should have numPieces * maxPiecesPerSet pieces.
	if expectedPieces := int64(sf.ErasureCode().NumPieces()) * maxPiecesPerSet; expectedPieces != int64(chunk.numPieces()) {
		t.Errorf("chunk should have %v pieces but was %v", expectedPieces, chunk.numPieces())
	}
	// Every set of pieces should have maxPiecesPerSet pieces.
	for i, pieceSet := range chunk.Pieces {
		if int64(len(pieceSet)) != maxPiecesPerSet {
			t.Errorf("pieceSet%v length is %v which is greater than %v",
				i, len(pieceSet), maxPiecesPerSet)
		}
	}

	// Create a new file with 2 used hosts and 1 unused one. This file should
	// use 2 pages per chunk.
	sf, _, _ = newBlankTestFileAndWAL(2) // make sure we have 1 full chunk at the beginning of the file.
	sf.staticMetadata.StaticPagesPerChunk = 2
	sf.pubKeyTable = append(sf.pubKeyTable, HostPublicKey{Used: true})
	sf.pubKeyTable = append(sf.pubKeyTable, HostPublicKey{Used: true})
	sf.pubKeyTable = append(sf.pubKeyTable, HostPublicKey{Used: false})
	sf.pubKeyTable[0].PublicKey.Key = fastrand.Bytes(crypto.EntropySize)
	sf.pubKeyTable[1].PublicKey.Key = fastrand.Bytes(crypto.EntropySize)
	sf.pubKeyTable[2].PublicKey.Key = fastrand.Bytes(crypto.EntropySize)

	// Save the above changes to disk to avoid failing sanity checks when
	// calling AddPiece.
	updates, err := sf.saveHeaderUpdates()
	if err != nil {
		t.Fatal(err)
	}
	if err := sf.createAndApplyTransaction(updates...); err != nil {
		t.Fatal(err)
	}

	// Add 500 pieces to the first chunk of the file, randomly belonging to
	// any of the 3 hosts. This should never produce an error.
	var duration time.Duration
	for i := 0; i < 50; i++ {
		chunk, err := sf.chunk(0)
		if err != nil {
			t.Fatal(err)
		}
		pk := sf.pubKeyTable[fastrand.Intn(len(sf.pubKeyTable))].PublicKey
		pieceIndex := fastrand.Intn(len(chunk.Pieces))
		before := time.Now()
		if err := sf.AddPiece(pk, 0, uint64(pieceIndex), crypto.Hash{}); err != nil {
			t.Fatal(err)
		}
		duration += time.Since(before)
	}

	// Save the file to disk again to make sure cached fields are persisted.
	updates, err = sf.saveHeaderUpdates()
	if err != nil {
		t.Fatal(err)
	}
	if err := sf.createAndApplyTransaction(updates...); err != nil {
		t.Fatal(err)
	}

	// Finally load the file from disk again and compare it to the original.
	sf2, err := LoadSiaFile(sf.siaFilePath, sf.wal)
	if err != nil {
		t.Fatal(err)
	}
	// Compare the files.
	if err := equalFiles(sf, sf2); err != nil {
		t.Fatal(err)
	}
}

// TestChunkHealth probes the chunkHealth method
func TestChunkHealth(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// Get a blank siafile with at least 3 chunks.
	sf, _, _ := newBlankTestFileAndWAL(3)
	rc := sf.ErasureCode()

	// Create offline map
	offlineMap := make(map[string]bool)
	goodForRenewMap := make(map[string]bool)

	// Check and Record file health of initialized file
	fileHealth, _, _, _, _ := sf.Health(offlineMap, goodForRenewMap)
	initHealth := float64(1) - (float64(0-rc.MinPieces()) / float64(rc.NumPieces()-rc.MinPieces()))
	if fileHealth != initHealth {
		t.Fatalf("Expected file to be %v, got %v", initHealth, fileHealth)
	}

	// Since we are using a pre set offlineMap, all the chunks should have the
	// same health as the file
	err := sf.iterateChunksReadonly(func(chunk chunk) error {
		chunkHealth, _, err := sf.chunkHealth(chunk, offlineMap, goodForRenewMap)
		if err != nil {
			return err
		}
		if chunkHealth != fileHealth {
			t.Log("ChunkHealth:", chunkHealth)
			t.Log("FileHealth:", fileHealth)
			t.Fatal("Expected file and chunk to have same health")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add good piece to first chunk
	host := fmt.Sprintln("host_0")
	spk := types.SiaPublicKey{}
	spk.LoadString(host)
	offlineMap[spk.String()] = false
	goodForRenewMap[spk.String()] = true
	if err := setCombinedChunkOfTestFile(sf); err != nil {
		t.Fatal(err)
	}
	if err := sf.AddPiece(spk, 0, 0, crypto.Hash{}); err != nil {
		t.Fatal(err)
	}

	// Chunk at index 0 should now have a health of 1 higher than before
	chunk, err := sf.chunk(0)
	if err != nil {
		t.Fatal(err)
	}
	newHealth := float64(1) - (float64(1-rc.MinPieces()) / float64(rc.NumPieces()-rc.MinPieces()))
	ch, _, err := sf.chunkHealth(chunk, offlineMap, goodForRenewMap)
	if err != nil {
		t.Fatal(err)
	}
	if ch != newHealth {
		t.Fatalf("Expected chunk health to be %v, got %v", newHealth, ch)
	}

	// Chunk at index 1 should still have lower health
	chunk, err = sf.chunk(1)
	if err != nil {
		t.Fatal(err)
	}
	ch, _, err = sf.chunkHealth(chunk, offlineMap, goodForRenewMap)
	if err != nil {
		t.Fatal(err)
	}
	if ch != fileHealth {
		t.Fatalf("Expected chunk health to be %v, got %v", fileHealth, ch)
	}

	// Add good piece to second chunk
	host = fmt.Sprintln("host_1")
	spk = types.SiaPublicKey{}
	spk.LoadString(host)
	offlineMap[spk.String()] = false
	goodForRenewMap[spk.String()] = true
	if err := sf.AddPiece(spk, 1, 0, crypto.Hash{}); err != nil {
		t.Fatal(err)
	}

	// Chunk at index 1 should now have a health of 1 higher than before
	chunk, err = sf.chunk(1)
	if err != nil {
		t.Fatal(err)
	}
	ch, _, err = sf.chunkHealth(chunk, offlineMap, goodForRenewMap)
	if err != nil {
		t.Fatal(err)
	}
	if ch != newHealth {
		t.Fatalf("Expected chunk health to be %v, got %v", newHealth, ch)
	}

	// Mark Chunk at index 1 as stuck and confirm that doesn't impact the result
	// of chunkHealth
	if err := sf.SetStuck(1, true); err != nil {
		t.Fatal(err)
	}
	chunk, err = sf.chunk(1)
	if err != nil {
		t.Fatal(err)
	}
	ch, _, err = sf.chunkHealth(chunk, offlineMap, goodForRenewMap)
	if err != nil {
		t.Fatal(err)
	}
	if ch != newHealth {
		t.Fatalf("Expected file to be %v, got %v", newHealth, ch)
	}
}

// TestStuckChunks checks to make sure the NumStuckChunks return the expected
// values and that the stuck chunks are persisted properly
func TestStuckChunks(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create siafile
	sf, sfs, err := newTestSiaFileSetWithFile()
	if err != nil {
		t.Fatal(err)
	}

	// Mark every other chunk as stuck
	expectedStuckChunks := 0
	for chunkIndex := 0; chunkIndex < sf.numChunks; chunkIndex++ {
		if (chunkIndex % 2) != 0 {
			continue
		}
		if sf.staticMetadata.HasPartialChunk && len(sf.CombinedChunks()) == 0 && chunkIndex == sf.numChunks-1 {
			continue // not included partial chunk at the end can't be stuck
		}
		if err := sf.SetStuck(uint64(chunkIndex), true); err != nil {
			t.Fatal(err)
		}
		expectedStuckChunks++
	}

	// Check that the total number of stuck chunks is consistent
	numStuckChunks := sf.NumStuckChunks()
	if numStuckChunks != uint64(expectedStuckChunks) {
		t.Fatalf("Wrong number of stuck chunks, got %v expected %v", numStuckChunks, expectedStuckChunks)
	}

	// Close file and confirm it is out of memory
	siaPath := sfs.SiaPath(sf)
	if err = sf.Close(); err != nil {
		t.Fatal(err)
	}
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("File not removed from memory")
	}
	if len(sfs.siapathToUID) != 1 {
		t.Fatal("File not removed from uid map")
	}

	// Load siafile from disk
	sf, err = sfs.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Check that the total number of stuck chunks is consistent
	if numStuckChunks != sf.NumStuckChunks() {
		t.Fatalf("Wrong number of stuck chunks, got %v expected %v", numStuckChunks, sf.NumStuckChunks())
	}

	// Check chunks and Stuck Chunk Table
	err = sf.iterateChunksReadonly(func(chunk chunk) error {
		if sf.staticMetadata.HasPartialChunk && len(sf.staticMetadata.CombinedChunks) == 0 &&
			uint64(chunk.Index) == sf.NumChunks()-1 {
			return nil // partial chunk at the end can't be stuck
		}
		if chunk.Index%2 != 0 {
			if chunk.Stuck {
				t.Fatal("Found stuck chunk when un-stuck chunk was expected")
			}
			return nil
		}
		if !chunk.Stuck {
			t.Fatal("Found un-stuck chunk when stuck chunk was expected")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestUploadedBytes tests that uploadedBytes() returns the expected values for
// total and unique uploaded bytes.
func TestUploadedBytes(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	// Create a new blank test file
	f := newBlankTestFile()
	if err := setCombinedChunkOfTestFile(f); err != nil {
		t.Fatal(err)
	}
	// Add multiple pieces to the first pieceSet of the first piece of the first
	// chunk
	for i := 0; i < 4; i++ {
		err := f.AddPiece(types.SiaPublicKey{}, uint64(0), 0, crypto.Hash{})
		if err != nil {
			t.Fatal(err)
		}
	}
	totalBytes, uniqueBytes, err := f.uploadedBytes()
	if err != nil {
		t.Fatal(err)
	}
	if totalBytes != 4*modules.SectorSize {
		t.Errorf("expected totalBytes to be %v, got %v", 4*modules.SectorSize, totalBytes)
	}
	if uniqueBytes != modules.SectorSize {
		t.Errorf("expected uniqueBytes to be %v, got %v", modules.SectorSize, uniqueBytes)
	}
}

// TestFileUploadProgressPinning verifies that uploadProgress() returns at most
// 100%, even if more pieces have been uploaded,
func TestFileUploadProgressPinning(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	f := newBlankTestFile()
	if err := setCombinedChunkOfTestFile(f); err != nil {
		t.Fatal(err)
	}

	for chunkIndex := uint64(0); chunkIndex < f.NumChunks(); chunkIndex++ {
		for pieceIndex := uint64(0); pieceIndex < uint64(f.ErasureCode().NumPieces()); pieceIndex++ {
			err1 := f.AddPiece(types.SiaPublicKey{Key: []byte{byte(0)}}, chunkIndex, pieceIndex, crypto.Hash{})
			err2 := f.AddPiece(types.SiaPublicKey{Key: []byte{byte(1)}}, chunkIndex, pieceIndex, crypto.Hash{})
			if err := errors.Compose(err1, err2); err != nil {
				t.Fatal(err)
			}
		}
	}
	if f.staticMetadata.CachedUploadProgress != 100 {
		t.Fatal("expected uploadProgress to report 100% but was", f.staticMetadata.CachedUploadProgress)
	}
}

// TestFileExpiration probes the expiration method of the file type.
func TestFileExpiration(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	siaFilePath, _, source, rc, sk, fileSize, numChunks, fileMode := newTestFileParams(1, false)
	f, _, _ := customTestFileAndWAL(siaFilePath, source, rc, sk, fileSize, numChunks, fileMode)
	contracts := make(map[string]modules.RenterContract)
	_ = f.Expiration(contracts)
	if f.staticMetadata.CachedExpiration != 0 {
		t.Error("file with no pieces should report as having no time remaining")
	}
	// Set a combined chunk for the file if necessary.
	if err := setCombinedChunkOfTestFile(f); err != nil {
		t.Fatal(err)
	}
	// Create 3 public keys
	pk1 := types.SiaPublicKey{Key: []byte{0}}
	pk2 := types.SiaPublicKey{Key: []byte{1}}
	pk3 := types.SiaPublicKey{Key: []byte{2}}

	// Add a piece for each key to the file.
	err1 := f.AddPiece(pk1, 0, 0, crypto.Hash{})
	err2 := f.AddPiece(pk2, 0, 1, crypto.Hash{})
	err3 := f.AddPiece(pk3, 0, 2, crypto.Hash{})
	if err := errors.Compose(err1, err2, err3); err != nil {
		t.Fatal(err)
	}

	// Add a contract.
	fc := modules.RenterContract{}
	fc.EndHeight = 100
	contracts[pk1.String()] = fc
	_ = f.Expiration(contracts)
	if f.staticMetadata.CachedExpiration != 100 {
		t.Error("file did not report lowest WindowStart", f.staticMetadata.CachedExpiration)
	}

	// Add a contract with a lower WindowStart.
	fc.EndHeight = 50
	contracts[pk2.String()] = fc
	_ = f.Expiration(contracts)
	if f.staticMetadata.CachedExpiration != 50 {
		t.Error("file did not report lowest WindowStart", f.staticMetadata.CachedExpiration)
	}

	// Add a contract with a higher WindowStart.
	fc.EndHeight = 75
	contracts[pk3.String()] = fc
	_ = f.Expiration(contracts)
	if f.staticMetadata.CachedExpiration != 50 {
		t.Error("file did not report lowest WindowStart", f.staticMetadata.CachedExpiration)
	}
}

// BenchmarkLoadSiaFile benchmarks loading an existing siafile's metadata into
// memory.
func BenchmarkLoadSiaFile(b *testing.B) {
	// Get new file params
	siaFilePath, _, source, _, sk, _, _, fileMode := newTestFileParams(1, false)
	// Create the path to the file.
	dir, _ := filepath.Split(siaFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		b.Fatal(err)
	}
	// Create the file.
	wal, _ := newTestWAL()
	rc, err := NewRSSubCode(10, 20, crypto.SegmentSize)
	if err != nil {
		b.Fatal(err)
	}
	sf, err := New(siaFilePath, source, wal, rc, sk, 1, fileMode, nil, true) // 1 chunk file
	if err != nil {
		b.Fatal(err)
	}
	if err := sf.GrowNumChunks(10); err != nil { // Grow file to 10 chunks
		b.Fatal(err)
	}
	// Add pieces to chunks until every chunk has full redundancy.
	hostKeys := make([]types.SiaPublicKey, rc.NumPieces())
	for i := range hostKeys {
		fastrand.Read(hostKeys[i].Key)
	}
	for pieceIndex := 0; pieceIndex < rc.NumPieces(); pieceIndex++ {
		for chunkIndex := 0; chunkIndex < int(sf.NumChunks()); chunkIndex++ {
			if err := sf.AddPiece(hostKeys[pieceIndex], uint64(chunkIndex), uint64(pieceIndex), crypto.Hash{}); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ResetTimer()
	for loads := 0; loads < b.N; loads++ {
		sf, err = LoadSiaFile(siaFilePath, wal)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRandomChunkWriteSingleThreaded(b *testing.B) {
	benchmarkRandomChunkWrite(1, b)
}
func BenchmarkRandomChunkWriteMultiThreaded(b *testing.B) {
	// 50 seems reasonable since it matches the number of hosts we usually have contracts with
	benchmarkRandomChunkWrite(50, b)
}

// BenchmarkRandomChunkWrite benchmarks writing pieces to random chunks within a
// siafile.
func benchmarkRandomChunkWrite(numThreads int, b *testing.B) {
	// Get new file params
	siaFilePath, _, source, _, sk, _, _, fileMode := newTestFileParams(1, false)
	// Create the path to the file.
	dir, _ := filepath.Split(siaFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		b.Fatal(err)
	}
	// Create the file.
	wal, _ := newTestWAL()
	rc, err := NewRSSubCode(10, 20, crypto.SegmentSize)
	if err != nil {
		b.Fatal(err)
	}
	sf, err := New(siaFilePath, source, wal, rc, sk, 1, fileMode, nil, true) // 1 chunk file
	if err != nil {
		b.Fatal(err)
	}
	if err := sf.GrowNumChunks(100); err != nil { // Grow file to 100 chunks
		b.Fatal(err)
	}
	// Add pieces to random chunks until every chunk has full redundancy.
	var writes uint64
	piecePerm := fastrand.Perm(rc.NumPieces())
	chunkPerm := fastrand.Perm(int(sf.NumChunks()))
	hostKeys := make([]types.SiaPublicKey, rc.NumPieces())
	for i := range hostKeys {
		fastrand.Read(hostKeys[i].Key)
	}
	start := make(chan struct{})
	worker := func() {
		<-start
		for atomic.LoadUint64(&writes) < uint64(b.N) {
			for _, pieceIndex := range piecePerm {
				for _, chunkIndex := range chunkPerm {
					if err := sf.AddPiece(hostKeys[pieceIndex], uint64(chunkIndex), uint64(pieceIndex), crypto.Hash{}); err != nil {
						b.Fatal(err)
					}
					atomic.AddUint64(&writes, 1)
					if atomic.LoadUint64(&writes) >= uint64(b.N) {
						return
					}
				}
			}
		}
	}
	// Spawn worker threads.
	var wg sync.WaitGroup
	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker()
		}()
	}
	// Reset timer and start threads.
	b.ResetTimer()
	close(start)
	wg.Wait()
}

// BenchmarkRandomChunkRead benchmarks reading pieces of a random chunks within
// a siafile.
func BenchmarkRandomChunkRead(b *testing.B) {
	// Get new file params
	siaFilePath, _, source, _, sk, _, _, fileMode := newTestFileParams(1, false)
	// Create the path to the file.
	dir, _ := filepath.Split(siaFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		b.Fatal(err)
	}
	// Create the file.
	wal, _ := newTestWAL()
	rc, err := NewRSSubCode(10, 20, crypto.SegmentSize)
	if err != nil {
		b.Fatal(err)
	}
	sf, err := New(siaFilePath, source, wal, rc, sk, 1, fileMode, nil, true) // 1 chunk file
	if err != nil {
		b.Fatal(err)
	}
	if err := sf.GrowNumChunks(10); err != nil { // Grow file to 10 chunks
		b.Fatal(err)
	}
	// Add pieces to chunks until every chunk has full redundancy.
	hostKeys := make([]types.SiaPublicKey, rc.NumPieces())
	for i := range hostKeys {
		fastrand.Read(hostKeys[i].Key)
	}
	for pieceIndex := 0; pieceIndex < rc.NumPieces(); pieceIndex++ {
		for chunkIndex := 0; chunkIndex < int(sf.NumChunks()); chunkIndex++ {
			if err := sf.AddPiece(hostKeys[pieceIndex], uint64(chunkIndex), uint64(pieceIndex), crypto.Hash{}); err != nil {
				b.Fatal(err)
			}
		}
	}
	// Read random pieces
	reads := 0
	chunkPerm := fastrand.Perm(int(sf.NumChunks()))
	b.ResetTimer()
	for reads < b.N {
		for _, chunkIndex := range chunkPerm {
			if _, err := sf.Pieces(uint64(chunkIndex)); err != nil {
				b.Fatal(err)
			}
			reads++
			if reads == b.N {
				return
			}
		}
	}
}
