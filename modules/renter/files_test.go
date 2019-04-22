package renter

import (
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// newFileTesting is a helper function that calls newFile but returns no error.
//
// Note: Since this function is not a renter method, the file will not be
// properly added to a renter if there is not one in the calling test
func newFileTesting(name string, wal *writeaheadlog.WAL, rsc modules.ErasureCoder, fileSize uint64, mode os.FileMode, source string) (*siafile.SiaFile, error) {
	// create the renter/files dir if it doesn't exist
	siaFilePath := filepath.Join(os.TempDir(), "siafiles", name)
	dir, _ := filepath.Split(siaFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	// Create SiaPath
	siaPath, err := modules.NewSiaPath(name)
	if err != nil {
		return nil, err
	}
	// create the file
	f, err := siafile.New(siaPath, siaFilePath, source, wal, rsc, crypto.GenerateSiaKey(crypto.RandomCipherType()), fileSize, mode)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// newTestingWal is a helper method to create a wal during testing.
func newTestingWal() *writeaheadlog.WAL {
	walDir := filepath.Join(os.TempDir(), "wals")
	if err := os.MkdirAll(walDir, 0700); err != nil {
		panic(err)
	}
	walPath := filepath.Join(walDir, hex.EncodeToString(fastrand.Bytes(8)))
	_, wal, err := writeaheadlog.New(walPath)
	if err != nil {
		panic(err)
	}
	return wal
}

// newRenterTestFile creates a test file when the test has a renter so that the
// file is properly added to the renter. It returns the SiaFileSetEntry that the
// SiaFile is stored in
func (r *Renter) newRenterTestFile() (*siafile.SiaFileSetEntry, error) {
	// Generate name and erasure coding
	siaPath, rsc := testingFileParams()
	// create the renter/files dir if it doesn't exist
	siaFilePath := siaPath.SiaFileSysPath(r.staticFilesDir)
	dir, _ := filepath.Split(siaFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	// Create File
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     siaPath,
		ErasureCode: rsc,
	}
	entry, err := r.staticFileSet.NewSiaFile(up, crypto.GenerateSiaKey(crypto.RandomCipherType()), 1000, 0777)
	if err != nil {
		return nil, err
	}
	return entry, nil
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
		rsc, _ := siafile.NewRSCode(test.dataPieces, 1) // can't use 0
		// Create the file
		f, err := newFileTesting(t.Name(), newTestingWal(), rsc, test.fileSize, 0777, "")
		if err != nil {
			t.Fatal(err)
		}
		// Make sure the file reports the correct pieceSize.
		if f.PieceSize() != modules.SectorSize-f.MasterKey().Type().Overhead() {
			t.Fatal("file has wrong pieceSize for its encryption type")
		}
		// Check that the number of chunks matches the expected number.
		expectedNumChunks := test.fileSize / (f.PieceSize() * uint64(test.dataPieces))
		if expectedNumChunks == 0 {
			// There is at least 1 chunk.
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

	for _, nData := range nDatas {
		rsc, _ := siafile.NewRSCode(nData, 10)
		f, err := newFileTesting(t.Name(), newTestingWal(), rsc, 1000, 0777, "")
		if err != nil {
			t.Fatal(err)
		}
		// Test that an empty file has 0 redundancy.
		if r := f.Redundancy(neverOffline, goodForRenew); r != 0 {
			t.Error("expected 0 redundancy, got", r)
		}
		// Test that a file with 1 host that has a piece for every chunk but
		// one chunk still has a redundancy of 0.
		for i := uint64(0); i < f.NumChunks()-1; i++ {
			err := f.AddPiece(types.SiaPublicKey{Key: []byte{byte(0)}}, i, 0, crypto.Hash{})
			if err != nil {
				t.Fatal(err)
			}
		}
		if r := f.Redundancy(neverOffline, goodForRenew); r != 0 {
			t.Error("expected 0 redundancy, got", r)
		}
		// Test that adding another host with a piece for every chunk but one
		// chunk still results in a file with redundancy 0.
		for i := uint64(0); i < f.NumChunks()-1; i++ {
			err := f.AddPiece(types.SiaPublicKey{Key: []byte{byte(1)}}, i, 1, crypto.Hash{})
			if err != nil {
				t.Fatal(err)
			}
		}
		if r := f.Redundancy(neverOffline, goodForRenew); r != 0 {
			t.Error("expected 0 redundancy, got", r)
		}
		// Test that adding a file contract with a piece for the missing chunk
		// results in a file with redundancy > 0 && <= 1.
		err = f.AddPiece(types.SiaPublicKey{Key: []byte{byte(2)}}, f.NumChunks()-1, 0, crypto.Hash{})
		if err != nil {
			t.Fatal(err)
		}
		// 1.0 / MinPieces because the chunk with the least number of pieces has 1 piece.
		expectedR := 1.0 / float64(f.ErasureCode().MinPieces())
		if r := f.Redundancy(neverOffline, goodForRenew); r != expectedR {
			t.Errorf("expected %f redundancy, got %f", expectedR, r)
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
		if r := f.Redundancy(neverOffline, goodForRenew); r != expectedR {
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
		if r := f.Redundancy(specificOffline, goodForRenew); r != expectedR {
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
	rsc, _ := siafile.NewRSCode(10, 20)
	zeroFile, err := newFileTesting(t.Name(), newTestingWal(), rsc, 0, 0777, "")
	if err != nil {
		t.Fatal(err)
	}

	// Create offline map
	offlineMap := make(map[string]bool)
	goodForRenewMap := make(map[string]bool)

	// Confirm the health is correct
	health, stuckHealth, numStuckChunks := zeroFile.Health(offlineMap, goodForRenewMap)
	if health != 0 {
		t.Fatal("Expected health to be 0 but was", health)
	}
	if stuckHealth != 0 {
		t.Fatal("Expected stuck health to be 0 but was", stuckHealth)
	}
	if numStuckChunks != 0 {
		t.Fatal("Expected no stuck chunks but found", numStuckChunks)
	}

	// Create File with 1 chunk
	f, err := newFileTesting(t.Name(), newTestingWal(), rsc, 100, 0777, "")
	if err != nil {
		t.Fatal(err)
	}

	// Check file health, since there are no pieces in the chunk yet no good
	// pieces will be found resulting in a health of 1.5 with the erasure code
	// settings of 10/30. Since there are no stuck chunks the stuckHealth of the
	// file should be 0
	//
	// 1 - ((0 - 10) / 20)
	health, stuckHealth, _ = f.Health(offlineMap, goodForRenewMap)
	if health != 1.5 {
		t.Fatalf("Health of file not as expected, got %v expected 1.5", health)
	}
	if stuckHealth != float64(0) {
		t.Fatalf("Stuck Health of file not as expected, got %v expected 0", stuckHealth)
	}

	// Add good pieces to first Piece Set
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
	health, _, _ = f.Health(offlineMap, goodForRenewMap)
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
	health, _, _ = f.Health(offlineMap, goodForRenewMap)
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
	health, _, _ = f.Health(offlineMap, goodForRenewMap)
	if health != 1.40 {
		t.Fatalf("Health of file not as expected, got %v expected 1.40", health)
	}

	// Mark chunk as stuck
	err = f.SetStuck(0, true)
	if err != nil {
		t.Fatal(err)
	}
	health, stuckHealth, numStuckChunks = f.Health(offlineMap, goodForRenewMap)
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
	f, err = newFileTesting(t.Name(), newTestingWal(), rsc, 5e4, 0777, "")
	if err != nil {
		t.Fatal(err)
	}

	// Create offline map
	offlineMap = make(map[string]bool)
	goodForRenewMap = make(map[string]bool)

	// Check file health, since there are no pieces in the chunk yet no good
	// pieces will be found resulting in a health of 1.5
	health, _, _ = f.Health(offlineMap, goodForRenewMap)
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
	health, stuckHealth, _ = f.Health(offlineMap, goodForRenewMap)
	if health != 1.5 {
		t.Fatalf("Health of file not as expected, got %v expected 1.5", health)
	}

	// Add good pieces to second chunk, confirm health is 1.40 since both chunks
	// have 2 good pieces.
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
	health, _, _ = f.Health(offlineMap, goodForRenewMap)
	if health != 1.40 {
		t.Fatalf("Health of file not as expected, got %v expected 1.40", health)
	}

	// Mark second chunk as stuck
	err = f.SetStuck(1, true)
	if err != nil {
		t.Fatal(err)
	}
	health, stuckHealth, numStuckChunks = f.Health(offlineMap, goodForRenewMap)
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

// TestRenterFileListLocalPath verifies that FileList() returns the correct
// local path information for an uploaded file.
func TestRenterFileListLocalPath(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	id := rt.renter.mu.Lock()
	entry, _ := rt.renter.newRenterTestFile()
	if err := entry.SetLocalPath("TestPath"); err != nil {
		t.Fatal(err)
	}
	rt.renter.mu.Unlock(id)
	files, err := rt.renter.FileList()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatal("wrong number of files, got", len(files), "wanted one")
	}
	if files[0].LocalPath != "TestPath" {
		t.Fatal("file had wrong LocalPath: got", files[0].LocalPath, "wanted TestPath")
	}
}

// TestRenterDeleteFile probes the DeleteFile method of the renter type.
func TestRenterDeleteFile(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Delete a file from an empty renter.
	siaPath, err := modules.NewSiaPath("dne")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.DeleteFile(siaPath)
	if err != siafile.ErrUnknownPath {
		t.Errorf("Expected '%v' got '%v'", siafile.ErrUnknownPath, err)
	}

	// Put a file in the renter.
	entry, err := rt.renter.newRenterTestFile()
	if err != nil {
		t.Fatal(err)
	}
	// Delete a different file.
	siaPathOne, err := modules.NewSiaPath("one")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.DeleteFile(siaPathOne)
	if err != siafile.ErrUnknownPath {
		t.Errorf("Expected '%v' got '%v'", siafile.ErrUnknownPath, err)
	}
	// Delete the file.
	siapath := rt.renter.staticFileSet.SiaPath(entry)
	err = entry.Close()
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.DeleteFile(siapath)
	if err != nil {
		t.Error(err)
	}
	files, err := rt.renter.FileList()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Error("file was deleted, but is still reported in FileList")
	}
	// Confirm that file was removed from SiaFileSet
	_, err = rt.renter.staticFileSet.Open(siapath)
	if err == nil {
		t.Fatal("Deleted file still found in staticFileSet")
	}

	// Put a file in the renter, then rename it.
	entry2, err := rt.renter.newRenterTestFile()
	if err != nil {
		t.Fatal(err)
	}
	siaPath1, err := modules.NewSiaPath("1")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(rt.renter.staticFileSet.SiaPath(entry2), siaPath1) // set name to "1"
	if err != nil {
		t.Fatal(err)
	}
	siapath2 := rt.renter.staticFileSet.SiaPath(entry2)
	err = entry2.Close()
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(siapath2, siaPathOne)
	if err != nil {
		t.Fatal(err)
	}
	// Call delete on the previous name.
	err = rt.renter.DeleteFile(siaPath1)
	if err != siafile.ErrUnknownPath {
		t.Errorf("Expected '%v' got '%v'", siafile.ErrUnknownPath, err)
	}
	// Call delete on the new name.
	err = rt.renter.DeleteFile(siaPathOne)
	if err != nil {
		t.Error(err)
	}

	// Check that all .sia files have been deleted.
	var walkStr string
	filepath.Walk(rt.renter.staticFilesDir, func(path string, _ os.FileInfo, _ error) error {
		// capture only .sia files
		if filepath.Ext(path) == ".sia" {
			rel, _ := filepath.Rel(rt.renter.staticFilesDir, path) // strip testdir prefix
			walkStr += rel
		}
		return nil
	})
	expWalkStr := ""
	if walkStr != expWalkStr {
		t.Fatalf("Bad walk string: expected %q, got %q", expWalkStr, walkStr)
	}
}

// TestRenterFileList probes the FileList method of the renter type.
func TestRenterFileList(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Get the file list of an empty renter.
	files, err := rt.renter.FileList()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatal("FileList has non-zero length for empty renter?")
	}

	// Put a file in the renter.
	entry1, _ := rt.renter.newRenterTestFile()
	files, err = rt.renter.FileList()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatal("FileList is not returning the only file in the renter")
	}
	if !files[0].SiaPath.Equals(rt.renter.staticFileSet.SiaPath(entry1)) {
		t.Error("FileList is not returning the correct filename for the only file")
	}

	// Put multiple files in the renter.
	entry2, _ := rt.renter.newRenterTestFile()
	files, err = rt.renter.FileList()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("Expected %v files, got %v", 2, len(files))
	}
	files, err = rt.renter.FileList()
	if err != nil {
		t.Fatal(err)
	}
	if !((files[0].SiaPath.Equals(rt.renter.staticFileSet.SiaPath(entry1)) || files[0].SiaPath.Equals(rt.renter.staticFileSet.SiaPath(entry2))) &&
		(files[1].SiaPath.Equals(rt.renter.staticFileSet.SiaPath(entry1)) || files[1].SiaPath.Equals(rt.renter.staticFileSet.SiaPath(entry2))) &&
		(files[0].SiaPath != files[1].SiaPath)) {
		t.Log("files[0].SiaPath", files[0].SiaPath)
		t.Log("files[1].SiaPath", files[1].SiaPath)
		t.Log("file1.SiaPath()", rt.renter.staticFileSet.SiaPath(entry1).String())
		t.Log("file2.SiaPath()", rt.renter.staticFileSet.SiaPath(entry2).String())
		t.Error("FileList is returning wrong names for the files")
	}
}

// TestRenterRenameFile probes the rename method of the renter.
func TestRenterRenameFile(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Rename a file that doesn't exist.
	siaPath1, err := modules.NewSiaPath("1")
	if err != nil {
		t.Fatal(err)
	}
	siaPath1a, err := modules.NewSiaPath("1a")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(siaPath1, siaPath1a)
	if err.Error() != siafile.ErrUnknownPath.Error() {
		t.Errorf("Expected '%v' got '%v'", siafile.ErrUnknownPath, err)
	}

	// Get the fileset.
	sfs := rt.renter.staticFileSet

	// Rename a file that does exist.
	entry, _ := rt.renter.newRenterTestFile()
	err = rt.renter.RenameFile(sfs.SiaPath(entry), siaPath1)
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(siaPath1, siaPath1a)
	if err != nil {
		t.Fatal(err)
	}
	files, err := rt.renter.FileList()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatal("FileList has unexpected number of files:", len(files))
	}
	if !files[0].SiaPath.Equals(siaPath1a) {
		t.Errorf("RenameFile failed: expected %v, got %v", siaPath1a.String(), files[0].SiaPath)
	}
	// Confirm SiaFileSet was updated
	_, err = rt.renter.staticFileSet.Open(siaPath1a)
	if err != nil {
		t.Fatal("renter staticFileSet not updated to new file name")
	}
	_, err = rt.renter.staticFileSet.Open(siaPath1)
	if err == nil {
		t.Fatal("old name not removed from renter staticFileSet")
	}

	// Rename a file to an existing name.
	entry2, err := rt.renter.newRenterTestFile()
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(rt.renter.staticFileSet.SiaPath(entry2), siaPath1) // Rename to "1"
	if err != nil {
		t.Fatal(err)
	}
	err = entry2.Close()
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(siaPath1, siaPath1a)
	if err != siafile.ErrPathOverload {
		t.Error("Expecting ErrPathOverload, got", err)
	}

	// Rename a file to the same name.
	err = rt.renter.RenameFile(siaPath1, siaPath1)
	if err != siafile.ErrPathOverload {
		t.Error("Expecting ErrPathOverload, got", err)
	}

	// Confirm ability to rename file
	siaPath1b, err := modules.NewSiaPath("1b")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(siaPath1, siaPath1b)
	if err != nil {
		t.Fatal(err)
	}

	// Rename file that would create a directory
	siaPathWithDir, err := modules.NewSiaPath("new/name/with/dir/test")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(siaPath1b, siaPathWithDir)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm directory metadatas exist
	dirSiaPath := siaPathWithDir
	for !dirSiaPath.Equals(modules.RootSiaPath()) {
		dirSiaPath, err = dirSiaPath.Dir()
		if err != nil {
			t.Fatal(err)
		}
		_, err = rt.renter.staticDirSet.Open(dirSiaPath)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestRenterFileDir tests that the renter files are uploaded to the files
// directory and not the root directory of the renter.
func TestRenterFileDir(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create local file to upload
	localDir := filepath.Join(rt.dir, "files")
	if err := os.MkdirAll(localDir, 0700); err != nil {
		t.Fatal(err)
	}
	size := 100
	fileName := fmt.Sprintf("%dbytes %s", size, hex.EncodeToString(fastrand.Bytes(4)))
	source := filepath.Join(localDir, fileName)
	bytes := fastrand.Bytes(size)
	if err := ioutil.WriteFile(source, bytes, 0600); err != nil {
		t.Fatal(err)
	}

	// Upload local file
	ec, err := siafile.NewRSCode(defaultDataPieces, defaultParityPieces)
	if err != nil {
		t.Fatal(err)
	}
	siaPath, err := modules.NewSiaPath(fileName)
	if err != nil {
		t.Fatal(err)
	}
	params := modules.FileUploadParams{
		Source:      source,
		SiaPath:     siaPath,
		ErasureCode: ec,
	}
	err = rt.renter.Upload(params)
	if err != nil {
		t.Fatal("failed to upload file:", err)
	}

	// Get file and check siapath
	f, err := rt.renter.File(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	if !f.SiaPath.Equals(siaPath) {
		t.Fatalf("siapath not set as expected: got %v expected %v", f.SiaPath, fileName)
	}

	// Confirm .sia file exists on disk in the SiapathRoot directory
	renterDir := filepath.Join(rt.dir, modules.RenterDir)
	siapathRootDir := filepath.Join(renterDir, modules.SiapathRoot)
	fullPath := siaPath.SiaFileSysPath(siapathRootDir)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		t.Fatal("No .sia file found on disk")
	}
}
