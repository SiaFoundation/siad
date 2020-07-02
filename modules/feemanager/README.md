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
 - [Watchdog Subsystem](#watchdog-subsystem)

### FeeManager Subsystem
**Key Files**
- [feemanager.go](./feemanager.go)

The FeeManager subsystem handles the creation and shutdown of the FeeManager.
Additionally this subsystem handles  providing information about the
FeeManager's state, such as the current fees being managed.

The FeeManager subsystem has a main `FeeManager` type as well as a
`feeManagerCommon` type. The `feeManagerCommon` type is a share struct between
the other subsystems and contains references to the other subsystems as well as
the common dependencies.

**Exports**
  - `New` creates a new FeeManager with default dependencies
  - `NewCustomFeeManager` creates a new FeeManager with custom dependencies
  - `AddFee` adds a fee to the FeeManager
  - `CancelFee` cancels a fee 
  - `Close` closes the FeeManager
  - `PaidFees` returns a list of fees that have been paid out by the FeeManager
  - `PayoutHeight` returns the `PayoutHeight` of the FeeManager 
  - `PendingFees` returns a list of pending fees being managed by the FeeManager

**Outbound Complexities**
  - The persist subsystem's `callInitPersist` method is called from
    `NewCustomFeeManager` to initialize the persistence files and/or load the
    persistence from disk
  - The persist subsystem's `callPersistFeeCancelation` method is called from
    `CancelFee` to remove the fee from the FeeManager and persist the change on
    disk
  - The persist subsystem's `callPersistNewFee` method is called from `AddFee`
    to add the fee to the FeeManager and persist the change on disk
  - The watchdog subsystem's `callFeeTracked` method is called from `CancelFee`
    as a check to see if a fee can be canceled

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
  - The feemanager subsystem's `NewCustomFeeManager` calls `callInitPersist` to
    initialize the persistence by creating or loading a persist file and
    initializing the logger
  - The feemanager subsystem's `CancelFee` method calls
    `callPersistFeeCancelation` to remove a fee from the FeeManager and persists
    the change on disk
  - The feemanager subsystem's `AddFee` method calls `callPersistNewFee` to add
    a fee to the FeeManager and persists the change on disk
  - The process fees subsystem's `threadedProcessFess` method calls
    `callPersistFeeUpdate` to persist a change to a Fee's `PayoutHeight` on disk
  - The process fees subsystem's `createdAndPersistTransaction` method calls
    `callPersistTxnCreated` to persist the link between feeUIDs and the
    transaction ID
  - The watchdog subsystem's `managedConfirmTransaction` method calls
    `callPersistTxnConfirmed` to persist that the transaction was confirmed
  - The watchdog subsystem's `managedDropTransaction` method calls
    `callPersistTxnDropped` to persist that the transaction was dropped

**Outbound Complexities**
 - The watchdog subsystems's `callApplyTransaction` is called from
   `applyEntryTransaction` to apply a persisted transaction to the watchdog
 - The watchdog subsystems's `callApplyTxnConfirmed` is called from
   `applyEntryTxnConfirmed` to apply a persisted transaction confirmed entry to
   the watchdog 
 - The watchdog subsystems's `callApplyTxnCreated` is called from
   `applyEntryTxnCreated` to apply a persisted transaction created entry to the
   watchdog
 - The watchdog subsystems's `callApplyTxnDropped` is called from
   `applyEntryTxnDropped` to apply a persisted transaction dropped entry to the
   watchdog

### Process Fees Subsystem
**Key Files**
- [processfees.go](./processfees.go)

The process fees subsystem handles processing fees for each payout period and
ensuring that the `PayoutHeight`'s are updated. Fees are processed by creating a
transaction and then submitting the transaction to the watchdog.

**Outbound Complexities**
 - The persist subsystem's `callPersistFeeUpdate` method is called from
   `threadedProcessFees` to update the PayoutHeight for a fee
 - The persist subsystem's `callPersistTxnCreated`method is call from
   `createdAndPersistTransaction` to link FeeUIDs and a TxnID
 - The watchdog subsystem's `callMonitorTransaction` method is called by
    `createdAndPersistTransaction` to add a transaction for the watchdog to
   monitor

### Watchdog Subsystem
**Key Files**
- [watchdog.go](./watchdog.go)
- [watchdogpersist.go](./watchdogpersist.go)

The watchdog subsystem handles tracking the transactions created for Fees and
marking them as paid once the transactions are confirmed. The watchdog will
continue to rebroadcast the transaction until the transaction is confirmed. If
the transaction is not confirmed within an acceptable time period, the watchdog
will assume the transaction will not be successful and drop it and mark the fees
as not having a transaction created. Once a transaction is confirmed the
watchdog updates the fees' `PaymentComplete` flag.

**Inbound Complexities**
 - The process fees subsystem's `createdAndPersistTransaction` calls
   `callMonitorTransaction` to add a transaction for the watchdog to monitor
 - The persistence subsystem's `applyEntryTransaction` calls
   `callApplyTransaction` to add a persisted transaction to the watchdog
 - The persistence subsystem's `applyEntryTxnConfirmed` calls
   `callApplyTxnConfirmed` to mark a transaction that the watchdog is monitoring
   as confirmed
 - The persistence subsystem's `applyEntryTxnCreated` calls
   `callApplyTxnCreated` to add fees to the watchdog that are associated with
   the transaction that was created 
 - The persistence subsystem's `applyEntryTxnDropped` calls
   `callApplyTxnDropped` to drop a transaction from the watchdog 
 - The feemanager subsystem's `CancelFee` method calls `callFeeTracked` as a
   check to see if a fee can be canceled

**Outbound Complexities**
  - The persist subsystem's `callPersistTxnConfirmed` method is called by
    `managedConfirmTransaction` to persist that the transaction was confirmed
  - The persist subsystem's `callPersistTxnDropped` method is called by
    `managedDropTransaction` to persist that the transaction was dropped
