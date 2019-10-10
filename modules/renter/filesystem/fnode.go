package filesystem

import (
	"gitlab.com/NebulousLabs/Sia/modules"
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
