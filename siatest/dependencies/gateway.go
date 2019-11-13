package dependencies

import (
	"gitlab.com/NebulousLabs/Sia/modules"
)

// DependencyDisableAutoOnline will disable the gateway always being online
// during testing and dev builds and instead apply the same rules which are used
// in production builds.
type DependencyDisableAutoOnline struct {
	modules.ProductionDependencies
}

// Disrupt returns true if the correct string is provided.
func (d *DependencyDisableAutoOnline) Disrupt(s string) bool {
	return s == "DisableGatewayAutoOnline"
}
