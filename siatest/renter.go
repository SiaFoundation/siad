package siatest

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/persist"
)

var (
	// ErrFileNotTracked is an error returned by the TestNode in case a file
	// wasn't accessible due to being unknown to the renter.
	ErrFileNotTracked = errors.New("file is not tracked by renter")
)

// DownloadToDisk downloads a previously uploaded file. The file will be downloaded
// to a random location and returned as a LocalFile object.
func (tn *TestNode) DownloadToDisk(rf *RemoteFile, async bool) (modules.DownloadID, *LocalFile, error) {
	fi, err := tn.File(rf)
	if err != nil {
		return "", nil, errors.AddContext(err, "failed to retrieve FileInfo")
	}
	// Create a random destination for the download
	fileName := fmt.Sprintf("%dbytes %s", fi.Filesize, persist.RandomSuffix())
	dest := filepath.Join(tn.downloadDir.path, fileName)
	uid, err := tn.RenterDownloadGet(rf.SiaPath(), dest, 0, fi.Filesize, async)
	if err != nil {
		return "", nil, errors.AddContext(err, "failed to download file")
	}
	// Make sure the download is in the history.
	_, err = tn.RenterDownloadInfoGet(uid)
	if err != nil {
		return "", nil, errors.AddContext(err, "failed to fetch download info")
	}

	// Create the TestFile
	lf := &LocalFile{
		path:     dest,
		size:     int(fi.Filesize),
		checksum: rf.Checksum(),
	}
	// If we download the file asynchronously we are done
	if async {
		return uid, lf, nil
	}
	// Verify checksum if we downloaded the file blocking
	if err := lf.checkIntegrity(); err != nil {
		return "", lf, errors.AddContext(err, "downloaded file's checksum doesn't match")
	}
	return uid, lf, nil
}

// DownloadToDiskPartial downloads a part of a previously uploaded file. The
// file will be downloaded to a random location and returned as a LocalFile
// object.
func (tn *TestNode) DownloadToDiskPartial(rf *RemoteFile, lf *LocalFile, async bool, offset, length uint64) (modules.DownloadID, *LocalFile, error) {
	fi, err := tn.File(rf)
	if err != nil {
		return "", nil, errors.AddContext(err, "failed to retrieve FileInfo")
	}
	// Create a random destination for the download
	fileName := fmt.Sprintf("%dbytes %s", fi.Filesize, persist.RandomSuffix())
	dest := filepath.Join(tn.downloadDir.path, fileName)
	uid, err := tn.RenterDownloadGet(rf.siaPath, dest, offset, length, async)
	if err != nil {
		return "", nil, errors.AddContext(err, "failed to download file")
	}
	// Make sure the download is in the history.
	_, err = tn.RenterDownloadInfoGet(uid)
	if err != nil {
		return "", nil, errors.AddContext(err, "failed to fetch download info")
	}
	// Create the TestFile
	destFile := &LocalFile{
		path:     dest,
		size:     int(fi.Filesize),
		checksum: rf.checksum,
	}
	// If we download the file asynchronously we are done
	if async {
		return uid, destFile, nil
	}
	// Verify checksum if we downloaded the file blocking and if lf was
	// provided.
	if lf != nil {
		var checksum crypto.Hash
		checksum, err = lf.partialChecksum(offset, offset+length)
		if err != nil {
			return "", nil, errors.AddContext(err, "failed to get partial checksum")
		}
		data, err := ioutil.ReadFile(dest)
		if err != nil {
			return "", nil, errors.AddContext(err, "failed to read downloaded file")
		}
		if checksum != crypto.HashBytes(data) {
			return "", nil, fmt.Errorf("downloaded bytes don't match requested data %v-%v", offset, length)
		}
	}
	return uid, destFile, nil
}

