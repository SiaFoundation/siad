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

## Benchmarks
- Writing to a random chunk of a SiaFile
    - i9-9900K with Intel SSDPEKNW010T8 -> 200 writes/second
- Writing to a random chunk of a SiaFile (multithreaded)
    - i9-9900K with Intel SSDPEKNW010T8 -> 200 writes/second
- Reading a random chunk of a SiaFile
    - i9-9900K with Intel SSDPEKNW010T8 -> 50,000 reads/second
- Loading a a SiaFile's header into memory
    - i9-9900K with Intel SSDPEKNW010T8 -> 20,000 reads/second

## Partial Uploads
This section contains information about how partial uploads are handled
within the siafile package. "Partial Upload" refers to being able to upload a
so-called partial chunk without padding it to the size of a full chunk and
therefore not wasting money when uploading many small files or files with
trailing partial chunks. This is achieved by combining multiple partial
chunks of different `SiaFiles` into a combined chunk.

A `SiaFile` can contain at most a single partial chnk. This partial chunk can
either be contained within a single combined chunk or spread across two
combined chunks. If a `SiaFile` has a partial chunk, the `HasPartialChunk`
field in the metadata will be set accordingly. Once it is clear which
combined chunks the partial chunk is part of, `SetPartialChunks` will be
called on the `SiaFile` to set the `PartialChunks` field in the `Metadata`.
This field will contain one or two entries, depending on whether the partial
chunk is split across two combined chunks or just one. These entries contain
the required information to retrieve a partial chunk from a combined chunk
and the status of the combined chunk to be able to determine whether to
expect the combined chunk to be uploaded or not. Since multiple `SiaFiles`
can reference the same combined chunks, a special type of `SiaFile` was
introduced, called the "Partials Siafile" which also uses the `SiaFile` type
but was a different file extension since it is never used directly.

### Partials Siafiles
Partials siafiles are a special type of `SiaFile`. A partials siafile doesn't
contain metadata about an individual file but rather contains metadata about
so-called combined chunks which are referenced by the regular `SiaFile` type.
A combined chunk is a chunk which contains multiple partial chunks which were
combined into a combined chunk. As such, a `SiaFile` with a partial chunk
contains a reference to a partials siafile and forwards calls to its exported
methods to the partials siafile as necessary.

A partials siafile can't itself have partial chunks since that would require
the partials siafile to reference another partials siafile. Instead it only
contains combined chunks which are full chunks by definition. Since a
combined chunk's size depends on its erasure code settings the same way that
a regular full chunk's size does, we can only combined partial chunks with
the same erasure code settings into a combined chunk which has the same
settings as well. This means that for every new erasure code setting, a
unique partials siafile will be created.

One implication of having a `SiaFile` point to a partials siafile is the fact
that we don't know the corresponding partials siafile before loading the
`SiaFile` unless we create a new `SiaFile` using `New`. That means when we
load a `SiaFile` from a backup or from disk, we need to manually set the
partials siafile afterwards using `SetPartialsSiaFile`.

### Partial Upload Workflow
Upon the creation of a `SiaFile` we can determine if it contains a partial
chunk by looking at the filesize. If the filesize is not a multiple of the
chunk size of the file, we set the `HasPartialChunk` field in the metadata to
'true'. In this state, the reported `Health` and `Redundancy` of the partial
chunk will be the worst possible value for both the repair code and users of
the API since the chunk isn't downloadable. Once the repair code picks up the
chunk, it will move the chunk into a combined chunk and call
`SetPartialChunks` on the `SiaFile`, effectively moving the status of the
partial chunk to `CombinedChunkStatusIncomplete`. At this point, the `Health`
and `Redundancy` reported to users are the highest possible values while for
the repair loop it is still the lowest. That way we guarantee that the repair
loop periodically checks if the combined chunk is ready for uploading. Once
it is, the status of the partial chunk will be moved to
`CombinedChunkStatusComplete` and both `Health` and `Redundancy` will start
reporting the actual values for the combined chunk.

## Structure of the SiaFile:
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
- [File Format Subsystem](#file-format-subsystem)
- [Persistence Subsystem](#persistence-subsystem)
- [SiaFileSet Subsystem](#siafileset-subsystem)
- [Snapshot Subsystem](#snapshot-subsystem)
- [Partials Siafile Subsystem](#partials-siafile-subsystem)

### Erasure Coding Subsystem
**Key Files**
- [rscode.go](./rscode.go)
- [rssubcode.go](./rssubcode.go)

### File Format Subsystem
**Key Files**
- [siafile.go](./siafile.go)
- [metadata.go](./metadata.go)

The file format subsystem contains the type definitions for the SiaFile
format and most of the exported methods of the package.

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

### Partials Siafile Subsystem
**Key Files**
- [partialssiafile.go](./partialssiafile.go)

The partials siafile subsystem contains code which is exclusively used by
partials siafiles or partial upload related helper functions. All other
methods are shared by regular siafiles and partials siafiles.