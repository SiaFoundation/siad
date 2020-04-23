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

The FeeManager subsystem handles the creation and shutdown of the FeeManager.
Additionally this subsystem handles  providing information about the
FeeManager's state, such as the current fees being managed.

**Exports**
  - `New` creates a new FeeManager with default dependencies
  - `NewCustomFeeManager` creates a new FeeManager with custom dependencies
  - `AddFee` adds a fee to the FeeManager
  - `CancelFee` cancels a fee 
  - `Close` closes the FeeManager
  - `PaidFees` returns a list of fees that have been paid out by the FeeManager
  - `PendingFees` returns a list of pending fees being managed by the FeeManager
  - `Settings` returns the settings of the FeeManager 

**Outbound Complexities**
  - The persist subsystem's `callInitPersist` method is called from
    `NewCustomeFeeManager` to initialize the persistence files and/or load the
    persistence from disk
  - The persist subsystem's `callPersistFeeCancelation` method is called from
    `CancelFee` to remove the fee from the FeeManager and persist the change on
    disk
  - The persist subsystem's `callPersistNewFee` method is called from `SetFee`
    to add the fee to the FeeManager and persist the change on disk

### Persistence Subsystem
**Key Files**
- [persist.go](./persist.go)
- [persistentries.go](./persistentries.go)

The persistence subsystem handles actions that update the state of the
FeeManager and the ACID disk interactions for the `feemanager` module. To ensure
disk interactions are ACID, the persistence subsystem uses an append only model
with the size of data stored in the persist file header.

The persistence subsystem has two main components, the `persistSubsystem` and
the `syncCoordinator`. The `persistSubsystem` contains the state of the
FeeManager persistence and handles the inbound complexity. The `syncCoordinator`
is solely responsible for handling the concurrent requests to sync the persist
file and update the persist file header.

**Inbound Complexities**
  - `callInitPersist` initializes the persistence by creating or loading a
    persist file and initializing the logger
  - The feemanager subsystem's `CancelFee` method calls
    `callPersistFeeCancelation` to remove a fee from the FeeManager and persists
    the change on disk
  - The feemanager subsystem's `AddFee` method calls `callPersistNewFee` to add
    a fee to the FeeManager and persists the change on disk
  - The process fees subsystem's `threadedProcessFess` method calls
    `callPersistNewFee` to persist a change to a Fee's `PayoutHeight` on disk

### Process Fees Subsystem
**Key Files**
- [processfees.go](./processfees.go)

The process fees subsystem handles processing fees for each payout period and
ensuring that the `PayoutHeight`'s are updated.

**Outbound Complexities**
 - The persist subsystem's `callPersistNewFee` method is called from
   `threadedProcessFees`