// DownloadByStream downloads a file and returns its contents as a slice of bytes.
func (tn *TestNode) DownloadByStream(rf *RemoteFile) (uid modules.DownloadID, data []byte, err error) {
	fi, err := tn.File(rf)
	if err != nil {
		return "", nil, errors.AddContext(err, "failed to retrieve FileInfo")
	}
	uid, data, err = tn.RenterDownloadHTTPResponseGet(rf.SiaPath(), 0, fi.Filesize)
	if err == nil && rf.Checksum() != crypto.HashBytes(data) {
		err = fmt.Errorf("downloaded bytes don't match requested data (len %v)", len(data))
	}
	// Make sure the download is in the history.
	_, err = tn.RenterDownloadInfoGet(uid)
	if err != nil {
		return "", nil, errors.AddContext(err, "failed to fetch download info")
	}
	return
}

// Rename renames a remoteFile and returns the new file.
func (tn *TestNode) Rename(rf *RemoteFile, newPath modules.SiaPath) (*RemoteFile, error) {
	err := tn.RenterRenamePost(rf.SiaPath(), newPath)
	if err != nil {
		return nil, err
	}
	rf.mu.Lock()
	rf.siaPath = newPath
	rf.mu.Unlock()
	return rf, nil
}

// SetFileRepairPath changes the repair path of a remote file to the provided
// local file's path.
func (tn *TestNode) SetFileRepairPath(rf *RemoteFile, lf *LocalFile) error {
	return tn.RenterSetRepairPathPost(rf.siaPath, lf.path)
}

// Stream uses the streaming endpoint to download a file.
func (tn *TestNode) Stream(rf *RemoteFile) (data []byte, err error) {
	data, err = tn.RenterStreamGet(rf.siaPath)
	if err == nil && rf.checksum != crypto.HashBytes(data) {
		err = errors.New("downloaded bytes don't match requested data")
	}
	return
}

// StreamPartial uses the streaming endpoint to download a partial file in
// range [from;to]. A local file can be provided optionally to implicitly check
// the checksum of the downloaded data.
func (tn *TestNode) StreamPartial(rf *RemoteFile, lf *LocalFile, from, to uint64) (data []byte, err error) {
	data, err = tn.RenterStreamPartialGet(rf.siaPath, from, to)
	if err != nil {
		return
	}
	if uint64(len(data)) != to-from {
		err = fmt.Errorf("length of downloaded data should be %v but was %v",
			to-from+1, len(data))
		return
	}
	if lf != nil {
		var checksum crypto.Hash
		checksum, err = lf.partialChecksum(from, to)
		if err != nil {
			err = errors.AddContext(err, "failed to get partial checksum")
			return
		}
		if checksum != crypto.HashBytes(data) {
			err = fmt.Errorf("downloaded bytes don't match requested data %v-%v", from, to)
			return
		}
	}
	return
}

// DownloadInfo returns the DownloadInfo struct of a file. If it returns nil,
// the download has either finished, or was never started in the first place.
// If the corresponding download info was found, DownloadInfo also performs a
// few sanity checks on its fields.
func (tn *TestNode) DownloadInfo(lf *LocalFile, rf *RemoteFile) (*api.DownloadInfo, error) {
	rdq, err := tn.RenterDownloadsGet()
	if err != nil {
		return nil, err
	}
	var di *api.DownloadInfo
	for _, d := range rdq.Downloads {
		if rf.siaPath == d.SiaPath && lf.path == d.Destination {
			di = &d
			break
		}
	}
	if di == nil {
		// No download info found.
		return nil, errors.New("download info not found")
	}
	// Check if length and filesize were set correctly
	if di.Length != di.Filesize {
		err = errors.AddContext(err, "filesize != length")
	}
	// Received data can't be larger than transferred data
	if di.Received > di.TotalDataTransferred {
		err = errors.AddContext(err, "received > TotalDataTransferred")
	}
	// If the download is completed, the amount of received data has to equal
	// the amount of requested data.
	if di.Completed && di.Received != di.Length {
		err = errors.AddContext(err, "completed == true but received != length")
	}
	return di, err
}

