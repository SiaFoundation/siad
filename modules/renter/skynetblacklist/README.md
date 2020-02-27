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
length of fsync'd bytes encoded in the metadata.

**Inbound Complexities**
 - `callInitPersist` initializes the persistence file 
    - The Skynet Blacklist Subsystem's `New` method uses `callInitPersist`
 - `callUpdateAndAppend` updates the skynet blacklist and appends the
   information to the persistence file
    - The Skynet Blacklist Subsytem's `Update` method uses `callUpdateAndAppend`

### Skynet Blacklist Subsystem
**Key Files**
 - [skynetblacklist.go](./skynetblacklist.go)

The Skynet Blacklist subsystem contains the structure of the Skynet Blacklist
and is used to create a new Skynet Blacklist and return information about the
Blacklist.

**Exports**
 - `IsBlacklisted` returns whether or not a skylink merkleroot is blacklisted
 - `New` creates and returns a new Skynet Blacklist
 - `Update` updates the blacklist

**Outbound Complexities**
 - `New` calls the Persistence Subsystem's `callInitPersist` method
 - `Update` calls the Persistence Subsystem's `callUpdateAndAppend` method