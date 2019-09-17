package renter

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
)

// DeleteFile removes a file entry from the renter and deletes its data from
// the hosts it is stored on.
func (r *Renter) DeleteFile(siaPath modules.SiaPath) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()

	// Call threadedBubbleMetadata on the old directory to make sure the system
	// metadata is updated to reflect the move
	defer func() error {
		dirSiaPath, err := siaPath.Dir()
		if err != nil {
			return err
		}
		go r.threadedBubbleMetadata(dirSiaPath)
		return nil
	}()

	return r.staticFileSet.Delete(siaPath)
}

// FileList returns all of the files that the renter has.
func (r *Renter) FileList(siaPath modules.SiaPath, recursive, cached bool) ([]modules.FileInfo, error) {
	if err := r.tg.Add(); err != nil {
		return []modules.FileInfo{}, err
	}
	defer r.tg.Done()
	offlineMap, goodForRenewMap, contractsMap := r.managedContractUtilityMaps()
	return r.staticFileSet.FileList(siaPath, recursive, cached, offlineMap, goodForRenewMap, contractsMap)
}

// File returns file from siaPath queried by user.
// Update based on FileList
func (r *Renter) File(siaPath modules.SiaPath) (modules.FileInfo, error) {
	if err := r.tg.Add(); err != nil {
		return modules.FileInfo{}, err
	}
	defer r.tg.Done()
	offline, goodForRenew, contracts := r.managedContractUtilityMaps()
	return r.staticFileSet.FileInfo(siaPath, offline, goodForRenew, contracts)
}

// RenameFile takes an existing file and changes the nickname. The original
// file must exist, and there must not be any file that already has the
// replacement nickname.
func (r *Renter) RenameFile(currentName, newName modules.SiaPath) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	// Rename file
	err := r.staticFileSet.Rename(currentName, newName)
	if err != nil {
		return err
	}
	// Call threadedBubbleMetadata on the old directory to make sure the system
	// metadata is updated to reflect the move
	dirSiaPath, err := currentName.Dir()
	if err != nil {
		return err
	}
	go r.threadedBubbleMetadata(dirSiaPath)

	// Create directory metadata for new path, ignore errors if siadir already
	// exists
	dirSiaPath, err = newName.Dir()
	if err != nil {
		return err
	}
	err = r.CreateDir(dirSiaPath)
	if err != siadir.ErrPathOverload && err != nil {
		return err
	}
	// Call threadedBubbleMetadata on the new directory to make sure the system
	// metadata is updated to reflect the move
	go r.threadedBubbleMetadata(dirSiaPath)
	return nil
}

// SetFileStuck sets the Stuck field of the whole siafile to stuck.
func (r *Renter) SetFileStuck(siaPath modules.SiaPath, stuck bool) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	// Open the file.
	entry, err := r.staticFileSet.Open(siaPath)
	if err != nil {
		return err
	}
	defer entry.Close()
	// Update the file.
	return entry.SetAllStuck(stuck)
}
