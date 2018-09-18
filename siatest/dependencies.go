package siatest

import (
	"net"
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
)

type (
	// DependencyInterruptOnceOnKeyword is a generic dependency that interrupts
	// the flow of the program if the argument passed to Disrupt equals str and
	// if f was set to true by calling Fail.
	DependencyInterruptOnceOnKeyword struct {
		f bool // indicates if the next download should fail
		modules.ProductionDependencies
		mu  sync.Mutex
		str string
	}
)

// NewDependencyInterruptOnceOnKeyword creates a new
// DependencyInterruptOnceOnKeyword from a given disrupt key.
func NewDependencyInterruptOnceOnKeyword(str string) *DependencyInterruptOnceOnKeyword {
	return &DependencyInterruptOnceOnKeyword{
		str: str,
	}
}

// Disrupt returns true if the correct string is provided and if the flag was
// set to true by calling fail on the dependency beforehand. After simulating a
// crash the flag will be set to false and fail has to be called again for
// another disruption.
func (d *DependencyInterruptOnceOnKeyword) Disrupt(s string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f && s == d.str {
		d.f = false
		return true
	}
	return false
}

// Fail causes the next call to Disrupt to return true if the correct string is
// provided.
func (d *DependencyInterruptOnceOnKeyword) Fail() {
	d.mu.Lock()
	d.f = true
	d.mu.Unlock()
}

// Disable sets the flag to false to make sure that the dependency won't fail.
func (d *DependencyInterruptOnceOnKeyword) Disable() {
	d.mu.Lock()
	d.f = false
	d.mu.Unlock()
}

type (
	// customResolver is a testing resolver which can be created from any
	// lookupIP method.
	customResolver struct {
		lookupIP func(string) ([]net.IP, error)
	}
	// dependencyCustomResolver is a dependency which overrides the Resolver
	// method to return a custom resolver with a specific lookupIP method.
	dependencyCustomResolver struct {
		modules.ProductionDependencies
		lookupIP func(string) ([]net.IP, error)
	}
)

// LookupIP implements the modules.Resolver interface.
func (cr customResolver) LookupIP(host string) ([]net.IP, error) {
	return cr.lookupIP(host)
}

// Disrupt makes sure that hosts which resolve to addresses we can't connect to
// due to the customResolver will be online in the hostdb.
func (d *dependencyCustomResolver) Disrupt(s string) bool {
	return s == "customResolver"
}

// Resolver creates a new custom resolver.
func (d *dependencyCustomResolver) Resolver() modules.Resolver {
	return customResolver{d.lookupIP}
}

// NewDependencyCustomResolver creates a dependency from a given lookupIP
// method which returns a custom resolver that uses the specified lookupIP
// method to resolve hostnames.
func NewDependencyCustomResolver(lookupIP func(string) ([]net.IP, error)) modules.Dependencies {
	return &dependencyCustomResolver{lookupIP: lookupIP}
}
