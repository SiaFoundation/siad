# Proto
Coming Soon...

## Subsystems

### Contracts
Coming Soon...

#### SafeContract
Coming Soon...

#### ContractSet
Coming Soon...

### Downloader
Coming Soon...

### Editor
Coming Soon...

### FileSection
Coming Soon...

### FormContract
Coming Soon...

### Merkle Roots
Coming Soon...

### Negotiate
Coming Soon...

### Reference Counter Subsystem
**Key Files**
 - [refcounter.go](./refcounter.go)

The reference counter is a ledger that accompanies the contract and keeps track 
of the number of references to each sector. The number of references starts at
one and increases as the user creates new backups of this contract. It decreases
as the user deletes backups or deletes the siafile itself. Once the counter
reaches zero, there is no way for the user to use the data stored in the sector.
At this moment the sector can be reused by storing new data in it or it can be
dropped when renewing the contract, so the user doesn't pay for it anymore.

#### The Reference Counter

The reference counter struct exposes a simple API aimed at manipulating the
counters that it holds. One of the few complexities that might not be 
immediately obvious is that before performing any mutating operation, the caller
need to start an update session by invoking `callStartUpdate`. This update
session will use the WAL in order to ensure that all operations are ACID. After
finishing a given set of mutations, the caller needs to invoke 
`callCreateAndApplyTransaction` in order for the updates to be written to disk.
Before being written to disk, the updates are only reflected in a set of 
in-memory data structures. This ensures that any operation with the reference 
counter between the creation and application of the update will work with 
the updated data. In order to finish an update the caller needs to invoke 
`callUpdateApplied` in order to close the update session.

Each reference counter is created and deleted together with a contract, and this
contract is responsible for its proper maintenance. The counts should be updated
on backup creation/deletion and on file deletion.

##### Inbound Complexities
 - `callCount` can be used to fetch the value of a given counter
 - `callStartUpdate` can be used to start a new series of ACID updates
 - `callAppend` and `callDropSectors` can be used to add and remove sectors as 
 they are added or removed to/from the contract
    - `contract.makeUpdateRefCounterAppend` uses `callAppend` to reflect the
    upload of new sectors to the contract
 - `callSwap` can be used to swap the positions of two counters in the file
 - `callIncrement`, `callDecrement`, and `callSetCount` can be used to adjust
 the value of a given counter
 - `callCreateAndApplyTransaction` is used to apply a set of updates to the file
 on disk
 - `callUpdateApplied` finished an update session
     - `contract.managedCommitAppend` and `contract.managedCommitTxns` use
     `callCreateAndApplyTransaction` (via the `contract.applyRefCounterUpdate`
     method) and `callUpdateApplied` in order to persist the changes they made 
     to disk
 - `callDeleteRefCounter` removes the entire reference counter file from disk
 
##### Outbound Complexities
 - `callCreateAndApplyTransaction` will use `writeaheadlog.WAL.NewTransaction` 
 in order to start a new WAL transaction. It will then use this transaction to 
 apply the given updates and then it will close it.

### Renew
Coming Soon...

### Seeds
Coming Soon...

### Session
Coming Soon...
