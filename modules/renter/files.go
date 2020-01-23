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

// FileList returns all of the files that the renter has.
func (r *Renter) FileList(siaPath modules.SiaPath, recursive, cached bool) ([]modules.FileInfo, error) {
	if err := r.tg.Add(); err != nil {
		return []modules.FileInfo{}, err
	}
	defer r.tg.Done()
	var fis []modules.FileInfo
	var err error
	if cached {
		fis, _, err = r.staticFileSystem.CachedList(siaPath, recursive)
	} else {
		offlineMap, goodForRenewMap, contractsMap := r.managedContractUtilityMaps()
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
