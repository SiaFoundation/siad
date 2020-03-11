# Proto
Coming Soon...

## Contracts
Coming Soon...

### SafeContract
Coming Soon...

### ContractSet
Coming Soon...

## Downloader
Coming Soon...

## Editor
Coming Soon...

## FileSection
Coming Soon...

## FormContract
Coming Soon...

## Merkle Roots
Coming Soon...

## Negotiate
Coming Soon...

## Reference Counter
The reference counter is a file that accompanies the contract and keeps track of
the number of references to each sector. The number of references starts at one
and increases as the user creates new backups of this contract. It decreases as
the user deletes backups or deletes the file itself. Once the counter reaches
zero, there is no way for the user to use the data stored in the sector. At this
moment we are free to either reuse the sector in order to store newly uploaded
data or to drop the sector when renewing the contract. The way we do that is by
swapping the zero ref sector with the last sector in the contract and dropping
it from the reference counter list. This marks the sector as garbage and it's up
for reuse/drop.

The refrence counter is created and deleted together with the contract. The
counts it holds should be updated on backup creation/deletion and on file
deletion.

## Renew
Coming Soon...

## Seeds
Coming Soon...

## Session
Coming Soon...
