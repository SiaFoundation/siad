package renter

import (
	"os"
	"sort"
	"sync"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/modules"
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
func (r *Renter) DirList(siaPath modules.SiaPath) (dis []modules.DirectoryInfo, _ error) {
	if err := r.tg.Add(); err != nil {
		return nil, err
	}
	defer r.tg.Done()
	return r.managedDirList(siaPath)
}

// managedDirList lists the directories in a siadir
func (r *Renter) managedDirList(siaPath modules.SiaPath) (dis []modules.DirectoryInfo, _ error) {
	var mu sync.Mutex
	dlf := func(di modules.DirectoryInfo) {
		mu.Lock()
		dis = append(dis, di)
		mu.Unlock()
	}
	err := r.staticFileSystem.CachedList(siaPath, false, func(modules.FileInfo) {}, dlf)
	if err != nil {
		return nil, err
	}
	sort.Slice(dis, func(i, j int) bool {
		return dis[i].SiaPath.String() < dis[j].SiaPath.String()
	})
	return dis, nil
}

// RenameDir takes an existing directory and changes the path. The original
// directory must exist, and there must not be any directory that already has
// the replacement path.  All sia files within directory will also be renamed
func (r *Renter) RenameDir(oldPath, newPath modules.SiaPath) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()

	// Special case: do not allow a user to rename a dir to root.
	if newPath.IsRoot() {
		return errors.New("cannot rename a file to the root directory")
	}
	return r.staticFileSystem.RenameDir(oldPath, newPath)
}
