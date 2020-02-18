# Skynet Blacklist

The Skynet Blacklist modules manages a list of blacklisted Skylinks by tracking
their merkleroots.

## Subsystems
The following subsystems help the Skynet Blacklist module execute its responsibilities:
 - [Persistence Subsystem](#persistence-subsystem)
 - [Skynet Blacklist Subsystem](#skynet-blacklist-subsystem)

 ### Persistence Subsystem
 **Key Files**
- [persist.go](./persist.go)

The Persistence subsystem is responsible for the disk interaction and ensuring
safe and performant ACID operations. An append only structure is used with a
length of good bytes encoded in the metadata. 

**Exports**
 - `Update`

**Inbound Complexities**
 - `callInitPersist` initializes the persistence file 
    - `skynetblacklist.New` uses `callInitPersist`

### Skynet Blacklist Subsystem
**Key Files**
 - [skynetblacklist.go](./skynetblacklist.go)

The Skynet Blacklist subsystem contains the structure of the Skynet Blacklist
and is used to create a new Skynet Blacklist and return information about the
Blacklist.

**Exports**
 - `Blacklisted` returns whether or not a skylink merkleroot is blacklisted
 - `New` creates and returns a new Skynet Blacklist

**Outbound Complexities**
 - `New` calls `callInitPersist`
 