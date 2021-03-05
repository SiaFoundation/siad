# SiaDir
The SiaDir module is responsible for creating and maintaining the directory
metadata information stored in the `.siadir` files on disk. This includes all
disk interaction and metadata definition. These siadirs represent directories on
the Sia network.

## Structure of the SiaDir
The SiaDir is a dir on the Sia network and the siadir metadata is a JSON
formatted metadata file that contains aggregate and non-aggregate fields. The
aggregate fields are the totals of the siadir and any sub siadirs, or are
calculated based on all the values in the subtree. The non-aggregate fields are
information specific to the siadir that is not an aggregate of the entire sub
directory tree

## Subsystems
The following subsystems help the SiaDir module execute its responsibilities:
 - [Persistence Subsystem](#persistence-subsystem)
 - [File Format Subsystem](#file-format-subsystem)
 - [SiaDirSet Subsystem](#siadirset-subsystem)
 - [DirReader Subsystem](#dirreader-subsystem)

 ### Persistence Subsystem
 **Key Files**
- [persist.go](./persist.go)

The Persistence subsystem is responsible for the disk interaction with the
`.siadir` files. All the information stored in the `.siadir` file is metadata
that can be recalculated on the fly. Because of this, the persistence is not
ACID. The persistence relies on a checksum at the beginning of the file to know
whether or not the file is corrupt.

**Exports**
 - `New`
 - `LoadSiaDir`
 - `UpdateMetadata`

**Inbound Complexities**
 - `callDelete` deletes a SiaDir from disk
    - `SiaDirSet.Delete` uses `callDelete`
 - `LoadSiaDir` loads a SiaDir from disk
    - `SiaDirSet.open` uses `LoadSiaDir`

### File Format Subsystem
 **Key Files**
- [siadir.go](./siadir.go)

The file format subsystem contains the type definitions for the SiaDir
format and methods that return information about the SiaDir.

**Exports**
 - `Deleted`
 - `Metatdata`
 - `SiaPath`

### SiaDirSet Subsystem
 **Key Files**
- [siadirset.go](./siadirset.go)

A SiaDir object is threadsafe by itself, and to ensure that when a SiaDir is
accessed by multiple threads that it is still threadsafe, SiaDirs should always
be accessed through the SiaDirSet. The SiaDirSet was created as a pool of
SiaDirs which is used by other packages to get access to SiaDirEntries which are
wrappers for SiaDirs containing some extra information about how many threads
are using it at a certain time. If a SiaDir was already loaded the SiaDirSet
will hand out the existing object, otherwise it will try to load it from disk.

**Exports**
 - `HealthPercentage`
 - `NewSiaDirSet`
 - `Close`
 - `Delete`
 - `DirInfo`
 - `DirList`
 - `Exists`
 - `InitRootDir`
 - `NewSiaDir`
 - `Open`
 - `Rename`

**Outbound Complexities**
 - `Delete` will use `callDelete` to delete the SiaDir once it has been acquired
   in the set
 - `open` calls `LoadSiaDir` to load the SiaDir from disk

### DirReader Subsystem
**Key Files**
 - [dirreader.go](./dirreader.go)

The DirReader Subsystem creates the DirReader which is used as a helper to read
raw .siadir from disk

**Exports**
 - `Close`
 - `Read`
 - `Stat`
 - `DirReader`
