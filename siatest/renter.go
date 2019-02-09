package siatest

import (
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"reflect"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/errors"
)

// DownloadToDisk downloads a previously uploaded file. The file will be downloaded
// to a random location and returned as a LocalFile object.
func (tn *TestNode) DownloadToDisk(rf *RemoteFile, async bool) (*LocalFile, error) {
	fi, err := tn.File(rf)
	if err != nil {
		return nil, errors.AddContext(err, "failed to retrieve FileInfo")
	}
	// Create a random destination for the download
	fileName := fmt.Sprintf("%dbytes %s", fi.Filesize, hex.EncodeToString(fastrand.Bytes(4)))
	dest := filepath.Join(tn.downloadDir.path, fileName)
	if err := tn.RenterDownloadGet(rf.SiaPath(), dest, 0, fi.Filesize, async); err != nil {
		return nil, errors.AddContext(err, "failed to download file")
	}
	// Create the TestFile
	lf := &LocalFile{
		path:     dest,
		size:     int(fi.Filesize),
		checksum: rf.Checksum(),
	}
	// If we download the file asynchronously we are done
	if async {
		return lf, nil
	}
	// Verify checksum if we downloaded the file blocking
	if err := lf.checkIntegrity(); err != nil {
		return lf, errors.AddContext(err, "downloaded file's checksum doesn't match")
	}
	return lf, nil
}

// DownloadToDiskPartial downloads a part of a previously uploaded file. The
// file will be downloaded to a random location and returned as a LocalFile
// object.
func (tn *TestNode) DownloadToDiskPartial(rf *RemoteFile, lf *LocalFile, async bool, offset, length uint64) (*LocalFile, error) {
	fi, err := tn.File(rf)
	if err != nil {
		return nil, errors.AddContext(err, "failed to retrieve FileInfo")
	}
	// Create a random destination for the download
	fileName := fmt.Sprintf("%dbytes %s", fi.Filesize, hex.EncodeToString(fastrand.Bytes(4)))
	dest := filepath.Join(tn.downloadDir.path, fileName)
	if err := tn.RenterDownloadGet(rf.siaPath, dest, offset, length, async); err != nil {
		return nil, errors.AddContext(err, "failed to download file")
	}
	// Create the TestFile
	destFile := &LocalFile{
		path:     dest,
		size:     int(fi.Filesize),
		checksum: rf.checksum,
	}
	// If we download the file asynchronously we are done
	if async {
		return destFile, nil
	}
	// Verify checksum if we downloaded the file blocking and if lf was
	// provided.
	if lf != nil {
		var checksum crypto.Hash
		checksum, err = lf.partialChecksum(offset, offset+length)
		if err != nil {
			return nil, errors.AddContext(err, "failed to get partial checksum")
		}
		data, err := ioutil.ReadFile(dest)
		if err != nil {
			return nil, errors.AddContext(err, "failed to read downloaded file")
		}
		if checksum != crypto.HashBytes(data) {
			return nil, fmt.Errorf("downloaded bytes don't match requested data %v-%v", offset, length)
		}
	}
	return destFile, nil
}

// DownloadByStream downloads a file and returns its contents as a slice of bytes.
func (tn *TestNode) DownloadByStream(rf *RemoteFile) (data []byte, err error) {
	fi, err := tn.File(rf)
	if err != nil {
		return nil, errors.AddContext(err, "failed to retrieve FileInfo")
	}
	data, err = tn.RenterDownloadHTTPResponseGet(rf.SiaPath(), 0, fi.Filesize)
	if err == nil && rf.Checksum() != crypto.HashBytes(data) {
		err = errors.New("downloaded bytes don't match requested data")
	}
	return
}

