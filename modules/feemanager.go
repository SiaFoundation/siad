package modules

const (
	// FeeManagerDir is the name of the directory that is used to store the
	// FeeManager's persistent data
	FeeManagerDir = "feemanager"
)

// FeeManager manages fees for applications
type FeeManager interface {
	// Close closes the FeeManager
	Close() error
}
