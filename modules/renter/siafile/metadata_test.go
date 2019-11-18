package siafile

import (
	"fmt"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// createLinkedBlankSiafile creates 2 SiaFiles which use the same SiaFile to
// store combined chunks. They reside within 'dir'.
func createLinkedBlankSiafiles(dir string) (*SiaFile, *SiaFile, error) {
	// Create a wal.
	walFilePath := filepath.Join(dir, "writeaheadlog.wal")
	_, wal, err := writeaheadlog.New(walFilePath)
	if err != nil {
		return nil, nil, err
	}
	// Get parameters for the files.
	_, _, source, rc, sk, fileSize, numChunks, fileMode := newTestFileParams(1, true)
	// Create a SiaFile for partial chunks.
	var partialsSiaFile *SiaFile
	partialsSiaPath := modules.CombinedSiaFilePath(rc)
	partialsSiaFilePath := partialsSiaPath.SiaPartialsFileSysPath(dir)
	if _, err = os.Stat(partialsSiaFilePath); os.IsNotExist(err) {
		partialsSiaFile, err = New(partialsSiaFilePath, "", wal, rc, sk, 0, fileMode, nil, false)
	} else {
		partialsSiaFile, err = LoadSiaFile(partialsSiaFilePath, wal)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load partialsSiaFile: %v", err)
	}
	/*
		 PARTIAL TODO:
			partialsEntry := &SiaFileSetEntry{
				dummyEntry(partialsSiaFile),
				uint64(fastrand.Intn(math.MaxInt32)),
			}
	*/
	// Create the files.
	sf1Path := filepath.Join(dir, "sf1"+modules.SiaFileExtension)
	sf2Path := filepath.Join(dir, "sf2"+modules.SiaFileExtension)
	sf1, err := New(sf1Path, source, wal, rc, sk, fileSize, fileMode, nil, false)
	if err != nil {
		return nil, nil, err
	}
	sf2, err := New(sf2Path, source, wal, rc, sk, fileSize, fileMode, nil, false)
	if err != nil {
		return nil, nil, err
	}
	// Check that the number of chunks in the files are correct.
	if numChunks >= 0 && sf1.numChunks != numChunks {
		return nil, nil, errors.New("createLinkedBlankSiafiles didn't create the expected number of chunks")
	}
	if numChunks >= 0 && sf2.numChunks != numChunks {
		return nil, nil, errors.New("createLinkedBlankSiafiles didn't create the expected number of chunks")
	}
	if partialsSiaFile.numChunks != 0 {
		return nil, nil, errors.New("createLinkedBlankSiafiles didn't create an empty partialsSiaFile")
	}
	return sf1, sf2, nil
}
