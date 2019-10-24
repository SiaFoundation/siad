package dependencies

import (
	"gitlab.com/NebulousLabs/Sia/modules"
)

// DependencyBlockScan blocks the scan progress of the hostdb until Scan is
// called on the dependency.
type DependencyBlockScan struct {
	modules.ProductionDependencies
	closed bool
	c      chan struct{}
}

// Disrupt will block the scan progress of the hostdb. The scan can be started
// by calling Scan on the dependency.
func (d *DependencyBlockScan) Disrupt(s string) bool {
	if d.c == nil {
		d.c = make(chan struct{})
	}
	if s == "BlockScan" {
		<-d.c
	}
	return false
}

// Scan resumes the blocked scan.
func (d *DependencyBlockScan) Scan() {
	if d.closed {
		return
	}
	close(d.c)
	d.closed = true
}
