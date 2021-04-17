package filesystem

import (
	"math"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter/filesystem/siafile"
	"go.sia.tech/siad/types"
)

type (
	// FileNode is a node which references a SiaFile.
	FileNode struct {
		node
		closed bool

		*siafile.SiaFile
	}
)

// AddPiece wraps siafile.AddPiece to guarantee that it's not called when the
// fileNode was already closed.
func (n *FileNode) AddPiece(pk types.SiaPublicKey, chunkIndex, pieceIndex uint64, merkleRoot crypto.Hash) (err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		err := errors.New("AddPiece called on close FileNode")
		build.Critical(err)
		return err
	}
	return n.SiaFile.AddPiece(pk, chunkIndex, pieceIndex, merkleRoot)
}

// close closes the file and removes it from the parent if it was the last open
// instance.
// NOTE: If the file has a parent, it needs to be already locked when this is
// called.
func (n *FileNode) close() {
	// Mark node as closed and sanity check that it hasn't been closed before.
	if n.closed {
		build.Critical("close called multiple times on same FileNode")
	}
	n.closed = true

	// Call common close method.
	n.node.closeNode()

	// Remove node from parent if the current thread was the last one.
	parent := n.parent
	if parent != nil && len(n.threads) == 0 {
		parent.removeFile(n)
	}
}

// close closes the file and removes it from the parent if it was the last open
// instance.
// NOTE: If the file has a parent, it needs to be already locked when this is
// called.
func (n *FileNode) managedClose() {
	n.node.mu.Lock()
	defer n.node.mu.Unlock()
	n.close()
}

// Close calls close on the FileNode and also removes the FileNode from its
// parent if it's no longer being used and if it doesn't have any children which
// are currently in use. This happens iteratively for all parent as long as
// removing a child resulted in them not having any children left.
func (n *FileNode) Close() error {
	// If a parent exists, we need to lock it while closing a child.
	parent := n.node.managedLockWithParent()

	// close the node.
	n.close()

	// Unlock child and parent.
	n.node.mu.Unlock()
	if parent != nil {
		parent.node.mu.Unlock()
		// Check if the parent needs to be removed from its parent too.
		parent.managedTryRemoveFromParentsIteratively()
	}
	return nil
}

// Copy copies a file node and returns the copy.
func (n *FileNode) Copy() *FileNode {
	return n.managedCopy()
}

// managedCopy copies a file node and returns the copy.
func (n *FileNode) managedCopy() *FileNode {
	n.node.mu.Lock()
	defer n.node.mu.Unlock()
	newNode := *n
	newNode.closed = false
	newNode.threadUID = newThreadUID()
	newNode.threads[newNode.threadUID] = struct{}{}
	return &newNode
}

// Delete deletes the fNode's underlying file from disk.
func (n *FileNode) managedDelete() error {
	n.node.mu.Lock()
	defer n.node.mu.Unlock()
	return n.SiaFile.Delete()
}

// managedMode returns the underlying file's os.FileMode.
func (n *FileNode) managedMode() os.FileMode {
	n.node.mu.Lock()
	defer n.node.mu.Unlock()
	return n.SiaFile.Mode()
}

// managedFileInfo returns the FileInfo of the file node.
func (n *FileNode) managedFileInfo(siaPath modules.SiaPath, offline map[string]bool, goodForRenew map[string]bool, contracts map[string]modules.RenterContract) (modules.FileInfo, error) {
	// Build the FileInfo
	var onDisk bool
	localPath := n.LocalPath()
	if localPath != "" {
		_, err := os.Stat(localPath)
		onDisk = err == nil
	}
	_, _, health, stuckHealth, numStuckChunks, repairBytes, stuckBytes := n.Health(offline, goodForRenew)
	_, redundancy, err := n.Redundancy(offline, goodForRenew)
	if err != nil {
		return modules.FileInfo{}, errors.AddContext(err, "failed to get n redundancy")
	}
	uploadProgress, uploadedBytes, err := n.UploadProgressAndBytes()
	if err != nil {
		return modules.FileInfo{}, errors.AddContext(err, "failed to get upload progress and bytes")
	}
	maxHealth := math.Max(health, stuckHealth)
	fileInfo := modules.FileInfo{
		AccessTime:       n.AccessTime(),
		Available:        redundancy >= 1,
		ChangeTime:       n.ChangeTime(),
		CipherType:       n.MasterKey().Type().String(),
		CreateTime:       n.CreateTime(),
		Expiration:       n.Expiration(contracts),
		Filesize:         n.Size(),
		Health:           health,
		LocalPath:        localPath,
		MaxHealth:        maxHealth,
		MaxHealthPercent: modules.HealthPercentage(maxHealth),
		ModificationTime: n.ModTime(),
		NumStuckChunks:   numStuckChunks,
		OnDisk:           onDisk,
		Recoverable:      onDisk || redundancy >= 1,
		Redundancy:       redundancy,
		Renewing:         true,
		RepairBytes:      repairBytes,
		SiaPath:          siaPath,
		Stuck:            numStuckChunks > 0,
		StuckHealth:      stuckHealth,
		StuckBytes:       stuckBytes,
		UID:              n.staticUID,
		UploadedBytes:    uploadedBytes,
		UploadProgress:   uploadProgress,
	}
	return fileInfo, nil
}

