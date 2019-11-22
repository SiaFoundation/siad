# Filesystem
The Filesystem is responsible for ensuring that all of its supported file
formats can be accessed in a threadsafe manner. It doesn't handle any
persistence directly but instead relies on the underlying format's package to
handle that itself. This also means that methods like `Delete` and `Rename`
should not be called on the underlying types directly but be called through
the Filesystem's corresponding wrappers instead.

To refer to a file or folder within the Filesystem, so-called SiaPaths are
used. They are unix-like paths relative to the specified root of the
Filesystem except for the fact that they don't start with a leading slash.
Ideally all parts of the siad codebase only have to interact with SiaPaths
instead of system paths and the Filesystem would handle all of the
translations between SiaPaths and regular system paths. The Filesystem also
enforces that files and folders can't share the same name.

The Filesystem is an in-memory tree-like data structure of nodes. The nodes
can either be files or directories. Directory nodes potentially point to
other directory nodes or file nodes while file nodes can't have any children.
When opening a file or folder, the Filesystem is traversed starting from the
root until the required file or folder is found, opening all the nodes on
demand and adding them to the tree. The tree will be pruned again as nodes
are no longer needed. To do so a special locking convention was introduced
which will be explained in its own paragraph.

## Locking Conventions
Due to the nature of the Filesystem custom locking conventions were required
to optimize the performance of the data structure.

- A locked child node can't grab a parent's lock
- A locked parent can grab its children's locks
- A parent needs to be locked before adding/removing children
- A parent doesn't need to be locked before modifying any children

Locking like this avoids a lot of lock contention and enables us to easily
and efficiently delete and rename folders.

# Subsystems
The Filesystem has the following subsystems.
- [Filesystem](#filesystem)
- [DirNode](#file-node)
- [FileNode](#dir-node)

### Filesystem
**Key Files**
- [filesystem.go](./filesystem.go)

The Filesystem subsystem contains Filesystem specific errors, the definition
for the common `node` type as well as all the exported methods which are
called by other modules.

It is a special `DirNode` which acts as the root of the Filesystem, is
created upon startup and is always kept in memory instead of being pruned
like the other nodes. The job of the FileSystem is to translate SiaPaths into
regular paths and pass calls to its exported methods on to the correct child
if possible. It also implements some high level methods which might require
interacting with multiple nodes like `RenameDir` for example.

### DirNode
**Key Files**
- [dirnode.go](./dirnode.go)

The DirNode extends the common `node` by fields relevant to a directory.
Namely children and an embedded `SiaDir`. The special thing about the
embedded `SiaDir` is that it is loaded lazily. That means it will only be
loaded from disk once a method is called that requires it. The `SiaDir` will
also be removed from memory again once the DirNode is no longer being
accessed. That way we avoid caching the `SiaDir` for too long.

### FileNode
**Key Files**
- [filenode.go](./filenode.go)

The FileNode is similar to the DirNode but it only extends the `node` by a
single embedded `Siafile` field. Apart from that it contains wrappers for the
`SiaFile` methods which correctly modify the parent directory when the
underlying file is moved or deleted.