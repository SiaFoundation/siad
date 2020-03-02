package mdm

// The following constants defines the time modifiers used for memory price
// calculations in the MDM.
const (
	// ProgramInitTime is the time it takes to execute a program. This is a
	// hardcoded value which is meant to be replaced in the future.
	// TODO: The time is hardcoded to 10 for now until we add time management in the
	// future.
	ProgramInitTime = 10

	// TimeAppend is the time for executing an 'Append' instruction.
	TimeAppend = 10e3

	// TimeCommit is the time used for executing managedFinalize.
	TimeCommit = 50e3

	// TimeHasSector is the time for executing a 'HasSector' instruction.
	TimeHasSector = 1

	// TimeReadSector is the time for executing a 'ReadSector' instruction.
	TimeReadSector = 1e3

	// TimeWriteSector is the time for executing a 'WriteSector' instruction.
	TimeWriteSector = 10e3
)
