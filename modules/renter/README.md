# Renter
The Renter is responsible for tracking all of the files that a user has uploaded
to Sia, as well as the locations and health of these files.

## Submodules
The Renter has several submodules that each perform a specific function for the
Renter. This `README` will provide brief overviews of the submodules, but for
more detailed descriptions of the inner workings of the submodules the
respective `README` files should be reviewed.
 - Contractor
 - HostDB
 - Proto
 - SiaDir
 - SiaFile

### Contractor
The Contractor manages the Renter's contracts and is responsible for all
contract actions such as new contract formation and contract renewals. The main
control logic of the Contractor begins in
[`threadedContractMaintenance`](https://gitlab.com/NebulousLabs/Sia/blob/master/modules/renter/contractor/contractmaintenance.go#L748).
`threadedContractMaintenance` is a background loop that is trigger by a
consensus change or the Renter setting an Allowance. Once triggered, the
Contractor begins checking for contracts and forming new contracts if there are
not enough and renewing or refreshing any current contracts as needed. Most of
the critical operations within the contract maintenance require the wallet to be
unlocked.

### HostDB
The HostDB manages the hosts that have been announced for the Renter. The HostDB
is responsible for scoring and sorting the hosts so that when hosts are needed
for contracts high quality hosts are provided. Additionally the HostDB scans the
hosts for changes so that if a host changes its settings or goes offline the
Renter is notified and can have its Contractor adjust any contracts accordingly.

### Proto
TODO - Chris

### SiaDir
The SiaDir package is the code that defines what a directory is on the Sia network.

### SiaFile
The SiaFile package is the code that defines what a file is on the Sia network.

### Worker
While the Worker is not currently a submodule, it plays a key role for the
Renter. A worker represents a host that the renter has a contract with. The
renter's worker pool is responsible for both uploads and downloads.

## Functions of the Renter
The Renter has the following functions that it performs.
 - Downloads
 - Repairs / Uploads

### Downloads
TODO - Chris

### Repairs and Uploads
The following describes the work flow of how the Renter repairs files.

There are 3 main functions that work together to make up Sia's file repair
mechanism, `threadedUpdateRenterHealth`, `threadedUploadAndRepairLoop`, and
`threadedStuckFileLoop`. These 3 functions will be referred to as the health
loop, the repair loop, and the stuck loop respectively.

The health loop is responsible for ensuring that the health of the renter's file
directory is updated periodically. Along with the health, the metadata for the
files and directories is also updated. The metadata information for a directory
is stored in the .siadir metadata file and contains directory specific and
aggregate information. The aggregate fields are the worst values for any of the
files and sub directories. This is true for all directories which, for example,
means the health of top level directory of the renter is the health of the worst
file in the renter. For health and stuck health the worst value is the highest
value, for timestamp values the oldest timestamp is the worst value, and for
aggregate values (ie NumStuckChunks) it will be the sum of all the files and sub
directories. The health loop keeps the renter file directory updated by
following the path of oldest LastHealthCheckTime and then calling
`managedBubbleMetadata` or `threadedBubbleMetadata`, to be referred to as
bubble, on that directory. When a directory is bubbled, the metadata information
is recalculated and saved to disk and then bubble is called on the parent
directory until the top level directory is reached. If during a bubble a file is
found that meets the threshold health for repair, then a signal is sent to the
repair loop. If a stuck chunk is found then a signal is sent to the stuck loop.
Once the entire renter's directory has been updated within the
healthCheckInterval the health loop sleeps until the time interval has passed.

The repair loop is responsible for repairing the renter's files, this includes
uploads. The repair loop uses a `directoryHeap` which is a max heap of directory
elements sorted by health. The repair loop follows the following sudo code:
```
// Add backup chunks to heap

// Check if file system is healthy and no chunks in upload heap
   // Sleep until triggered by upload or repair needed

// Add chunks to heap

// Repair chunks

// Call bubble to update file system
```
We always check for backup chunks first to ensure backups are succeeding. The
repair loop then checks if the file system is healthy by checking the top
directory element in the directory heap. If healthy and there are no chunks
currently in the upload heap, then the repair loop sleeps until it is triggered
by a new upload or a repair is needed. Chunks are added to the upload heap by
popping the directory off the directory heap and adding any chunks that are a
worse health than the next directory in the directory heap. This continues until
the `MaxUploadHeapChunks` is met. The repair loop will then repair those chunks
and call bubble on the directories that chunks were added from to keep the file
system updated. This will continue until the file system is healthy, which means
all files have a health less than the `RepairThreshold`.

The stuck loop is responsible for targeting chunks that didn't get repaired
properly. The stuck loop randomly finds a directory containing stuck chunks and
then will randomly add one stuck chunk to the heap. Stuck chunks are priority in
the heap, so limiting it to `MaxStuckChunksInHeap` at a time prevents the heap
from being saturated with stuck chunks that potentially cannot be repaired which
would cause no other files to be repaired. If the repair of a stuck chunk is
successful, a signal is sent to the stuck loop and another stuck chunk is added
to the heap. If the repair wasn't successful, the stuck loop will wait for the
`repairStuckChunkInterval` to pass and then try another random stuck chunk. If
the stuck loop doesn't find any stuck chunks, it will sleep until a bubble
triggers it by finding a stuck chunk.