# Skynet Blocklist

The Skynet Blocklist module manages a list of blocked Skylinks by tracking
hashes of their merkleroots.

## Subsystems
The following subsystems help the Skynet Blocklist module execute its
responsibilities:
 - [Skynet Blocklist Subsystem](#skynet-blocklist-subsystem)

### Skynet Blocklist Subsystem
**Key Files**
 - [skynetblocklist.go](./skynetblocklist.go)

The Skynet Blocklist subsystem contains the structure of the Skynet Blocklist
and is used to create a new Skynet Blocklist and return information about the
Blocklist. Uses Persist package's Append-Only File subsystem to ensure ACID disk
updates.

**Exports**
 - `Blocklist` returns the list of hashes of the blocked merkle roots
 - `IsBlocked` returns whether or not a skylink merkleroot is blocked
 - `New` creates and returns a new Skynet Blocklist
 - `UpdateBlocklist` updates the blocklist
