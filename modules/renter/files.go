package renter

import (
	"gitlab.com/NebulousLabs/Sia/modules"

	"gitlab.com/NebulousLabs/errors"
)

// DeleteFile removes a file entry from the renter and deletes its data from
// the hosts it is stored on.
func (r *Renter) DeleteFile(siaPath modules.SiaPath) error {
	err := r.tg.Add()
	if err != nil {
		return err
	}
	defer r.tg.Done()

	// Fetch info before deleting the file.
	_, err = r.staticFileSystem.CachedFileInfo(siaPath)
	if err != nil {
		return errors.AddContext(err, "failed to fetch info before deleting file")
	}

	// Perform the delete operation.
	err = r.staticFileSystem.DeleteFile(siaPath)
	if err != nil {
		return errors.AddContext(err, "unable to delete siafile from filesystem")
	}

	// Update the filesystem metadata.
	//
	// TODO: This is incorrect, should be running the metadata update call on a
	// node, not on a siaPath. The node should be returned by the delete call.
	// Need a metadata update func that operates on a node to do that.
	dirSiaPath, err := siaPath.Dir()
	if err != nil {
		r.log.Printf("Unable to fetch the directory from a siaPath %v for deleted siafile: %v", siaPath, err)
		// Return 'nil' because the delete operation succeeded, it was only the
		// metadata update operation that failed.
		return nil
	}
	go r.callThreadedBubbleMetadata(dirSiaPath)
	return nil
}

// FileList loops over all the files within the directory specified by siaPath
// and will then call the provided listing function on the file.
func (r *Renter) FileList(siaPath modules.SiaPath, recursive, cached bool, flf modules.FileListFunc) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	var err error
	if cached {
		err = r.staticFileSystem.CachedList(siaPath, recursive, flf, func(modules.DirectoryInfo) {})
	} else {
		offlineMap, goodForRenewMap, contractsMap := r.managedContractUtilityMaps()
		err = r.staticFileSystem.List(siaPath, recursive, offlineMap, goodForRenewMap, contractsMap, flf, func(modules.DirectoryInfo) {})
	}
	if err != nil {
		return err
	}
	return err
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
		return modules.FileInfo{}, errors.AddContext(err, "unable to get the fileinfo from the filesystem")
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

	// Rename file.
	err := r.staticFileSystem.RenameFile(currentName, newName)
	if err != nil {
		return err
	}

	// Call callThreadedBubbleMetadata on the old and new directories to make
	// sure the system metadata is updated to reflect the move.
	oldDirSiaPath, err := currentName.Dir()
	if err != nil {
		return err
	}
	newDirSiaPath, err := newName.Dir()
	if err != nil {
		return err
	}
	bubblePaths := r.newUniqueRefreshPaths()
	err = bubblePaths.callAdd(oldDirSiaPath)
	if err != nil {
		r.log.Printf("failed to add old directory '%v' to bubble paths:  %v", oldDirSiaPath, err)
	}
	err = bubblePaths.callAdd(newDirSiaPath)
	if err != nil {
		r.log.Printf("failed to add new directory '%v' to bubble paths:  %v", newDirSiaPath, err)
	}
	bubblePaths.callRefreshAll()
	return nil
}

// SetFileStuck sets the Stuck field of the whole siafile to stuck.
func (r *Renter) SetFileStuck(siaPath modules.SiaPath, stuck bool) (err error) {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	// Open the file.
	entry, err := r.staticFileSystem.OpenSiaFile(siaPath)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Compose(err, entry.Close())
	}()
	// Update the file.
	return entry.SetAllStuck(stuck)
}
