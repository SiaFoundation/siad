# Siatest
The siatest package is designed to handle the integration tests for the Sia
codebase.

## Methodology
When writing siatests, the developer should structure the tests to mimic
production use. All interactions with Sia should be performed through the API
and client methods.

Test group creation is a time and resource intensive operation. Whenever
possible tests should be grouped together and share the same test group.

## Submodules
There is a submodule of the siatest package for each module of Sia (ie Host,
Renter, Wallet, etc). There submodules contain all the test code for the
corresponding Sia module.

In addition to these testing submodules there is a dependencies submodule. The
dependencies submodule is for dependency injection for tests that need to
exploit a specific test scenario that cannot be reliably tested through normal
means.

## Subsystems
The following subsystems help the SiaDir module execute its responsibilities:
 - [Local Dir Subsystem](#local-dir-subsystem)
 - [Local File Subsystem](#local-file-subsystem)
 - [Remote Dir Subsystem](#remote-dir-subsystem)
 - [Remote File Subsystem](#remote-file-subsystem)
 - [Test Group Subsystem](#test-group-subsystem)
 - [Test Node Subsystem](#test-node-subsystem)
 - [Test Helpers Subsystem](#test-helpers-subsystem)
 - [Modules Subsystem](#modules-subsystem)

 ### Local Dir Subsystem
 **Key Files**
- [localdir.go](./localdir.go)

The Local Dir subsystem is responsible for representing a local directory on
disk that could be uploaded to the Sia network.

**Exports**
`TestNode` Methods
 - `NewLocalDir` creates a new `LocalDir` in the `TestNodes` renter directory

`LocalDir` Methods
 - `CreateDir`creates a new `LocalDir`
 - `Files` returns a slice of files that are in the `LocalDir`
 - `Name` returns the name of the `LocalDir`
 - `NewFile` creates a new `LocalFile` in the `LocalDir`
 - `NewFileWithName` creates a new `LocalFile` with a specified name in the
   `LocalDir` 
 - `NewFileWithMode` creates a new `LocalFile` with a specified mode in the
   `LocalDir`
 - `Path` returns the path of the `LocalDir`
 - `PopulateDir` populates a `LocalDir` with files and sub-directories

**Outbound Complexities**
 - `NewLocalDir` calls out to the `testhelper` subsystem's `RenterDir` method

 ### Local File Subsystem
 **Key Files**
- [localfile.go](./localfile.go)

The Local File subsystem is responsible for representing a file on disk that
has been uploaded to the Sia network.

**Exports**
 - `Data` returns the byte slice data of a file
 - `Delete` deletes the local file from disk
 - `Equal` compares a byte slice to the data stored on disk
 - `FileName` returns the file name
 - `Move` moves the file to a new random location
 - `Path` returns the on disk path to the file
 - `Size` returns the size of the file
 - `Stat` is a wrapper for `os.Stat`

 ### Remote Dir Subsystem
 **Key Files**
- [remotedir.go](./remotedir.go)

The Remote Dir subsystem is responsible for representing a directory on the Sia
network.

**Exports**
 - `SiaPath` returns the siapath of the remote directory

 ### Remote File Subsystem
 **Key Files**
- [remotefile.go](./remotefile.go)

The Remote File subsystem is responsible for representing a SiaFile on the Sia
network.

**Exports**
 - `Checksum` returns the checksum of the remote file
 - `SiaPath` returns the siapath of the remote file

 ### Test Group Subsystem
 **Key Files**
- [testgroup.go](./testgroup.go)

The Test Group subsystem to responsible for creating a group of `TestNodes` to
form a mini Sia network.

There is a `testGroupBuffer` that is used to control the creation of the
`TestGroups` in order to reduce `too many open file` errors.

**Exports**
 - `NewGroup` creates a new `TestGroup` with fully synced, funded, and connected
   `TestNodes` based on node params
 - `NewGroupBuffer` creates a buffer channel and fills it
 - `NewGroupFromTemplate` creates a new `TestGroup` with fully synced, funded, and connected
   `TestNodes` based on group params
 - `AddNodeN` adds a number of nodes of a given template to a `TestGroup`
 - `AddNodes` adds a list of nodes to a `TestGroup`
 - `Close` closes the group
 - `Hosts` converts the map of host nodes to a slice and returns it
 - `Nodes` converts the map of nodes to a slice and returns it
 - `Miners` converts the map of miners nodes to a slice and returns it
 -` RemoveNode` removes a node from the group
 - `Renters` converts the map of renters nodes to a slice and returns it
 - `RestartNode` restarts a node from the group
 - `SetRenterAllowance` sets the allowance of a renter node in the group
 - `StartNode` starts a group's previously stopped node
 - `StartNodeCleanDeps` starts a group's previously stopped node with nil
   dependencies
 - `StopNode` stops a group's node
 - `Sync` makes sure that the group's nodes are synchronized

**Inbound Complexities**

**Outbound Complexities**

 ### Test Node Subsystem
 **Key Files**
- [testnode.go](./testnode.go)

The Test Node subsystem is responsible for representing a node on the Sia
network.

**Exports**
 - `NewNode` returns a new `TestNode` that has a funded wallet
 - `NewCleanNew` returns a new `TestNode` that has an unfunded wallet
 - `NewCleanNewAsync` returns a new `TestNode` that has an unfunded wallet and
   uses the `NewAsync` function of the `server` package
 - `IsAlertRegistered` checks if an alert is registered with a `TestNode`
 - `IsAlertUnregistered` checks if an alert has been unregistered with a
   `TestNode`
 - `PrintDebugInfo` logs out some helpful debug information about a `TestNode`
 - `RestartNode` stops and then restarts a `TestNode`
 - `SiaPath` returns a siapath of a `LocalDir` or `LocalFile` that could be used
   for uploading
 - `StartNode` starts a `TestNode`
 - `StartNodeCleanDeps` starts a `TestNode` but first sets any dependencies to
   nil
 - `StopNode` stops a `TestNode`

### Test Helpers Subsystem
 **Key Files**
- [consts.go](./consts.go)
- [dir.go](./dir.go)
- [testhelpers.go](./testhelpers.go)

The Test Helper subsystem contains helper methods and functions for executing
tests.

**Exports**
 - `ChunkSize` calculates the size of a chunk based on the pieces
 - `DownloadDir` returns the `LocalDir` of the `TestNode` that represents the
   downloads directory
 - `FilesDir` returns the `LocalDir` of the `TestNode` that represents the
   upload directory
 - `Fuzz` returns -1, 0, or 1
 - `RenterDir` returns the path of the `TestNode`'s renter directory
 - `RenterFilesDir` returns the path of the `TestNode`'s files directory
 - `Retry` calls a function a number of times with a sleep duration in between
   each call
 - `TestDir` creates a tmp directory on disk for the test

**Inbound Complexities**
 - The `LocaDir` subsystem's `NewLocalDir` method uses the `RenterDir` method 

### Modules Subsystem
 **Key Files**
- [consensus.go](./consensus.go)
- [contractor.go](./contractor.go)
- [gateway.go](./gateway.go)
- [miner.go](./miner.go)
- [renter.go](./renter.go)
- [wallet.go](./wallet.go)

The modules subsystem contains useful methods that are common across a module's
test suite. For example, uploading a file is necessary for almost every renter
test so there are a number of Upload methods in `renter.go` that can be used to
deduplicate code.

See each corresponding module file for more details and examples.
