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
// The download code follows a clean/intuitive flow for getting super high and
// computationally efficient parallelism on downloads. When a download is
// requested, it gets split into its respective chunks (which are downloaded
// individually) and then put into the download heap. The primary purpose of the
// download heap is to keep downloads on standby until there is enough memory
// available to send the downloads off to the workers. The heap is sorted first
// by priority, but then a few other criteria as well.
//
// Some downloads, in particular downloads issued by the repair code, have
// already had their memory allocated. These downloads get to skip the heap and
// go straight for the workers.
//
// When a download is distributed to workers, it is given to every single worker
// without checking whether that worker is appropriate for the download. Each
// worker has their own queue, which is bottlenecked by the fact that a worker
// can only process one item at a time. When the worker gets to a download
// request, it determines whether it is suited for downloading that particular
// file. The criteria it uses include whether or not it has a piece of that
// chunk, how many other workers are currently downloading pieces or have
// completed pieces for that chunk, and finally things like worker latency and
// worker price.
//
// If the worker chooses to download a piece, it will register itself with that
// piece, so that other workers know how many workers are downloading each
// piece. This keeps everything cleanly coordinated and prevents too many
// workers from downloading a given piece, while at the same time you don't need
// a giant messy coordinator tracking everything. If a worker chooses not to
// download a piece, it will add itself to the list of standby workers, so that
// in the event of a failure, the worker can be returned to and used again as a
// backup worker. The worker may also decide that it is not suitable at all (for
// example, if the worker has recently had some consecutive failures, or if the
// worker doesn't have access to a piece of that chunk), in which case it will
// mark itself as unavailable to the chunk.
//
// As workers complete, they will release memory and check on the overall state
// of the chunk. If some workers fail, they will enlist the standby workers to
// pick up the slack.
//
// When the final required piece finishes downloading, the worker who completed
// the final piece will spin up a separate thread to decrypt, decode, and write
// out the download. That thread will then clean up any remaining resources, and
// if this was the final unfinished chunk in the download, it'll mark the
// download as complete.

// The download process has a slightly complicating factor, which is overdrive
// workers. Traditionally, if you need 10 pieces to recover a file, you will use
// 10 workers. But if you have an overdrive of '2', you will actually use 12
// workers, meaning you download 2 more pieces than you need. This means that up
// to two of the workers can be slow or fail and the download can still complete
// quickly. This complicates resource handling, because not all memory can be
// released as soon as a download completes - there may be overdrive workers
// still out fetching the file. To handle this, a catchall 'cleanUp' function is
// used which gets called every time a worker finishes, and every time recovery
// completes. The result is that memory gets cleaned up as required, and no
// overarching coordination is needed between the overdrive workers (who do not
// even know that they are overdrive workers) and the recovery function.

// By default, the download code organizes itself around having maximum possible
// throughput. That is, it is highly parallel, and exploits that parallelism as
// efficiently and effectively as possible. The hostdb does a good of selecting
// for hosts that have good traits, so we can generally assume that every host
// or worker at our disposable is reasonably effective in all dimensions, and
// that the overall selection is generally geared towards the user's
// preferences.
//
// We can leverage the standby workers in each unfinishedDownloadChunk to
// emphasize various traits. For example, if we want to prioritize latency,
// we'll put a filter in the 'managedProcessDownloadChunk' function that has a
// worker go standby instead of accept a chunk if the latency is higher than the
// targeted latency. These filters can target other traits as well, such as
// price and total throughput.


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