# Renter
The Renter is responsible for tracking and actively maintaining all of the files
that a user has uploaded to Sia. This includes the location and health of these
files. The Renter, via the HostDB and the Contractor, is also responsible for
picking hosts and maintaining the relationship with them.

*TODO*
  - Should assumptions for each section be put in a specific **assumptions**
    section at the end of each section?
  - Update list of submodules to be links to README files was submodule READMEs
    are ready
  - If we like this format for the README we should document it and make it
    standard for consistency between module READMEs. Things to consider:
     - What gets `highlighted`
     - What gets linked
     - What Sections to have and order
  - Confirm all assumptions have tests

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
`threadedContractMaintenance` is a background loop that is triggered by a
consensus change or the Renter setting an Allowance. Once triggered, the
Contractor begins checking for contracts and forming new contracts if there are
not enough and renewing or refreshing any current contracts as needed. Many of
the critical operations within the contract maintenance require the wallet to be
unlocked.

### HostDB
The HostDB curates and manages a list of hosts that may be useful for the renter
in storing various types of data. The HostDB is responsible for scoring and
sorting the hosts so that when hosts are needed for contracts high quality hosts
are provided. 

### Proto
The proto package implements the renter's half of the renter-host protocol,
including contract formation and renewal RPCs, uploading and downloading,
verifying Merkle proofs, and synchronizing revision states. It is a low-level
package whose functionality is largely wrapped by the Contractor.

### SiaDir
The SiaDir package is the code that defines what a directory is on the Sia network.

### SiaFile
The SiaFile package is the code that defines what a file is on the Sia network.

