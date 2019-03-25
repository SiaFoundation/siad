package siatest

import (
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
)

type (
	// RemoteFile is a helper struct that represents a file uploaded to the Sia
	// network.
	RemoteFile struct {
		checksum crypto.Hash
		siaPath  string

		mu sync.Mutex
	}
)

// Checksum returns the checksum of a remote file.
func (rf *RemoteFile) Checksum() crypto.Hash {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.checksum
}

// SiaPath returns the siaPath of a remote file.
func (rf *RemoteFile) SiaPath() string {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.siaPath
}
