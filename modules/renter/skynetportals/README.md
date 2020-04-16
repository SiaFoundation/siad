# Skynet Portals

The Skynet Portals module manages a list of known Skynet portals and whether
they are public or not.

## Subsystems
The following subsystems help the Skynet Portals module execute its
responsibilities:
 - [Persistence Subsystem](#persistence-subsystem)
 - [Skynet Portals Subsystem](#skynet-portals-subsystem)

 ### Persistence Subsystem
 **Key Files**
- [persist.go](./persist.go)

The Persistence subsystem is responsible for the disk interaction and ensuring
safe and performant ACID operations. An append only structure is used with a
length of fsync'd bytes encoded in the metadata.

**Inbound Complexities**
 - `callInitPersist` initializes the persistence file
    - The Skynet Portals Subsystem's `New` method uses `callInitPersist`
 - `callUpdateAndAppend` updates the skynet portal list and appends the
   information to the persistence file
    - The Skynet Portals Subsytem's `Update` method uses `callUpdateAndAppend`

### Skynet Portals Subsystem
**Key Files**
 - [skynetportals.go](./skynetportals.go)

The Skynet Portals subsystem contains the structure of the Skynet Portals List
and is used to create a new Skynet Portals List and return information about the
Portals.

**Exports**
 - `Portals` returns the list of known Skynet portals and whether they are
   public
 - `New` creates and returns a new Skynet Portals List
 - `Update` updates the Portals List

**Outbound Complexities**
 - `New` calls the Persistence Subsystem's `callInitPersist` method
 - `Update` calls the Persistence Subsystem's `callUpdateAndAppend` method