## Subsystems of the Renter
The Renter has the following subsystems that help carry out its
responsibilities.
 - [Filesystem Controllers](#filesystem-controllers)
 - [Persistance Subsystem](#persistance-subsystem)
 - [Memory Subsystem](#memory-subsystem)
 - [Worker Subsystem](#worker-subsystem)
 - [Download Subsystem](#download-subsystem)
 - [Download Streaming Subsystem](#download-streaming-subsystem)
 - [Upload Subsystem](#upload-subsystem)
 - [Upload Streaming Subsystem](#upload-streaming-subsystem)
 - [Health and Repair Subsystem](#health-and-repair-subsystem)
 - [Backup Subsystem](#backup-subsystem)

### Filesystem Controllers
**Key Files**
 - [dirs.go](./dirs.go)
 - [files.go](./files.go)

**Key Constants**
 - N/A

*TODO* 
  - fill out subsystem explanation

### Persistance Subsystem
**Key Files**
 - [persist_compat.go](./persist_compat.go)
 - [persist.go](./persist.go)

**Key Constants**
 - N/A

*TODO* 
  - fill out subsystem explanation

### Memory Subsystem
**Key Files**
 - [memory.go](./memory.go)

**Key Constants**
 - defaultMemory

*TODO* 
  - fill out subsystem explanation

### Worker Subsystem
**Key Files**
 - [worker.go](./worker.go)
 - [workerdownload.go](./workerdownload.go)
 - [workerupload.go](./workerupload.go)

**Key Constants**
 - downloadFailureCooldown
 - maxConsecutivePenalty
 - uploadFailureCooldown
 - workerPoolUpdateTimeout

*TODO* 
  - expand subsystem description

The worker subsystem is the interface between the renter and the hosts. All
actions (with the exception of some legacy actions that are going to be migrated
over) that involve working with hosts will pass through the worker. 

### Download Subsystem
**Key Files**
 - [download.go](./download.go)
 - [downloadheap.go](./downloadheap.go)
 - [downloadchunk.go](./downloadchunk.go)

**Key Constants**
 - N/A

*TODO* 
  - expand subsystem description

The download code follows a clean/intuitive flow for getting super high and
computationally efficient parallelism on downloads. When a download is
requested, it gets split into its respective chunks (which are downloaded
individually) and then put into the download heap. The primary purpose of the
download heap is to keep downloads on standby until there is enough memory
available to send the downloads off to the workers. The heap is sorted first
by priority, but then a few other criteria as well.

Some downloads, in particular downloads issued by the repair code, have
already had their memory allocated. These downloads get to skip the heap and
go straight for the workers.

When a download is distributed to workers, it is given to every single worker
without checking whether that worker is appropriate for the download. Each
worker has their own queue, which is bottlenecked by the fact that a worker
can only process one item at a time. When the worker gets to a download
request, it determines whether it is suited for downloading that particular
file. The criteria it uses include whether or not it has a piece of that
chunk, how many other workers are currently downloading pieces or have
completed pieces for that chunk, and finally things like worker latency and
worker price.

If the worker chooses to download a piece, it will register itself with that
piece, so that other workers know how many workers are downloading each
piece. This keeps everything cleanly coordinated and prevents too many
workers from downloading a given piece, while at the same time you don't need
a giant messy coordinator tracking everything. If a worker chooses not to
download a piece, it will add itself to the list of standby workers, so that
in the event of a failure, the worker can be returned to and used again as a
backup worker. The worker may also decide that it is not suitable at all (for
example, if the worker has recently had some consecutive failures, or if the
worker doesn't have access to a piece of that chunk), in which case it will
mark itself as unavailable to the chunk.

As workers complete, they will release memory and check on the overall state
of the chunk. If some workers fail, they will enlist the standby workers to
pick up the slack.

When the final required piece finishes downloading, the worker who completed
the final piece will spin up a separate thread to decrypt, decode, and write
out the download. That thread will then clean up any remaining resources, and
if this was the final unfinished chunk in the download, it'll mark the
download as complete.

The download process has a slightly complicating factor, which is overdrive
workers. Traditionally, if you need 10 pieces to recover a file, you will use
10 workers. But if you have an overdrive of '2', you will actually use 12
workers, meaning you download 2 more pieces than you need. This means that up
to two of the workers can be slow or fail and the download can still complete
quickly. This complicates resource handling, because not all memory can be
released as soon as a download completes - there may be overdrive workers
still out fetching the file. To handle this, a catchall 'cleanUp' function is
used which gets called every time a worker finishes, and every time recovery
completes. The result is that memory gets cleaned up as required, and no
overarching coordination is needed between the overdrive workers (who do not
even know that they are overdrive workers) and the recovery function.

By default, the download code organizes itself around having maximum possible
throughput. That is, it is highly parallel, and exploits that parallelism as
efficiently and effectively as possible. The hostdb does a good job of selecting
for hosts that have good traits, so we can generally assume that every host
or worker at our disposable is reasonably effective in all dimensions, and
that the overall selection is generally geared towards the user's
preferences.

We can leverage the standby workers in each unfinishedDownloadChunk to
emphasize various traits. For example, if we want to prioritize latency,
we'll put a filter in the 'managedProcessDownloadChunk' function that has a
worker go standby instead of accept a chunk if the latency is higher than the
targeted latency. These filters can target other traits as well, such as
price and total throughput.

### Download Streaming Subsystem
**Key Files**
 - [downloaddestination.go](./downloaddestination.go)
 - [downloadstreamer.go](./downloadstreamer.go)

**Key Constants**
 - initialStreamerCacheSize
 - maxStreamerCacheSize

*TODO* 
  - fill out subsystem explanation

### Upload Subsystem
**Key Files**
 - [directoryheap.go](./directoryheap.go)
 - [upload.go](./upload.go)
 - [uploadheap.go](./uploadheap.go)
 - [uploadchunk.go](./uploadchunk.go)

**Key Constants**
 - N/A

*TODO* 
  - expand subsystem description

The Renter uploads `siafiles` in 40MB chunks. Redundancy kept at the chunk level
which means each chunk will then be split in `datapieces` number of pieces. For
the standard 10/20 scheme this means that each 40MB chunk will be split into 10
4MB pieces, which is turn will be uploaded to 30 different hosts (10 data piecs
and 20 parity pieces).

Chunks are uploaded by first distributing the chunk to the worker pool. The
chunk is distributed to the worker pool by adding it to the upload queue and
then signalling the worker upload channel. Workers that are waiting for work
will receive this channel and begin the upload. First the worker creates a
connection with the host by creating an `editor`. Next the `editor` is used to
update the file contract with the next data being uploaded. This will update the
merkle root and the contract revision.

### Upload Streaming Subsystem
**Key Files**
 - [uploadstreamer.go](./uploadstreamer.go)

**Key Constants**
 - N/A

*TODO* 
  - fill out subsystem explanation

### Health and Repair Subsystem
**Key Files**
 - [metadata.go](./metadata.go)
 - [repair.go](./repair.go)
 - [uploadheap.go](./uploadheap.go)

**Key Constants**
 - RepairThreshold
 - maxStuckChunksInHeap
 - maxRepairLoopTime
 - maxUploadHeapChunks
 - minUploadHeapSize
 - repairLoopResetFrequency
 - repairStuckChunkInterval
 - stuckLoopErrorSleepDuration
 - uploadAndRepairSleepDuration

*TODO*
  - Update naming of bubble methods to updateAggregateMetadata, this will more
    closely match the file naming as well. Update the health loop description to
    match new naming
  - Move HealthLoop and related methods out of repair.go to health.go
  - Pull out repair code from  uploadheap.go so that uploadheap.go is only heap
    related code. Put in repair.go
  - Pull out stuck loop code from repair.go and uploadheap.go and put in
    stuck.go
  
There are 3 main functions that work together to make up Sia's file repair
mechanism, `threadedUpdateRenterHealth`, `threadedUploadAndRepairLoop`, and
`threadedStuckFileLoop`. These 3 functions will be referred to as the health
loop, the repair loop, and the stuck loop respectively.

The Health and Repair Subsystems work off of the directory metadata. The
metadata information contains directory specific and aggregate information. The
aggregate fields are the worst values for any of the files and sub directories.
This is true for all directories which, for example, means the health of top
level directory of the renter is the health of the worst file in the renter. For
health and stuck health the worst value is the highest value, for timestamp
values the oldest timestamp is the worst value, and for aggregate values (ie
NumStuckChunks) it will be the sum of all the files and sub directories.

#### Health Loops
The health loop is responsible for ensuring that the health of the renter's file
directory is updated periodically. Along with the health, the metadata for the
files and directories is also updated. Health is defined as the percent of
redundancy missing, this means that a health of 0 is a full health file.

The health loop keeps the renter file directory updated by following the path of
oldest `LastHealthCheckTime` and then calling `managedBubbleMetadata` or
`threadedBubbleMetadata`, to be referred to as bubble, on that directory. When a
directory is bubbled, the metadata information is recalculated and saved to disk
and then bubble is called on the parent directory until the top level directory
is reached. If during a bubble a file is found that meets the threshold health
for repair, then a signal is sent to the repair loop. If a stuck chunk is found
then a signal is sent to the stuck loop. Once the entire renter's directory has
been updated within the healthCheckInterval the health loop sleeps until the
time interval has passed.

**Assumptions / Complexities**
 - `LastHealthCheckTime` is being kept updated and accurate throughout the
   filesystem.

#### Repair Loop
The repair loop is responsible for uploading new files to the renter and
repairing existing files. The heart of the repair loop is
`threadedUploadAndRepair`, a thread that continually checks for work, schedules
work, and then updates the filesystem when work is completed.

We always check for backup chunks first to ensure backups are succeeding. For
the rest of the filesystem the repair loop uses a directory heap. The
directoryHeap is a max heap of directory elements sorted by health. The repair
loop checks if the file system is healthy by checking the top directory element
in the directory heap. If healthy and there are no chunks currently in the
upload heap, then the repair loop sleeps until it is triggered by a new upload
or a repair is needed. Chunks are added to the upload heap by popping the
directory off the directory heap and adding any chunks that are a worse health
than the next directory in the directory heap. This continues until the
`MaxUploadHeapChunks` is met. The repair loop will then repair those chunks and
call bubble on the directories that chunks were added from to keep the file
system updated. This will continue until the file system is healthy, which means
all files have a health less than the `RepairThreshold`.

When repairing files, the Renter will first try and repair the `siafile` from
the local file on disk. If the local file is not present, the Renter will
download the needed data from its contracts in order to perform the repair. In
order for a remote repair, ie repairing from data downloaded from the Renter's
contracts, to be successful the `siafile` must be at 1x redundancy or better. If
a `siafile` is below 1x redundancy and the local file is not present the file is
considered lost as there is no way to repair it. 

**Assumptions / Complexities**
 - New uploads have chunks added directly to the upload heap
 - Repair loop will be sleep until work is needed meaning other threads will
   wake up the repair loop 
 - Backup chunks are added first
 - Repair loop doesn't account for the fact that workers take time to finish an
   upload. Currently is assumes that this work finishing immediately

#### Stuck Loop
File's are marked as `stuck` if the Renter is unable to fully upload the file.
While there are many reasons a file might not be fully uploaded, failed uploads
due to the Renter, ie the Renter shut down, will not cause the file to be marked
as `stuck`. The intention is that if a file is marked as `stuck` then it is
assumed that there is a problem with the file itself.

The stuck loop is responsible for targeting chunks that didn't get repaired
properly. The stuck loop randomly finds a directory containing stuck chunks and
then will randomly add one stuck chunk to the heap. The randomness with which
the stuck loop finds stuck chunks is weighted by stuck chunks ie a directory
with more stuck chunks will be more likely to be chosen and a file with more
stuck chunks will be more likely to be chosen. Stuck chunks are priority in the
heap, so limiting it to `MaxStuckChunksInHeap` at a time prevents the heap from
being saturated with stuck chunks that potentially cannot be repaired which
would cause no other files to be repaired. If the repair of a stuck chunk is
successful, a signal is sent to the stuck loop and another stuck chunk is added
to the heap. If the repair wasn't successful, the stuck loop will wait for the
`repairStuckChunkInterval` to pass and then try another random stuck chunk. If
the stuck loop doesn't find any stuck chunks, it will sleep until a bubble
triggers it by finding a stuck chunk.

### Backup Subsystem
**Key Files**
 - [backup.go](./backup.go)
 - [backupsnapshot.go](./backupsnapshot.go)

**Key Constants**
 - uploadPollTimeout
 - uploadPollInterval
 - snapshotSyncSleepDuration

*TODO* 
  - expand subsystem description

The backup subsystem of the renter is responsible for creating local and remote
backups of the user's data, such that all data is able to be recovered onto a
new machine should the current machine + metadata be lost.
