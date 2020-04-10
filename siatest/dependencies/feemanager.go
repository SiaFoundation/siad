package dependencies

// NewDependencyProcessFeeFail creates a new dependency that simulates getting
// an error while processing a fee.
func NewDependencyProcessFeeFail() *DependencyWithDisableAndEnable {
	return newDependencywithDisableAndEnable("ProcessFeeFail")
}
