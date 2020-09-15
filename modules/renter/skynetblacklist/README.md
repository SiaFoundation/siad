# Skynet Blacklist

The Skynet Blacklist module manages a list of blacklisted Skylinks by tracking
their merkleroots.

## Subsystems
The following subsystems help the Skynet Blacklist module execute its
responsibilities:
 - [Skynet Blacklist Subsystem](#skynet-blacklist-subsystem)

### Skynet Blacklist Subsystem
**Key Files**
 - [skynetblacklist.go](./skynetblacklist.go)

The Skynet Blacklist subsystem contains the structure of the Skynet Blacklist
and is used to create a new Skynet Blacklist and return information about the
Blacklist. Uses Persist package's Append-Only File subsystem to ensure ACID disk
updates.

**Exports**
 - `Blacklist` returns the list of blacklisted merkle roots
 - `IsBlacklisted` returns whether or not a skylink merkleroot is blacklisted
 - `New` creates and returns a new Skynet Blacklist
 - `UpdateBlacklist` updates the blacklist
 - `UpdateBlacklistHash` updates the blacklist with the hash of the Skylink's
     merkleroot
