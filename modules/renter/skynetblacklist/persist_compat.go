package skynetblacklist

import (
	"bytes"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
)

var metadataVersionv143 = types.NewSpecifier("v1.4.3\n")

// convertPersistVersionFromv143Tov150 handles the compatibility code for
// upgrading the persistence from v1.4.3 to v1.5.0. The change in persistence is
// that the hash of the merkleroot is now persisted instead of the merkleroot
// itself.
func convertPersistVersionFromv143Tov150(persistDir string) error {
	// Open the v143 pesistence file
	aopv143, readerv143, err := persist.NewAppendOnlyPersist(persistDir, persistFile, metadataHeader, metadataVersionv143)
	if err != nil {
		return errors.AddContext(err, "unable to open v143 persistence")
	}
	defer aopv143.Close()

	// Temporarily rename the persist file
	v143FileName := aopv143.FilePath()
	err = aopv143.Rename(v143FileName + "_old")
	if err != nil {
		return errors.AddContext(err, "unable to temporarily rename the persist file")
	}

	// Unmarshal the persistence. We can still use the same unmarshalObjects
	// function since merkleroots are a crypto.Hash this code did not change
	merkleroots, err := unmarshalObjects(readerv143)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal persist objects")
	}

	// Convert merkleroots to hashes and marshal again
	var buf bytes.Buffer
	for mr := range merkleroots {
		hash := crypto.HashObject(mr)
		pe := persistEntry{hash, true}
		bytes := encoding.Marshal(pe)
		buf.Write(bytes)
	}

	// Initialize new persistence
	aopv150, _, err := persist.NewAppendOnlyPersist(persistDir, persistFile, metadataHeader, metadataVersion)
	if err != nil {
		errContext := errors.New("unable to initialize v150 persistence file")
		// Revert partial conversion by renaming the v143 back to the original name
		return errors.Compose(err, errContext, aopv143.Rename(v143FileName))
	}
	defer aopv150.Close()

	// Write the hashses to the persistence file
	_, err = aopv150.Write(buf.Bytes())
	if err != nil {
		errContext := errors.New("unable to write to v150 persistence file")
		// Revert partial conversion by removing the v150 file and renaming the v143
		// back to the original name
		return errors.Compose(err, errContext, aopv150.Remove(), aopv143.Rename(v143FileName))
	}

	// Remove the old persistence so that we can overwrite it with a new file
	err = aopv143.Remove()
	if err != nil {
		return errors.AddContext(err, "unable to remove the old persistence file from disk")
	}

	return nil
}
