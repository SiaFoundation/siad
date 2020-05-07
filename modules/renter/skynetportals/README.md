# Skynet Portals

The Skynet Portals module manages a list of known Skynet portals and whether
they are public or not.

## Subsystems
The following subsystems help the Skynet Portals module execute its
responsibilities:
 - [Skynet Portals Subsystem](#skynet-portals-subsystem)

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
 - `UpdateSkynetPortals` updates the Portals List

**Outbound Complexities**
 - `New` calls the Persistence System's `NewAppendOnlyPersist` method
 - `Update` calls the Persistence System's `UpdateAndAppend` method
