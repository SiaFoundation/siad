# SIP-0007 - Content Based Linkfiles

Status: Proposal

SIP-0007 is a description of a file sharing format for data stored on Sia. In
particular, the format is for a content-addressed file which can be accessed
using a 65 character URL. The URL contains a Merkle root which commits to the
entire contents of the file that is being shared, as well as some additional
information that is useful for fetching the file quickly.

## Motivation and Rationale

Decentralized applications have in practice proven to require access to
convenient and efficient filesharing. Many applications need to put filesharing
links onto blockchains, which means the link needs to be small. Other
applications use communication infrastructure that cannot support shared objects
as large as siafiles, and therefore would benefit from having simple links.

These links function similar to cryptocurrency addresses in how they can be
shared, and greatly improve the UX of publishing data on Sia.

## Content Based Linkfile Specification

A linkfile is a special type of siafile that has wrapped the underlying file
with some additional metadata that allow the file to be exported as nothing more
than a 65 character sialink. The key property of this file is that it can be
recovered in full by an external party that knows nothing more than the data
made available through the 65 character sialink. An example of a sialink would
be:

`sia://Df2ppPMjUikqRaJeopveNWQRpRhQQQ7mex3Z3q7H1smpvJodQE7is63q1Dt`

The linkfile itself is broken into two sections. The first section contains the
medatdata and the leading file chunk, and the second section contains the fanout
chunks. The structure of the data and redundancy settings are different in each
section, therefore a "linkfile" is actually composed of two different siafiles,
one siafile for each section.

### First Section - The Leading Data Section

The first section is a siafile with a 1-of-N erasure coding setting and is only
a single chunk. This means that the logical data of the chunk is only 4 MiB.
This section serves as a launchpoint for downloading the other parts of the
file. A 1-of-N erasure coding scheme is required because the sialink that is
generated from the linkfile only has enough space to report one Merkle root,
from that root all other data must be retriveable. If the first section were
erasure coded, a single Merkle root would not be sufficient to learn how to
download the file. By default, encryption needs to be set to plaintext, as the
sialink will not contain any decryption information.

The linkfile contents are not typical file contents, because all of the metadata
of the file also needs to be stored in the linkfile contents, as well as any
fanout information for how the rest of the chunks in the linkfile are handled.
This is because the downloader does not have access to the .siafile, the
downloader only has access to the 65 character sialink.

The first 4096 bytes of the siafile are reserved for file metadata. This
includes things like file mode, atime, mtime, and other file attributes. The
second 8192 bytes are reserved for fanout information, with the first 32 bytes
of the fanout information being dedicated to the erasure coding information of
the fanout. The remaining data is the physical file data.

```go
type LinkFileSectionOne struct {
	[4096]byte    // File Metadata, stored as JSON. JSON encoded metadata must not exceed 4096 bytes
	uint8         // Version
	uint64        // Logical Filesize
	uint8         // Leading Data Pieces
	uint8         // Leading Parity Pieces
	uint8         // Fanout Data Pieces
	uint8         // Fanout Parity Pieces
	[19]byte      // Reserved for future use.
	[255][32]byte // 255 sector roots of the fanout sectors.
	[4182016]byte // Physical data of the first chunk of the file
}
```

The physical file data is itself erasure coded data. The information for the
data pieces and parity pieces of the physical data is provided in the actual
sialink. Note that even though the physical data is erasure coded, every single
host has the exact same sector, which means that every single host has every
single one of the erasure coded pieces.

The erasure coding is not applied to increase the integrity of the sector, the
sector fundamentally has 1-of-N redundancy. The physical data of the sector is
erasure coded to improve download performance, allowing a viewnode to download
different data from many hosts in parallel at once, even without knowing in
advance which hosts have the sector.

Only the 4182016 bytes of physical data get erasure coded. The first 12288 bytes
are kept in full, not erasure coded on each host.

