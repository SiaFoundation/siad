package renter

import (
	"gitlab.com/NebulousLabs/Sia/modules"
)

// rebaseInputSiaPath rebases the SiaPath provided by the user to one that is
// prefix by the "siafiles" directory.
func rebaseInputSiaPath(siaPath modules.SiaPath) (modules.SiaPath, error) {
	// Prepend the provided siapath with the /home/siafiles dir.
	if siaPath.IsRoot() {
		return modules.SiaFilesSiaPath(), nil
	}
	return modules.SiaFilesSiaPath().Join(siaPath.String())
}

// CreateDir creates a directory for the renter
func (r *Renter) CreateDir(siaPath modules.SiaPath) error {
	err := r.tg.Add()
	if err != nil {
		return err
	}
	defer r.tg.Done()
	siaPath, err = rebaseInputSiaPath(siaPath)
	if err != nil {
		return err
	}
	return r.staticFileSystem.NewSiaDir(siaPath)
}

// DeleteDir removes a directory from the renter and deletes all its sub
// directories and files
func (r *Renter) DeleteDir(siaPath modules.SiaPath) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	siaPath, err := rebaseInputSiaPath(siaPath)
	if err != nil {
		return err
	}
	return r.staticFileSystem.DeleteDir(siaPath)
}

// DirList lists the directories in a siadir
func (r *Renter) DirList(siaPath modules.SiaPath) ([]modules.DirectoryInfo, error) {
	if err := r.tg.Add(); err != nil {
		return nil, err
	}
	defer r.tg.Done()
	siaPath, err := rebaseInputSiaPath(siaPath)
	if err != nil {
		return nil, err
	}
	offlineMap, goodForRenewMap, contractsMap := r.managedContractUtilityMaps()
	_, di, err := r.staticFileSystem.List(siaPath, false, false, offlineMap, goodForRenewMap, contractsMap)
	return di, err
}

// RenameDir takes an existing directory and changes the path. The original
// directory must exist, and there must not be any directory that already has
// the replacement path.  All sia files within directory will also be renamed
func (r *Renter) RenameDir(oldPath, newPath modules.SiaPath) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	// Prepend the provided siapath with the /home/siafiles dir.
	oldPath, err := rebaseInputSiaPath(oldPath)
	if err != nil {
		return err
	}
	newPath, err = rebaseInputSiaPath(newPath)
	if err != nil {
		return err
	}
	return r.staticFileSystem.RenameDir(oldPath, newPath)
}
