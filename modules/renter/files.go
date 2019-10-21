package renter

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
)

// trimSiaFileFolder is a helper method to trim /home/siafiles off of the
// siapaths of the fileinfos since the user expects a path relative to
// /home/siafiles and not relative to root.
func trimSiaFileFolder(fis ...modules.FileInfo) (_ []modules.FileInfo, err error) {
	for i := range fis {
		fis[i].SiaPath, err = fis[i].SiaPath.Rebase(modules.SiaFilesSiaPath(), modules.RootSiaPath())
		if err != nil {
			return nil, err
		}
	}
	return fis, nil
}

// DeleteFile removes a file entry from the renter and deletes its data from
// the hosts it is stored on.
func (r *Renter) DeleteFile(siaPath modules.SiaPath) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()

	// Prepend the provided siapath with the /home/siafiles dir.
	var err error
	siaPath, err = modules.SiaFilesSiaPath().Join(siaPath.String())
	if err != nil {
		return err
	}

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
	// Prepend the provided siapath with the /home/siafiles dir.
	var err error
	if siaPath.IsRoot() {
		siaPath = modules.SiaFilesSiaPath()
	} else {
		siaPath, err = modules.SiaFilesSiaPath().Join(siaPath.String())
	}
	if err != nil {
		return []modules.FileInfo{}, err
	}
	offlineMap, goodForRenewMap, contractsMap := r.managedContractUtilityMaps()
	fis, _, err := r.staticFileSystem.List(siaPath, recursive, cached, offlineMap, goodForRenewMap, contractsMap)
	if err != nil {
		return nil, err
	}
	fis, err = trimSiaFileFolder(fis...)
	return fis, err
}

// File returns file from siaPath queried by user.
// Update based on FileList
func (r *Renter) File(siaPath modules.SiaPath) (modules.FileInfo, error) {
	if err := r.tg.Add(); err != nil {
		return modules.FileInfo{}, err
	}
	defer r.tg.Done()
	// Prepend the provided siapath with the /home/siafiles dir.
	var err error
	siaPath, err = modules.SiaFilesSiaPath().Join(siaPath.String())
	if err != nil {
		return modules.FileInfo{}, err
	}
	offline, goodForRenew, contracts := r.managedContractUtilityMaps()
	fi, err := r.staticFileSystem.FileInfo(siaPath, offline, goodForRenew, contracts)
	if err != nil {
		return modules.FileInfo{}, err
	}
	fis, err := trimSiaFileFolder(fi)
	if err != nil {
		return modules.FileInfo{}, err
	}
	return fis[0], nil
}

// RenameFile takes an existing file and changes the nickname. The original
// file must exist, and there must not be any file that already has the
// replacement nickname.
func (r *Renter) RenameFile(currentName, newName modules.SiaPath) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	// Prepend the provided siapaths with the /home/siafiles dir.
	var err error
	currentName, err = modules.SiaFilesSiaPath().Join(currentName.String())
	if err != nil {
		return err
	}
	newName, err = modules.SiaFilesSiaPath().Join(newName.String())
	if err != nil {
		return err
	}
	// Rename file
	err = r.staticFileSystem.RenameFile(currentName, newName)
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
	// Prepend the provided siapath with the /home/siafiles dir.
	var err error
	siaPath, err = modules.SiaFilesSiaPath().Join(siaPath.String())
	if err != nil {
		return err
	}
	// Open the file.
	entry, err := r.staticFileSystem.OpenSiaFile(siaPath)
	if err != nil {
		return err
	}
	defer entry.Close()
	// Update the file.
	return entry.SetAllStuck(stuck)
}