The physical data is split into K equal sized pieces, where K is the number of
leading data pieces plus the number of leading parity pieces. The total amount
of logical data that fits inside of the physical data will depend on the erasure
coding settings. Higher values for the data pieces mean that more parallel
downloads can be executed simultaneously. Higher values for the parity pieces
decrease the chance that the viewnode will accidentally download redundant data
when searching for the pieces. There is no value to setting K to be larger than
the total number of hosts that are holding onto the sector, and the protocol is
most efficient when K is exactly equal to the number of hosts holding onto the
sector. The recommended erasure coding settings are 8 data pieces and 2 parity
pieces, for a total of 10 pieces, with the sector being stored on 10 hosts,
giving the sector a total of 10x redundancy. The total amount of logical data
that can fit in the leading section with these settings is about 3.3 MB.

TODO: Livetest various settings for the erasure coding to see if 8:2 is really
the best choice.

TODO: I believe the erasure coding library likes the pieces padded to the
nearest 64 bytes, but I am not sure. If it's not 64 bytes, insert the correct
number below. I don't remember if it hurts performance for 64 bytes versus 1024
or other values, need to check that as well.

The erasure coding should be performed such that any row of 64 bytes can be
extracted from the pieces and decoded correctly. This enables partial downloads.
TODO: Need to explain this better. But basically it's the erasure stuff that we
do in our existing siafiles so that we don't need to download all the data in
order to decode it.

### Second Section - The Fanout Section

TODO: Fill this out. Major constraints: we'd like users to be able to upload the
data sequentially, in byte-order to the viewnode. But, because the merkle root
of the leading data needs to commit/include all of the data, the fanout has to
be constructed such that it can be built in order yet have the commitments work
out correctly. This almost certainly requires keeping the leading data in memory
while the rest of the upload completes, but hopefully does not require keeping
any of the rest of the data in memory. We can achieve that by building the
fanout backwars, BUT, we also want to be able to download-stream in-order, so
ideally the fanout can be constructed such that it's fast for the viewnode to
stream the downloads the right way without having to scan through the entire
fanout to start a download.

### Sialink Structure

The sialink itself contains several fields, composing a total of 43 bytes in the
base58 string:

+ 1  byte:  Version
+ 32 bytes: Sector Root
+ 8  bytes: Filesize
+ 1  byte:  Leading Data Pieces
+ 1  byte:  Leading Parity Pieces

#### Version

The version byte of SIP-0007 sialinks is always set to '1'. If a new version is
added, that will be covered in a different SIP.

#### Sector Root

This is the sector root of the leading section of the file. Hosts can be queried
by sector root to retreive the underlying data.

#### Filesize

This is the total size of the logical data in the file. This information can be
used to determine how much data needs to be requested, and also to determine the
fanout structure of the file.

This is the size of the file inside of the linkfile, excluding any metadata and
excluding any fanout data. This data is used to help the viewnode understand how
large the download requests need to be.

#### Leading Data Pieces

The number of data pieces that are in the FileLeadingBytes. Recommended default
value is 8.

#### Leading Parity Pieces

The number of parity pieces that are in the FileLeadingBytes. Recommended
default value is 2. Combined with the recommended base data pieces, this means
that it's recommended that the sector be put on 10 hosts total.

NOTE: even though the sector is on 10 hosts total, all 10 hosts have the exact
same data for the FileLeadingBytes - the sector root must be identical across
all 10 hosts.

### Creating the Linkfile

TODO: Tricks and tips for creating the linkfile. One thing that we need to
remember is that if 100% of the physical redundancy of the file is exposed in
the sialink or shared publicly, the hosts are able to perform a deduplication
attack which can reduce the integrity of the file. For the time being this can
be resolved by having the user maintain a normal file as well as the linkfile if
they want to be able to repair the file. May want to be able to point to a
normal file as the "on disk" location of the logical data, so that the linkfile
can be repaired using the hidden redundancy of the normal file.

### Encryption Extensions

Linkfiles can be encrypted so that only authorized parties are able to read the
underlying contents.

`sia://HFpP7QB2hMRpPSP5giWWVrKe8JKgNh3kbRTZafk27kXrEB3Gxaz51CAhRS1.nebulous`

The receiver of the sialink knows to look up an encryption key corresponding to
the ID that is appended to the sialink. The encryption key must be used to
decode the base58 extension, which results in a normal looking file sialink. The
file itself also must be decrypted using the same key.

TODO: Think of a protocol so the extension can be obfuscated itself - a party
shouldn't be able to tell that "nebulous" is the intended receiver of the
sialink... Initial exploration here has largely suggested that this may not be
feasible with our current knowledge.
