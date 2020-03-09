# Skykey Manager
The `skykey` package defines Skykeys used for encrypting files in Skynet and
provides a way to persist Skykeys using a `SkykeyManager` that manages these
keys in a file on-disk.

The file consists of a header which is:
  `SkykeyFileMagic | SkykeyVersion | Length`

The `SkykeyFileMagic` never changes. The version only changes when
backwards-incompatible changes are made to the design of `Skykeys` or to the way
this file is structured. The length refers to the number of bytes in the file.

When adding a `Skykey` to the file, a `Skykey` is unmarshaled and appended to
the end of the file and the file is then synced. Then the length field in the
header is updated to indicate the newly written bytes and the file is synced
once again.

## Skykeys
A `Skykey` is a key associated with a name to be used in Skynet to share
encrypted files. Each key has a name and a unique identifier.
