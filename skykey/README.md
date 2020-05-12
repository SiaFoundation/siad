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

## Usage

Skykeys are primarily used for encrypting skyfiles. Currently all skykeys are used with the 
XChaCha20 stream cipher. Key re-use is safe with this encryption scheme if we
use random nonces for each message. This is safe until `2 << 96` messages are
transmitted.

## Key Derivation

The skykey manager stores only master skykeys. These skykeys are not used
directly for encryption/decryption. Rather they are used to derive file-specific
Skykeys. File-specific skykeys share the same key material as the master skykey
they are derived from. They differ in the nonce value. This allows us to reuse
the master skykey for multiple files, by using a new file-specific skykey for
every new file. 

The method `GenerateFileSpecificSubkey` is used to create new file-specific
sub-keys from a master skykey. 

Further levels of key derivation may be necessary and are supported by using the
`DeriveSubkey` method.

## Skyfile encryption
Two other types of subkeys are the ones actually used for encrypting skyfiles.
There is a `BaseSector` derivation and a `Fanout` derivation which are used for
encrypting the base sector and fanout of a skyfile respectively. 

This is necessary because of the final level of key derivation used in the upload
process of Sia. When splitting up files for redundancy, each `(chunkIndex,
pieceIndex)` upload uses a different XChaCha20 nonce as well. To avoid re-using
the same `(chunkIndex, pieceIndex)` derivation for the base sector and fanout
sections, we just use a different nonce for each.
