# FeeManager
The FeeManager is a way for developers building on top of Sia to charge a fee
for the use of their product. The FeeManager is structured in a way that one
siad instance can support fees from multiple applications running on top of it.

An application can request that the user be charged a fixed amount once.
Applications can use this as a setup fee or can charge the user for various
actions as the application is used.

Fees can be marked as recurring to indicate to the user that the fee will be
charged every month. The application that is extracting the fee is still
expected to register the fee each month, siad will not charge users for
applications that the user is no longer using.

## Subsystems
The following subsystems help the FeeManager module execute its
responsibilities:
 - [FeeManager Subsystem](#feemanager-subsystem)
 - [Persistence Subsystem](#persistence-subsystem)
 - [Process Fee Subsystem](#process-fee-subsystem)

### FeeManager Subsystem
**Key Files**
- [feemanager.go](./feemanager.go)
- [feemanager_test.go](./feemanager_test.go)

The FeeManager subsystem handles the creation and shutdown of the FeeManager.
Additionally this subsystem handles the Consensus changes and providing
information about the FeeManager's state, such as the current fees being
managed.

**Exports**
  - `New` creates a new FeeManager with default dependencies
  - `NewCustomFeeManager` creates a new FeeManager with custom dependencies
  - `CancelFee` cancels a fee 
  - `Close` closes the FeeManager
  - `PaidFees` returns a list of fees that have been paid out by the FeeManager
  - `PendingFees` returns a list of pending fees being managed by the FeeManager
  - `SetFee` sets a fee for the FeeManager to manage
  - `Settings` returns the settings of the FeeManager 

**Outbound Complexities**
  - The persist subsystem's `callCancelFee` method is called from `CancelFee` to
    remove the fee from the FeeManager and persist the change on disk
  - The persist subsystem's `callInitPersist` method is called from
    `NewCustomeFeeManager` to initialize the persistence files and/or load the
    persistence from disk
  - The persist subsystem's `callLoadAllFees` method is called from `PaidFees`
    to load all the persisted fees from disk
  - The persist subsystem's `callSetFee` method is called from `SetFee` to add
    the fee to the FeeManager and persist the change on disk

### Persistence Subsystem
**Key Files**
- [persist.go](./persist.go)
- [persist_test.go](./persist_test.go)
- [persistwal.go](./persistwal.go)
- [persistwal_test.go](./persistwal_test.go)

The persistence subsystem handles actions that update the state of the
FeeManager and the ACID disk interactions for the `feemanager` module. To ensure
disk interactions are ACID, the persistence subsystem uses the `writeaheadlog`
to persist the FeeManager's information on disk. The persistence subsystem
manages two persist files, one file for the FeeManager pending fees and
settings, and another file that contains a record of all the historical fees.

**Inbound Complexities**
  - The feemanager subsystem's `CancelFee` method calls `callCancelFee` to
    remove a fee from the FeeManager and persists the change on disk
  - `callInitPersist` initializes the persistence by creating or loading a
    persist file and initializing the logger
  - The feemanager subsystem's `PaidFees` method calls `callLoadAllFees` to load
    all the persisted fees from disk
  - The feemanager subsystem's `SetFee` method calls `callSetFee` to add a fee
    to the FeeManager and persists the change on disk
  - The process fees subsystem's `threadedProcessFess` method calls `save` to
    persist changes to the FeeManager to disk after processing fees

### Process Fees Subsystem
**Key Files**
- [processfees.go](./processfees.go)

The process fees subsystem handles the consensus changes and processes fees for
each payout period.

**Exports**
  - `ProcessConsensusChange` handles consensus changes

**Outbound Complexities**
 - The persist subsystem's `save` method is called from `threadedProcessFees`