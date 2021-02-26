# Accounting
The accounting module provides accounting information for a Sia node. 

## Subsystems
The Accounting module has the following subsystems
 - [Accounting Subsystem](#accounting-subsystem)
 - [Persistence Subsystem](#persistence-subsystem)

### Accounting Subsystem
**Key Files**
 - [accounting.go](./accounting.go)

The accounting subsystem is responsible for general actions related to the
accounting module, such as initialization and returning information about the
module.

**Exports**
 - `Accounting`
 - `Close`
 - `NewCustomAccounting`

**Inbound Complexities**
 - `callUpdateAccounting` updates the accounting information and the persistence
     object in memory.
      - The persistence subsystem's `managedUpdateAndPersistAccounting` method
        calls `callUpdateAccounting` to update the persistence before saving to
        disk.

**Outbound Complexities**
 - `NewCustomAccounting` will use `callThreadedPersistAccounting` to launch the
     background loop for persisting the accounting information.

### Persistence Subsystem
**Key Files**
 - [persist.go](./persist.go)

The persistence subsystem is responsible for ensuring safe and performant ACID
operations by using the `persist` package's `AppendOnlyPersist` object. The
latest persistence is stored in the `Accounting` struct and is loaded from disk
on startup.

**Inbound Complexities**
 - `callThreadedPersistAccounting` is a background loop that updates the
     persistence and saves it to disk.
      - The accounting subsystem's `NewCustomAccounting` function calls
        `callThreadedPersistAccounting` once the Accounting module has been
        initialized before returning.

**Outbound Complexities**
 - `managedUpdateAndPersistAccounting` will use `callUpdateAccounting` to update
     the Accounting persistence before saving the persistence to disk.