// File returns the file queried by the user
func (tn *TestNode) File(rf *RemoteFile) (modules.FileInfo, error) {
	rfile, err := tn.RenterFileGet(rf.SiaPath())
	if err != nil {
		return rfile.File, err
	}
	return rfile.File, err
}

// Files lists the files tracked by the renter
func (tn *TestNode) Files(cached bool) ([]modules.FileInfo, error) {
	rf, err := tn.RenterFilesGet(cached)
	if err != nil {
		return nil, err
	}
	return rf.Files, err
}

// Upload uses the node to upload the file with the option to overwrite if exists.
func (tn *TestNode) Upload(lf *LocalFile, siapath modules.SiaPath, dataPieces, parityPieces uint64, force bool) (*RemoteFile, error) {
	// Upload file
	err := tn.RenterUploadForcePost(lf.path, siapath, dataPieces, parityPieces, force)
	if err != nil {
		return nil, errors.AddContext(err, "unable to upload from "+lf.path+" to "+siapath.String())
	}
	// Create remote file object
	rf := &RemoteFile{
		siaPath:  siapath,
		checksum: lf.checksum,
	}
	// Make sure renter tracks file
	_, err = tn.File(rf)
	if err != nil {
		return rf, ErrFileNotTracked
	}
	return rf, nil
}

// UploadDirectory uses the node to upload a directory
func (tn *TestNode) UploadDirectory(ld *LocalDir) (*RemoteDir, error) {
	// Check for edge cases.
	if ld == nil {
		return nil, errors.New("cannot upload a nil localdir")
	}
	stat, err := os.Stat(ld.path)
	if err != nil {
		return nil, errors.AddContext(err, "unable to stat local dir path")
	}
	if !stat.IsDir() {
		return nil, errors.AddContext(err, "cannot upload a directory if it's a file")
	}

	// Walk through the directory and create any dirs.
	err = filepath.Walk(ld.path, func(path string, info os.FileInfo, err error) error {
		// Upload the directory if it is a directory.
		if info.IsDir() {
			createErr := tn.RenterDirCreatePost(tn.SiaPath(path))
			return errors.AddContext(createErr, "unable to upload a directory")
		}

		// Upload the file because it's a file.
		siapath := tn.SiaPath(path)
		uploadErr := tn.RenterUploadDefaultPost(path, siapath)
		return errors.AddContext(uploadErr, "unable to upload a file")
	})
	if err != nil {
		return nil, errors.AddContext(err, "ran into issues during filepath.Walk")
	}

	// Create remote directory object
	rd := &RemoteDir{
		siapath: tn.SiaPath(ld.path),
	}
	return rd, nil
}

// UploadNewDirectory uses the node to create and upload a directory with a
// random name
func (tn *TestNode) UploadNewDirectory() (*RemoteDir, error) {
	ld, err := tn.NewLocalDir()
	if err != nil {
		return nil, errors.AddContext(err, "unable to create new directory for uploading")
	}
	return tn.UploadDirectory(ld)
}

// UploadNewFile initiates the upload of a filesize bytes large file with the option to overwrite if exists.
func (tn *TestNode) UploadNewFile(filesize int, dataPieces uint64, parityPieces uint64, force bool) (*LocalFile, *RemoteFile, error) {
	// Create file for upload
	localFile, err := tn.filesDir.NewFile(filesize)
	if err != nil {
		return nil, nil, errors.AddContext(err, "failed to create file")
	}
	// Upload file, creating a parity piece for each host in the group
	remoteFile, err := tn.Upload(localFile, tn.SiaPath(localFile.path), dataPieces, parityPieces, force)
	if err != nil {
		return nil, nil, errors.AddContext(err, "failed to start upload")
	}
	return localFile, remoteFile, nil
}

