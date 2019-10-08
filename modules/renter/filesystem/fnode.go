package filesystem

import "gitlab.com/NebulousLabs/Sia/modules/renter/siafile"

type (
	// fNode is a node which references a SiaFile.
	fNode struct {
		node

		*siafile.SiaFile
	}
)

// Close calls close on the underlying node and also removes the fNode from its
// parent.
func (n *fNode) Close() {
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

// Delete deletes the fNode's underlying file from disk.
func (n *fNode) Delete() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.SiaFile.Delete()
}
