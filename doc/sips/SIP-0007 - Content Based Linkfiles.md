# SIP-0007 - Content Based Linkfiles

Status: Proposal

SIP-0007 is a description of a file format for storing data on Sia. In
particular, the format is for a content-addressed file which can be summarized
and accessed using nothing more than a single Merkle root. The Merkle root
encompasses a commitment to the entire file, changing any data in the file would
change the resulting Merkle root that can be used to access the file.

## Motivation and Rationale

TODO: make this better

support viewnodes

## Content Based Linkfile Specification

A linkfile is a file that can be accessed and decoded using nothing more than a
URL-looking link. An example of such a link would be:

`sia://Df2ppPMjUikqRaJeopveNWQRpRhQQQ7mex3Z3q7H1smpvJodQE7is63q1Dt`

The linkfile itself contains several fields, composing a total of 43 bytes in
the base58 string:

+ 1  byte:  Version
+ 32 bytes: Sector Root
+ 8  bytes: Filesize
+ 1  byte:  Base Data Pieces
+ 1  byte:  Base Parity Pieces

The base sector unpacks into roughly this shape:

type BaseSector struct {
	FileMetadata       // 4096 bytes - contains things like the file's mode, tags, and attributes
	FanoutDataPieces   // 1 byte
	FanoutParityPieces // 1 byte
	FanoutRoots        // 3200 bytes, 100 fanout roots that are 32 bytes each

	FileLeadingBytes []byte // The rest of the sector, sector can be 4 MiB total.
}

The FileLeadingBytes are erasure coded, even though the exact same data is on
every host. The fetch pattern that a viewnode uses to get the initial sector is
to send out a download request to every host which includes downloading all of
the initial data for the BaseSector (7298 bytes), and then it grabs one piece of
the FileLeadingBytes.

For example, if there are 8 data pieces and 2 parity pieces, and the filesize
indicates that there are 4 million bytes in the FileLeadingBytes, each host will
grab a different 400,000 bytes, allowing for higher parallelism on the download.

So even though every single host contains the exact same data, the viewnode
doesn't know which hosts on the network are actually storing data, and the
viewnode can get better throughput and latency by asking each host for a
fraction of the data instead of all of it, and then using erasure coding to
minimize the total amount of redundant requests.

The base sector erasure coded data is coded into fragments of 64 bytes. Meaning
that you can run a decode on every set of 64 bytes from each data and parity
piece once enough data has been downloaded. (or change 64 bytes to whatever
makes sense and is high performance for our erasure coding library)

### Linkfile Fields Breakdown

#### Version

The version byte of SIP-0007 links is always set to '1'.

#### Sector Root

This is the sector root of the linkfile. This sector root will be placed on a
number of hosts so that viewnodes can discover it. When downloaded, it will
contain all of the file metadata, and all of the fanout data.

#### Filesize

This is the size of the file inside of the linkfile, excluding any metadata and
excluding any fanout data. This data is used to help the viewnode understand how
large the download requests need to be.

#### Base Data Pieces

The number of data pieces that are in the FileLeadingBytes. Recommended default
value is 8.

#### Base Parity Pieces

The number of parity pieces that are in the FileLeadingBytes. Recommended
default value is 2. Combined with the recommended base data pieces, this means
that it's recommended that the sector be put on 10 hosts total.

NOTE: even though the sector is on 10 hosts total, all 10 hosts have the exact
same data for the FileLeadingBytes - the sector root must be identical across
all 10 hosts.

### Linkfile Fanout Specification

The fanout size in combination with the redundancy specified by the fanout
erasure coding settings can be used to calculate the total number of fanout
sectors that will be necessary.

The leading 7298 bytes of each fanout sector can be used to specify up to and
additional 228 fanout sectors, allowing for larger files if necessary. The
fanout sector data is ordered as a bredth-first-search.

Unlike the FileLeadingBytes, each sector in the fanout only contains a single
piece of erasure coded data. This means that data cannot be recovered just by
downloading a single sector if there is more than 1 data piece.

### Encryption Extensions

Linkfiles can be encrypted so that only authorized parties are able to read the
underlying contents.

`sia://HFpP7QB2hMRpPSP5giWWVrKe8JKgNh3kbRTZafk27kXrEB3Gxaz51CAhRS1.nebulous`

The receiver of the link knows to look up an encryption key corresponding to the
ID that is appended to the link. The encryption key must be used to decode the
base58 extension, which results in a normal looking file link. The file itself
also must be decrypted using the same key.

TODO: Think of a protocol so the extension can be obfuscated itself - a party
shouldn't be able to tell that "nebulous" is the intended receiver of the link.
