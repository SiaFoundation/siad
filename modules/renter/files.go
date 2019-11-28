package renter

import (
	"gitlab.com/NebulousLabs/Sia/modules"

	"gitlab.com/NebulousLabs/errors"
)

// DeleteFile removes a file entry from the renter and deletes its data from
// the hosts it is stored on.
func (r *Renter) DeleteFile(siaPath modules.SiaPath) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()

	// Call callThreadedBubbleMetadata on the old directory to make sure the
	// system metadata is updated to reflect the move
	defer func() error {
		dirSiaPath, err := siaPath.Dir()
		if err != nil {
			return err
		}
		go r.callThreadedBubbleMetadata(dirSiaPath)
		return nil
	}()

	return r.staticFileSystem.DeleteFile(siaPath)
}

// FileList returns all of the files that the renter has.
func (r *Renter) FileList(siaPath modules.SiaPath, recursive, cached bool) ([]modules.FileInfo, error) {
	if err := r.tg.Add(); err != nil {
		return []modules.FileInfo{}, err
	}
	defer r.tg.Done()
	offlineMap, goodForRenewMap, contractsMap := r.managedContractUtilityMaps()
	if cached {

	}
	var fis []modules.FileInfo
	var err error
	if cached {
		fis, _, err = r.staticFileSystem.CachedList(siaPath, recursive)
	} else {
		fis, _, err = r.staticFileSystem.List(siaPath, recursive, offlineMap, goodForRenewMap, contractsMap)
	}
	if err != nil {
		return nil, err
	}
	return fis, err
}

// File returns file from siaPath queried by user.
// Update based on FileList
func (r *Renter) File(siaPath modules.SiaPath) (modules.FileInfo, error) {
	if err := r.tg.Add(); err != nil {
		return modules.FileInfo{}, err
	}
	defer r.tg.Done()
	offline, goodForRenew, contracts := r.managedContractUtilityMaps()
	fi, err := r.staticFileSystem.FileInfo(siaPath, offline, goodForRenew, contracts)
	if err != nil {
		return modules.FileInfo{}, err
	}
	return fi, nil
}

// FileCached returns file from siaPath queried by user, using cached values for
// health and redundancy.
func (r *Renter) FileCached(siaPath modules.SiaPath) (modules.FileInfo, error) {
	if err := r.tg.Add(); err != nil {
		return modules.FileInfo{}, err
	}
	defer r.tg.Done()
	return r.staticFileSystem.CachedFileInfo(siaPath)
}

// RenameFile takes an existing file and changes the nickname. The original
// file must exist, and there must not be any file that already has the
// replacement nickname.
func (r *Renter) RenameFile(currentName, newName modules.SiaPath) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()

	// Special case: do not allow the user to rename to root.
	if newName.IsRoot() {
		return errors.New("cannot rename a file to the root directory")
	}

	// Rename file
	err := r.staticFileSystem.RenameFile(currentName, newName)
	if err != nil {
		return err
	}
	// Call callThreadedBubbleMetadata on the old directory to make sure the
	// system metadata is updated to reflect the move
	dirSiaPath, err := currentName.Dir()
	if err != nil {
		return err
	}
	go r.callThreadedBubbleMetadata(dirSiaPath)

	// Call callThreadedBubbleMetadata on the new directory to make sure the
	// system metadata is updated to reflect the move
	go r.callThreadedBubbleMetadata(dirSiaPath)
	return nil
}

// SetFileStuck sets the Stuck field of the whole siafile to stuck.
func (r *Renter) SetFileStuck(siaPath modules.SiaPath, stuck bool) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	// Open the file.
	entry, err := r.staticFileSystem.OpenSiaFile(siaPath)
	if err != nil {
		return err
	}
	defer entry.Close()
	// Update the file.
	return entry.SetAllStuck(stuck)
}
