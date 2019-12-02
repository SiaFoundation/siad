package renter

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	"os"
)

// CreateDir creates a directory for the renter
func (r *Renter) CreateDir(siaPath modules.SiaPath, mode os.FileMode) error {
	err := r.tg.Add()
	if err != nil {
		return err
	}
	defer r.tg.Done()
	return r.staticFileSystem.NewSiaDir(siaPath, mode)
}

// DeleteDir removes a directory from the renter and deletes all its sub
// directories and files
func (r *Renter) DeleteDir(siaPath modules.SiaPath) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	return r.staticFileSystem.DeleteDir(siaPath)
}

// DirList lists the directories in a siadir
func (r *Renter) DirList(siaPath modules.SiaPath) ([]modules.DirectoryInfo, error) {
	if err := r.tg.Add(); err != nil {
		return nil, err
	}
	defer r.tg.Done()
	_, dis, err := r.staticFileSystem.CachedList(siaPath, false)
	return dis, err
}

// RenameDir takes an existing directory and changes the path. The original
// directory must exist, and there must not be any directory that already has
// the replacement path.  All sia files within directory will also be renamed
func (r *Renter) RenameDir(oldPath, newPath modules.SiaPath) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	return r.staticFileSystem.RenameDir(oldPath, newPath)
}