// UploadNewFileBlocking uploads a filesize bytes large file with the option to overwrite if exists
// and waits for the upload to reach 100% progress and redundancy.
func (tn *TestNode) UploadNewFileBlocking(filesize int, dataPieces uint64, parityPieces uint64, force bool) (*LocalFile, *RemoteFile, error) {
	localFile, remoteFile, err := tn.UploadNewFile(filesize, dataPieces, parityPieces, force)
	if err != nil {
		return nil, nil, err
	}
	// Wait until upload reached the specified progress
	if err = tn.WaitForUploadProgress(remoteFile, 1); err != nil {
		return nil, nil, err
	}
	// Wait until upload reaches a certain health
	err = tn.WaitForUploadHealth(remoteFile)
	return localFile, remoteFile, err
}

// Dirs returns the siapaths of all dirs of the TestNode's renter in no
// deterministic order.
func (tn *TestNode) Dirs() ([]modules.SiaPath, error) {
	// dirs always contains the root dir.
	dirs := []modules.SiaPath{modules.RootSiaPath()}
	toVisit := []modules.SiaPath{modules.RootSiaPath()}

	// As long as we find new dirs we add them to dirs.
	for len(toVisit) > 0 {
		// Pop of the first dir.
		d := toVisit[0]
		toVisit = toVisit[1:]

		// Add the first dir to dirs.
		dirs = append(dirs, d)

		// Get the dir info.
		rd, err := tn.RenterGetDir(d)
		if err != nil {
			return nil, err
		}
		// Append the subdirs to toVisit.
		for _, di := range rd.Directories {
			if di.SiaPath != d {
				toVisit = append(toVisit, di.SiaPath)
			}
		}
	}
	return dirs, nil
}

// UploadBlocking attempts to upload an existing file with the option to overwrite if exists
// and waits for the upload to reach 100% progress and redundancy.
func (tn *TestNode) UploadBlocking(localFile *LocalFile, dataPieces uint64, parityPieces uint64, force bool) (*RemoteFile, error) {
	// Upload file, creating a parity piece for each host in the group
	remoteFile, err := tn.Upload(localFile, tn.SiaPath(localFile.path), dataPieces, parityPieces, force)
	if err != nil {
		return nil, errors.AddContext(err, "failed to start upload")
	}

	// Wait until upload reached the specified progress
	if err = tn.WaitForUploadProgress(remoteFile, 1); err != nil {
		return nil, err
	}

	// Wait until upload reaches a certain health
	err = tn.WaitForUploadHealth(remoteFile)
	return remoteFile, err
}

// WaitForDownload waits for the download of a file to finish. If a file wasn't
// scheduled for download it will return instantly without an error. If parent
// is provided, it will compare the contents of the downloaded file to the
// contents of tf2 after the download is finished. WaitForDownload also
// verifies the checksum of the downloaded file.
func (tn *TestNode) WaitForDownload(lf *LocalFile, rf *RemoteFile) error {
	var downloadErr error
	err := Retry(1000, 100*time.Millisecond, func() error {
		file, err := tn.DownloadInfo(lf, rf)
		if err != nil {
			return errors.AddContext(err, "couldn't retrieve DownloadInfo")
		}
		if file == nil {
			return nil
		}
		if !file.Completed {
			return errors.New("download hasn't finished yet")
		}
		if file.Error != "" {
			downloadErr = errors.New(file.Error)
		}
		return nil
	})
	if err != nil || downloadErr != nil {
		return errors.Compose(err, downloadErr)
	}
	// Verify checksum
	return lf.checkIntegrity()
}

