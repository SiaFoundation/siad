# Contractor

The Contractor is responsible for forming and renewing file contracts with
hosts. Its goal is to manage the low-level details of the negotiation, revision,
and renewal protocols, such that the renter can operate at a higher level of
abstraction. Ideally, the renter should be mostly ignorant of the Sia protocol,
instead focusing on file management, redundancy, and upload/download algorithms.

The Contractor is also responsible for various forms of contract maintenance
including contract recovery, utility checking, archiving, and monitoring its own
contracts using the watchdog subsystem.

## Design Challenges

The primary challenge of the Contractor is that it must be smart enough for the
user to feel comfortable allowing it to spend their money. Because contract
renewal is a background task, it is difficult to report errors to the user and
defer to their decision. For example, what should the Contractor do in the
following scenarios?

- The contract set is up for renewal, but the average host price has increased,
  and now the allowance is not sufficient to cover all of the user's uploaded
  data.

- The user sets an allowance of 10 hosts. The Contractor forms 5 contracts, but
  the rest fail, and the remaining hosts in the HostDB are too expensive.

- After contract formation succeeds, 2 of 10 hosts become unresponsive. Later,
  another 4 become unresponsive.

Waiting for user input is dangerous because if the contract period elapses, data
is permanently lost. The Contractor should treat this as the worst-case
scenario, and take steps to prevent it, so long as the allowance is not
exceeded. However, since the Contractor has no concept of redundancy, it is not
well-positioned to determine which sectors to sacrifice and which to preserve.
The Contractor also lacks the ability to reupload data; it can download sectors,
but it does not know the decryption keys or erasure coding metadata required to
reconstruct the original data. It follows that these responsibilities must be
delegated to the renter.

## Alerts

`WalletLockedDuringMaintenance` is registered if the wallet is locked while the
contractor is attempting to create contracts.

`AllowanceLowFunds`  is registered if the contractor lacks the necessary fund to
renew or form contracts.



