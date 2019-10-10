package filesystem

import (
	"math"
	"os"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
)

type (
	// FNode is a node which references a SiaFile.
	FNode struct {
		node

		*siafile.SiaFile
	}
)

// Close calls close on the underlying node and also removes the fNode from its
// parent.
func (n *FNode) Close() {
	n.mu.Lock()
	// Call common close method.
	n.node._close()

	// Remove node from parent.
	if len(n.threads) == 0 {
		n.mu.Unlock()
		n.staticParent.managedRemoveFile(n)
		return
	}
	n.mu.Unlock()
}

// Copy copies a file node and returns the copy.
func (n *FNode) Copy() *FNode {
	return n.managedCopy()
}

// managedCopy copies a file node and returns the copy.
func (n *FNode) managedCopy() *FNode {
	n.mu.Lock()
	defer n.mu.Unlock()
	newNode := *n
	newNode.threadUID = newThreadUID()
	newNode.threads[newNode.threadUID] = newThreadType()
	return &newNode
}

// Delete deletes the fNode's underlying file from disk.
func (n *FNode) managedDelete() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.SiaFile.Delete()
}

// managedRename renames the fNode's underlying file.
func (n *FNode) managedRename(newPath string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.SiaFile.Rename(newPath + modules.SiaFileExtension)
}

// cachedFileInfo returns information on a siafile. As a performance
// optimization, the fileInfo takes the maps returned by
// renter.managedContractUtilityMaps for many files at once.
func (n *FNode) staticCachedInfo(siaPath modules.SiaPath, offline map[string]bool, goodForRenew map[string]bool, contracts map[string]modules.RenterContract) (modules.FileInfo, error) {
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
		MaxHealthPercent: siadir.HealthPercentage(maxHealth),
		ModTime:          md.ModTime,
		NumStuckChunks:   md.NumStuckChunks,
		OnDisk:           onDisk,
		Recoverable:      onDisk || md.CachedUserRedundancy >= 1,
		Redundancy:       md.CachedUserRedundancy,
		Renewing:         true,
		SiaPath:          siaPath,
		Stuck:            md.NumStuckChunks > 0,
		StuckHealth:      md.CachedStuckHealth,
		UploadedBytes:    md.CachedUploadedBytes,
		UploadProgress:   md.CachedUploadProgress,
	}
	return fileInfo, nil
}