// WaitForUploadProgress waits for a file to reach a certain upload progress.
func (tn *TestNode) WaitForUploadProgress(rf *RemoteFile, progress float64) error {
	if _, err := tn.File(rf); err != nil {
		return ErrFileNotTracked
	}
	// Wait until it reaches the progress
	return Retry(1000, 100*time.Millisecond, func() error {
		file, err := tn.File(rf)
		if err != nil {
			return ErrFileNotTracked
		}
		if file.UploadProgress < progress {
			return fmt.Errorf("progress should be %v but was %v", progress, file.UploadProgress)
		}
		return nil
	})

}

// WaitForUploadHealth waits for a file to reach a health better than the
// RepairThreshold.
func (tn *TestNode) WaitForUploadHealth(rf *RemoteFile) error {
	// Check if file is tracked by renter at all
	if _, err := tn.File(rf); err != nil {
		return ErrFileNotTracked
	}
	// Wait until the file is viewed as healthy by the renter
	err := Retry(1000, 100*time.Millisecond, func() error {
		file, err := tn.File(rf)
		if err != nil {
			return ErrFileNotTracked
		}
		if file.MaxHealth >= renter.RepairThreshold {
			return fmt.Errorf("file is not healthy yet, threshold is %v but health is %v", renter.RepairThreshold, file.MaxHealth)
		}
		return nil
	})
	if err != nil {
		rc, err2 := tn.RenterContractsGet()
		if err2 != nil {
			return errors.Compose(err, err2)
		}
		goodHosts := 0
		for _, contract := range rc.Contracts {
			if contract.GoodForUpload {
				goodHosts++
			}
		}
		return errors.Compose(err, fmt.Errorf("%v available hosts", goodHosts))
	}
	return nil
}

// WaitForDecreasingRedundancy waits until the redundancy decreases to a
// certain point.
func (tn *TestNode) WaitForDecreasingRedundancy(rf *RemoteFile, redundancy float64) error {
	// Check if file is tracked by renter at all
	if _, err := tn.File(rf); err != nil {
		return ErrFileNotTracked
	}
	// Wait until it reaches the redundancy
	return Retry(1000, 100*time.Millisecond, func() error {
		file, err := tn.File(rf)
		if err != nil {
			return ErrFileNotTracked
		}
		if file.Redundancy > redundancy {
			return fmt.Errorf("redundancy should be %v but was %v", redundancy, file.Redundancy)
		}
		return nil
	})
}

// WaitForStuckChunksToBubble waits until the stuck chunks have been bubbled to
// the root directory metadata
func (tn *TestNode) WaitForStuckChunksToBubble() error {
	// Wait until the root directory no long reports no stuck chunks
	return build.Retry(1000, 100*time.Millisecond, func() error {
		rd, err := tn.RenterGetDir(modules.RootSiaPath())
		if err != nil {
			return err
		}
		if rd.Directories[0].AggregateNumStuckChunks == 0 {
			return errors.New("no stuck chunks found")
		}
		return nil
	})
}

// WaitForStuckChunksToRepair waits until the stuck chunks have been repaired
// and bubbled to the root directory metadata
func (tn *TestNode) WaitForStuckChunksToRepair() error {
	// Wait until the root directory no long reports no stuck chunks
	return build.Retry(1000, 100*time.Millisecond, func() error {
		rd, err := tn.RenterGetDir(modules.RootSiaPath())
		if err != nil {
			return err
		}
		if rd.Directories[0].AggregateNumStuckChunks != 0 {
			return fmt.Errorf("%v stuck chunks found, expected 0", rd.Directories[0].AggregateNumStuckChunks)
		}
		return nil
	})
}

// KnowsHost checks if tn has a certain host in its hostdb. This check is
// performed using the host's public key.
func (tn *TestNode) KnowsHost(host *TestNode) error {
	hdag, err := tn.HostDbActiveGet()
	if err != nil {
		return err
	}
	for _, h := range hdag.Hosts {
		pk, err := host.HostPublicKey()
		if err != nil {
			return err
		}
		if reflect.DeepEqual(h.PublicKey, pk) {
			return nil
		}
	}
	return errors.New("host ist unknown")
}
