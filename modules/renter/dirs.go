package renter

import (
	"io/ioutil"
	"path/filepath"
	"strings"

	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// CreateDir creates a directory for the renter
func (r *Renter) CreateDir(siaPath string) error {
	err := r.tg.Add()
	if err != nil {
		return err
	}
	defer r.tg.Done()
	siaDir, err := r.staticDirSet.NewSiaDir(siaPath)
	defer siaDir.Close()
	return err
}

// DeleteDir removes a directory from the renter and deletes all its sub
// directories and files
func (r *Renter) DeleteDir(siaPath string) error {
	return r.staticDirSet.Delete(siaPath)
}

// DirInfo returns the Directory Information of the siadir
func (r *Renter) DirInfo(siaPath string) (modules.DirectoryInfo, error) {
	// Grab the siadir entry
	entry, err := r.staticDirSet.Open(siaPath)
	if err != nil {
		return modules.DirectoryInfo{}, err
	}
	defer entry.Close()

	// Grab the health information and return the Directory Info, stuckHealth is
	// the true health of the directory so that it what is returned as the
	// health
	_, stuckHealth, lastHealthCheckTime := entry.Health()
	return modules.DirectoryInfo{
		Health:              stuckHealth,
		LastHealthCheckTime: lastHealthCheckTime,
		SiaPath:             siaPath,
	}, nil
}

// DirList returns directories and files stored in the siadir as well as the
// DirectoryInfo of the siadir
func (r *Renter) DirList(siaPath string) ([]modules.DirectoryInfo, []modules.FileInfo, error) {
	var dirs []modules.DirectoryInfo
	var files []modules.FileInfo
	// Get DirectoryInfo
	di, err := r.DirInfo(siaPath)
	if err != nil {
		return nil, nil, err
	}
	dirs = append(dirs, di)
	// Read Directory
	fileInfos, err := ioutil.ReadDir(filepath.Join(r.filesDir, siaPath))
	if err != nil {
		return nil, nil, err
	}
	for _, fi := range fileInfos {
		// Check for directories
		if fi.IsDir() {
			di, err := r.DirInfo(filepath.Join(siaPath, fi.Name()))
			if err != nil {
				return nil, nil, err
			}
			dirs = append(dirs, di)
			continue
		}
		// Ignore non siafiles
		ext := filepath.Ext(fi.Name())
		if ext != siafile.ShareExtension {
			continue
		}
		// Grab siafile
		fileName := strings.TrimSuffix(fi.Name(), siafile.ShareExtension)
		file, err := r.File(filepath.Join(siaPath, fileName))
		if err != nil {
			return nil, nil, err
		}
		files = append(files, file)
	}
	return dirs, files, nil
}

// RenameDir takes an existing directory and changes the path. The original
// directory must exist, and there must not be any directory that already has
// the replacement path.  All sia files within directory will also be renamed
//
// TODO: implement, need to rename directory and walk through and rename all sia
// files within func (r *Renter) RenameDir(currentPath, newPath string) error {
//  return nil
// }