## Subsystems
The Contractor is split up into the following subsystems:
- [Allowance](#allowance-subsystem)
- [Contract Maintenance Subsystem](#contract-maintenance-subsystem)
- [Recovery Subsystem](#recovery-subsystem)
- [Session Subsystem](#session-subsystem)
- [Persistence Subsystem](#persistence-subsystem)
- [Watchdog Subsystem](#watchdog-subsystem)

## Allowance Subsystem
**Key Files**
- [Allowance](./allowance.go)

The allowance subsystem is used for setting and cancelling allowances. A change
in allowance does not necessarily cause changes in contract formation.

### Exports
- `SetAllowance` is exported by the `Contractor` and allows the caller to
  dictate the contract spendings of the `Renter`.

### Outbound Complexities
- `callInterruptContractMaintenance` is used when setting the allowance to
  stop and restart contract maintenance with the new allowance settings in
  place.


## Contract Maintenance Subsystem
**Key Files**
- [contractmaintenance.go](./contractmaintenance.go)

The contract maintenance subsystem is responsible for forming and renewing
contracts, and for other general maintenance tasks.

### Contract Formation

Contract formation does not begin until the user first calls `SetAllowance`. An
allowance dictates how much money the Contractor is allowed to spend on file
contracts during a given period. When the user calls `SetAllowance` the
[Allowance subsystem](#allowance-subsytem) updates the allowance and restarts the
Maintenance subsystem so that it will form new contracts with the changed
settings. New contracts will only be formed if needed and if the allowance is
sufficiently greater than the previous allowance, where "sufficiently greater"
currently means "enough money to pay for at least one additional sector on every
host." This allows the user to increase the amount of available storage
immediately, at the cost of some complexity.

The Contractor forms many contracts in parallel with different host, and tries
to keep all the contracts "consistent" -- that is, they should all have the same
storage capacity, and they should all end at the same height. Hosts are selected
from the HostDB. There is no support for manually specifying hosts, but the
Contractor will not form contracts with multiple hosts within the same subnet.

**Contract Renewal**

Contracts are automatically renewed by the Contractor at a safe threshold before
they are set to expire. This value is set in the allowance. When contracts are
renewed, they are renewed with the current allowance, which may differ from the
allowance that was used to form the initial contracts. In general, this means
that allowance modifications only take effect upon the next "contract cycle".

### Other Maintenance Checks

- Check the contract set for **duplicate contracts** and remove them.
- **Prune hosts**  that are no longer used for any contracts and hosts that violate rules about address ranges
- **Check the utility of opened contracts** by figuring out which contracts are still useful for uploading or for renewing
- **Archive contracts** which have expired by placing them in a historic contract set.

### Inbound Complexities
- `threadedContractMaintenance` is called by the
  [Allowance subsystem](#allowance-subsystem) when setting allowances, when `CancelContract`
  is called from the `Contractor`, and also with every `ConsensusChange` by the
  `Contractor` in the `ProcessConsensusChange` when the `Contractor` is synced.
- `callInterruptContractMaintenance` is used by [Allowance
  subsystem](#allowance-subsystem) when setting the allowance to
  stop and restart contract maintenance with the new allowance settings in
  place.

### Outbound Complexities
- `callInitRecoveryScan` in the [Recovery subsystem](#recovery-subsystem) is
  called to scan blocks for recoverable contracts.
- `save` is called to persist the contractor whenever the `Contractor's` state
  is updated during maintenance.
- Funds established by the [Allowance subsystem](#allowance-subsystem) are used
  and deducted appropriately during maintenance to form and renew contracts.


## Recovery Subsystem
**Key Files**
- [recovery.go](./recovery.go)

The Contractor is also responsible for scanning the Sia blockchain and
recovering all unexpired contracts which belong to the current wallet seed. The
relevant contracts are found by examining the contract identifier attached to
every file contract. Recovery scans are initiated whenever the wallet is
unlocked or when a new seed is imported.

A recoverable contract is recovered by reinitiating a session with the relevant
host and by getting the most recent revision from the host using this session.

### Inbound Complexities
- `callInitRecoveryScan` is called in the [Maintenance
  subsystem](#contract-maintenance-subsystem) to initiate recovery scans.
- `callRecoverContracts` is called in the [Maintenance
  subsystem](#contract-maintenance-subsystem) to recover contracts found in a
  recovery scan.


## Session Subsystem
**Key Files**
- [session.go](./session.go)
- [downloader.go](./downloader.go)
- [editor.go](./editor.go)

The Session subsystem provides an interface for communication with hosts. It
allows the contractor to modify its contracts through the renter-host protocol.
Sessions are used to initiate uploads, downloads, and file modifications. This
subsystem exports several methods used outside of the `Contractor` for this
purpose.

Pre-v1.4.0 contracts using an older version of the renter-host protocol use the
Editor and Downloader interfaces to interact with hosts.


### Pre-v1.4.0 Contract Modification

Modifications to file contracts are mediated through the Editor interface. An
Editor maintains a network connection to a host, over which is sends
modification requests, such as "delete sector 12." After each modification, the
Editor revises the underlying file contract and saves it to disk.

### Exports
The following `Session` methods are all exported by the Contractor:
- `Address`
- `Close`
- `ContractID`
- `Download`
- `DownloadIndex`
- `EndHeight`
- `Upload`
- `Replace`


## Persistence Subsystem
**Key Files**
- [persist.go](./persist.go)
- [persist\_journal.go](./persist_journal.go)


The Persistence subsystem is used to persist Contractor data across sessions.
Currently it uses the Sia persist package. Prior to v1.3.0 the persistence
subsystem used a journal system which is no longer used. If, on startup, this
old journal system is found, the Contractor will convert it into the new
Persistence subsytem.

### Inbound Complexities
- `save` is called from the [Allowance](#allowance-subsystem), and
  [Maintenance](#contract-maintenance-subsystem) subsystems to persist the
  `Contractor` when its internal state is updated.

### Outbound Complexities
The persist system is also responsible for persisting the [Watchdog
subsystem](#watchdog-subsystem).


## Watchdog Subsystem
**Key Files**
- [watchdog.go](./watchdog.go)

The Contractor is also responsible for monitoring all unexpired contracts to make
sure that they are finalized on-chain in the correct state. To do this, the
Contractor watchdog always makes sure that the final revision for a contract is
published onchain before the end of the contract window is reached. It also
monitors new file contracts to make sure they are posted onchain, and notifies
the contractor if inputs used to create a file contract are double-spent.
Finally, the watchdog also monitors the Sia blockchain for storage proofs and
takes actions against hosts that fail to submit proofs in time.

This functionality is currently unimplemented.
