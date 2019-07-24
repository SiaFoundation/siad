# SiaFile
The SiaFile contains all the information about an uploaded file that is
required to download it plus additional metadata about the file. The SiaFile
is split up into 4kib pages. The header of the SiaFile is located within the
first page of the SiaFile. More pages will be allocated should the header
outgrow the page. The metadata and host public key table are kept in memory
for as long as the siafile is open, and the chunks are loaded and unloaded as
they are accessed.

Since SiaFile's are rapidly accessed during downloads and repairs, the
SiaFile was built with the requirement that all reads and writes must be able
to happen in contant time, knowing only the offset of thte logical data
within the SiaFile. To achieve that, all the data is page-aligned which also
improves disk performance. Overall the SiaFile package is designed to
minimize disk I/O operations and to keep the memory footprint as small as
possible without sacrificing performance.

Structure of the SiaFile:
- Header
    - [Metadata](#metadata)
    - [Host Public Key Table](#host-public-key-table)
- [Chunks](#chunks)

### Metadata
The metadata contains all the information about a SiaFile that is not
specific to a single chunk of the file. This includes keys, timestamps,
erasure coding etc. The definition of the `Metadata` type which contains all
the persisted fields is located within [metadata.go](./metadata.go). The
metadata is the only part of the SiaFile that is JSON encoded for easier
compatibility and readability. The encoded metadata is written to the
beginning of the header.

### Host Public Key Table
The host public key table uses the [Sia Binary
Encoding](./../../../doc/Encoding.md) and is written to the end of the
header. As the table grows, it will grow towards the front of the header
while the metadata grows towards the end. Should metadata and host public key
table ever overlap, a new page will be allocated for the header. The host
public key table is a table of all the hosts that contain pieces of the
corresponding SiaFile.

### Chunks
The chunks are written to disk starting at the first 4kib page after the
header. For each chunk, the SiaFile reserves a full page on disk. That way
the SiaFile always knows at which offset of the file to look for a chunk and
can therefore read and write chunks in constant time. A chunk only consists
of its pieces and each piece contains its merkle root and an offset which can
be resolved to a host's public key using the host public key table. The
`chunk` and `piece` types can be found in [siafile.go](./siafile.go).

## Subsystems
The SiaFile is split up into the following subsystems.
- [Erasure Coding Subsystem](#erasure-coding-subsystem)
- [Persistence Subsystem](#persistence-subsystem)
- [SiaFileSet Subsystem](#siafileset-subsystem)
- [Snapshot Subsystem](#snapshot-subsystem)

### Erasure Coding Subsystem
**Key Files**
- [rscode.go](./rscode.go)
- [rssubcode.go](./rssubcode.go)

The erasure coding subsystem contains the code required to split up chunks
into multiple pieces for uploading them to hosts.

### Persistence Subsystem
**Key Files**
- [encoding.go](./encoding.go)
- [persist.go](./persist.go)

The persistence subsystem handles all of the disk I/O and marshaling of
datatypes. It provides helper functions to read the SiaFile from disk and
atomically write to disk using the
[writeaheadlog](https://gitlab.com/NebulousLabs/writeaheadlog) package.

### SiaFileSet Subsystem
**Key Files**
- [siafileset.go](./siafileset.go)

While a SiaFile object is threadsafe by itself, it's not safe to load a
SiaFile into memory multiple times as this will cause corruptions on disk.
Only one instance of a specific SiaFile can exist in memory at once. To
ensure that, the siafileset was created as a pool for SiaFiles which is used
by other packages to get access to SiaFileEntries which are wrappers for
SiaFiles containing some extra information about how many threads are using
it at a certain time. If a SiaFile was already loaded the siafileset will
hand out the existing object, otherwise it will try to load it from disk.

### Snapshot Subsystem
**Key Files**
- [snapshot.go](./snapshot.go)

The snapshot subsystem allows a user to create a readonly snapshot of a
SiaFile. A snapshot contains most of the information a SiaFile does but can't
be used to modify the underlying SiaFile directly. It is used to reduce
locking contention within parts of the codebase where readonly access is good
enough like the download code for example.