// Rename renames a remoteFile and returns the new file.
func (tn *TestNode) Rename(rf *RemoteFile, newPath string) (*RemoteFile, error) {
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
func (tn *TestNode) Files() ([]modules.FileInfo, error) {
	rf, err := tn.RenterFilesGet()
	if err != nil {
		return nil, err
	}
	return rf.Files, err
}

// Upload uses the node to upload the file with the option to overwrite if exists.
func (tn *TestNode) Upload(lf *LocalFile, dataPieces, parityPieces uint64, force bool) (*RemoteFile, error) {
	// Upload file
	siapath := tn.SiaPath(lf.path)
	err := tn.RenterUploadForcePost(lf.path, siapath, dataPieces, parityPieces, force)
	if err != nil {
		return nil, err
	}
	// Create remote file object
	rf := &RemoteFile{
		siaPath:  siapath,
		checksum: lf.checksum,
	}
	// Make sure renter tracks file
	_, err = tn.File(rf)
	if err != nil {
		return rf, errors.AddContext(err, "uploaded file is not tracked by the renter")
	}
	return rf, nil
}

// UploadDirectory uses the node to upload a directory
func (tn *TestNode) UploadDirectory(ld *LocalDir) (*RemoteDir, error) {
	// Upload Directory
	siapath := tn.SiaPath(ld.path)
	err := tn.RenterDirCreatePost(siapath)
	if err != nil {
		return nil, errors.AddContext(err, "failed to upload directory")
	}

	// Create remote directory object
	rd := &RemoteDir{
		siapath: siapath,
	}
	return rd, nil
}

// UploadNewDirectory uses the node to create and upload a directory with a
// random name
func (tn *TestNode) UploadNewDirectory() (*RemoteDir, error) {
	return tn.UploadDirectory(tn.NewLocalDir())
}

// UploadNewFile initiates the upload of a filesize bytes large file with the option to overwrite if exists.
func (tn *TestNode) UploadNewFile(filesize int, dataPieces uint64, parityPieces uint64, force bool) (*LocalFile, *RemoteFile, error) {
	// Create file for upload
	localFile, err := tn.filesDir.NewFile(filesize)
	if err != nil {
		return nil, nil, errors.AddContext(err, "failed to create file")
	}
	// Upload file, creating a parity piece for each host in the group
	remoteFile, err := tn.Upload(localFile, dataPieces, parityPieces, force)
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
	// Wait until upload reaches a certain redundancy
	err = tn.WaitForUploadRedundancy(remoteFile, float64((dataPieces+parityPieces))/float64(dataPieces))
	return localFile, remoteFile, err
}

// UploadBlocking attempts to upload an existing file with the option to overwrite if exists
// and waits for the upload to reach 100% progress and redundancy.
func (tn *TestNode) UploadBlocking(localFile *LocalFile, dataPieces uint64, parityPieces uint64, force bool) (*RemoteFile, error) {
	// Upload file, creating a parity piece for each host in the group
	remoteFile, err := tn.Upload(localFile, dataPieces, parityPieces, force)
	if err != nil {
		return nil, errors.AddContext(err, "failed to start upload")
	}

	// Wait until upload reached the specified progress
	if err = tn.WaitForUploadProgress(remoteFile, 1); err != nil {
		return nil, err
	}

	// Wait until upload reaches a certain redundancy
	err = tn.WaitForUploadRedundancy(remoteFile, float64((dataPieces+parityPieces))/float64(dataPieces))
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
		return errors.New("file is not tracked by renter")
	}
	// Wait until it reaches the progress
	return Retry(1000, 100*time.Millisecond, func() error {
		file, err := tn.File(rf)
		if err != nil {
			return errors.AddContext(err, "couldn't retrieve FileInfo")
		}
		if file.UploadProgress < progress {
			return fmt.Errorf("progress should be %v but was %v", progress, file.UploadProgress)
		}
		return nil
	})

}

// WaitForUploadRedundancy waits for a file to reach a certain upload redundancy.
func (tn *TestNode) WaitForUploadRedundancy(rf *RemoteFile, redundancy float64) error {
	// Check if file is tracked by renter at all
	if _, err := tn.File(rf); err != nil {
		return errors.New("file is not tracked by renter")
	}
	// Wait until it reaches the redundancy
	err := Retry(600, 100*time.Millisecond, func() error {
		file, err := tn.File(rf)
		if err != nil {
			return errors.AddContext(err, "couldn't retrieve FileInfo")
		}
		if file.Redundancy < redundancy {
			return fmt.Errorf("redundancy should be %v but was %v", redundancy, file.Redundancy)
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
		return errors.New("file is not tracked by renter")
	}
	// Wait until it reaches the redundancy
	return Retry(1000, 100*time.Millisecond, func() error {
		file, err := tn.File(rf)
		if err != nil {
			return errors.AddContext(err, "couldn't retrieve FileInfo")
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
		rd, err := tn.RenterGetDir("")
		if err != nil {
			return err
		}
		if rd.Directories[0].NumStuckChunks == 0 {
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
		rd, err := tn.RenterGetDir("")
		if err != nil {
			return err
		}
		if rd.Directories[0].NumStuckChunks != 0 {
			return fmt.Errorf("%v stuck chunks found, expected 0", rd.Directories[0].NumStuckChunks)
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