// managedRename renames the fNode's underlying file.
func (n *FileNode) managedRename(newName string, oldParent, newParent *DirNode) error {
	// Lock the parents. If they are the same, only lock one.
	if oldParent.staticUID == newParent.staticUID {
		oldParent.node.mu.Lock()
		defer oldParent.node.mu.Unlock()
	} else {
		oldParent.node.mu.Lock()
		defer oldParent.node.mu.Unlock()
		newParent.node.mu.Lock()
		defer newParent.node.mu.Unlock()
	}
	n.node.mu.Lock()
	defer n.node.mu.Unlock()
	// Check that newParent doesn't have a file or folder with that name
	// already.
	if exists := newParent.childExists(newName); exists {
		return ErrExists
	}
	newPath := filepath.Join(newParent.absPath(), newName) + modules.SiaFileExtension
	// Rename the file.
	err := n.SiaFile.Rename(newPath)
	if errors.Contains(err, siafile.ErrPathOverload) {
		return ErrExists
	}
	if err != nil {
		return err
	}
	// Remove file from old parent and add it to new parent.
	// TODO: iteratively remove parents like in Close
	oldParent.removeFile(n)
	// Update parent and name.
	n.parent = newParent
	*n.name = newName
	*n.path = newPath
	// Add file to new parent.
	n.parent.files[*n.name] = n
	return err
}

// cachedFileInfo returns information on a siafile. As a performance
// optimization, the fileInfo takes the maps returned by
// renter.managedContractUtilityMaps for many files at once.
func (n *FileNode) staticCachedInfo(siaPath modules.SiaPath) (modules.FileInfo, error) {
	md := n.Metadata()

	// Build the FileInfo
	var onDisk bool
	localPath := md.LocalPath
	if localPath != "" {
		_, err := os.Stat(localPath)
		onDisk = err == nil
	}
	maxHealth := math.Max(md.CachedHealth, md.CachedStuckHealth)
	fileInfo := modules.FileInfo{
		AccessTime:       md.AccessTime,
		Available:        md.CachedUserRedundancy >= 1,
		ChangeTime:       md.ChangeTime,
		CipherType:       md.StaticMasterKeyType.String(),
		CreateTime:       md.CreateTime,
		Expiration:       md.CachedExpiration,
		Filesize:         uint64(md.FileSize),
		Health:           md.CachedHealth,
		LocalPath:        localPath,
		MaxHealth:        maxHealth,
		MaxHealthPercent: modules.HealthPercentage(maxHealth),
		ModificationTime: md.ModTime,
		NumStuckChunks:   md.NumStuckChunks,
		OnDisk:           onDisk,
		Recoverable:      onDisk || md.CachedUserRedundancy >= 1,
		Redundancy:       md.CachedUserRedundancy,
		Renewing:         true,
		RepairBytes:      md.CachedRepairBytes,
		SiaPath:          siaPath,
		Stuck:            md.NumStuckChunks > 0,
		StuckBytes:       md.CachedStuckBytes,
		StuckHealth:      md.CachedStuckHealth,
		UID:              n.staticUID,
		UploadedBytes:    md.CachedUploadedBytes,
		UploadProgress:   md.CachedUploadProgress,
	}
	return fileInfo, nil
}
