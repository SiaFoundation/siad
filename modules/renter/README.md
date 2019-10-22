# Renter
The Renter is responsible for tracking and actively maintaining all of the files
that a user has uploaded to Sia. This includes the location and health of these
files. The Renter, via the HostDB and the Contractor, is also responsible for
picking hosts and maintaining the relationship with them.

The renter is unique for having two different logs. The first is a general
renter activity log, and the second is a repair log. The repair log is intended
to be a high-signal log that tells users what files are being repaired, and
whether the repair jobs have been successful. Where there are failures, the
repair log should try and document what those failures were. Every message of
the repair log should be interesting and useful to a power user, there should be
no logspam and no messages that would only make sense to siad developers.

## Submodules
The Renter has several submodules that each perform a specific function for the
Renter. This README will provide brief overviews of the submodules, but for more
detailed descriptions of the inner workings of the submodules the respective
README files should be reviewed.
 - Contractor
 - HostDB
 - Proto
 - SiaDir
 - SiaFile

### Contractor
The Contractor manages the Renter's contracts and is responsible for all
contract actions such as new contract formation and contract renewals. The
Contractor determines which contracts are GoodForUpload and GoodForRenew and
marks them accordingly.

### HostDB
The HostDB curates and manages a list of hosts that may be useful for the renter
in storing various types of data. The HostDB is responsible for scoring and
sorting the hosts so that when hosts are needed for contracts high quality hosts
are provided. 

### Proto
The proto module implements the renter's half of the renter-host protocol,
including contract formation and renewal RPCs, uploading and downloading,
verifying Merkle proofs, and synchronizing revision states. It is a low-level
module whose functionality is largely wrapped by the Contractor.

### SiaDir
The SiaDir module is the code that defines what a directory is on the Sia
network. It also manages accesses and updates to the file, ensuring safety and
ACIDity when performing file operations.

### SiaFile
The SiaFile module is the code that defines what a file is on the Sia network.
It also manages accesses and updates to the file, ensuring safety and ACIDity
when performing file operations.

## Subsystems
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

*TODO* 
  - fill out subsystem explanation

#### Outbound Complexities
 - `DeleteFile` calls `callThreadedBubbleMetadata` after the file is deleted
 - `RenameFile` calls `callThreadedBubbleMetadata` on the current and new
   directories when a file is renamed

### Persistance Subsystem
**Key Files**
 - [persist_compat.go](./persist_compat.go)
 - [persist.go](./persist.go)

*TODO* 
  - fill out subsystem explanation

### Memory Subsystem
**Key Files**
 - [memory.go](./memory.go)

*TODO* 
  - fill out subsystem explanation

### Worker Subsystem
**Key Files**
 - [worker.go](./worker.go)
 - [workerdownload.go](./workerdownload.go)
 - [workerfetchbackups.go](./workerfetchbackups.go)
 - [workerpool.go](./workerpool.go)
 - [workerupload.go](./workerupload.go)

The worker subsystem is the interface between the renter and the hosts. All
actions (with the exception of some legacy actions that are currently being
updated) that involve working with hosts will pass through the worker subsystem.

#### The Worker Pool

The heart of the worker subsystem is the worker pool, implemented in
[workerpool.go](./workerpool.go). The worker pool contains the set of workers
that can be used to communicate with the hosts, one worker per host. The
function `callWorker` can be used to retrieve a specific worker from the pool,
and the function `callUpdate` can be used to update the set of workers in the
worker pool. `callUpdate` will create new workers for any new contracts, will
update workers for any contracts that changed, and will kill workers for any
contracts that are no longer useful.

##### Inbound Complexities

 - `callUpdate` should be called on the worker pool any time that that the set
   of contracts changes or has updates which would impact what actions a worker
   can take. For example, if a contract's utility changes or if a contract is
   cancelled.
   - `Renter.SetSettings` calls `callUpdate` after changing the settings of the
	 renter. This is probably incorrect, as the actual contract set is updated
	 by the contractor asynchronously, and really `callUpdate` should be
	 triggered by the contractor as the set of hosts is changed.
   - `Renter.threadedDownloadLoop` calls `callUpdate` on each iteration of the
	 outer download loop to ensure that it is always working with the most
	 recent set of hosts. If the contractor is updated to be able to call
	 `callUpdate` during maintenance, this call becomes unnecessary.
   - `Renter.managedRefreshHostsAndWorkers` calls `callUpdate` so that the
	 renter has the latest list of hosts when performing uploads.
	 `Renter.managedRefreshHostsAndWorkers` is itself called in many places,
	 which means there's substantial complexity between the upload subsystem and
	 the worker subsystem. This complexity can be eliminated by having the
	 contractor being responsible for updating the worker pool as it changes the
	 set of hosts, and also by having the worker pool store host map, which is
	 one of the key reasons `Renter.managedRefreshHostsAndWorkers` is called so
	 often - this function returns the set of hosts in addition to updating the
	 worker pool.
 - `callWorker` can be used to fetch a worker and queue work into the worker.
   The worker can be killed after `callWorker` has been called but before the
   returned worker has been used in any way.
   - `renter.BackupsOnHost` will use `callWorker` to retrieve a worker that can
	 be used to pull the backups off of a host.

#### The Worker

Each worker in the worker pool is responsible for managing communications with a
single host. The worker has an infinite loop where it checks for work, performs
any outstanding work, and then sleeps for a wake, kill, or shutdown signal. The
implementation for the worker is primarily in [worker.go](./worker.go).

Each type of work that the worker can perform has a queue. A unit of work is
called a job. External subsystems can use `callQueueX` to add a job to the
worker. External subsystems can only queue work with a worker, the worker makes
all of the decisions around when the work is actually performed. Internally, the
worker needs to remember to call `staticWake` after queuing a new job, otherwise
the primary work thread will potentially continue sleeping and ignoring the work
that has been queued.

When a worker wakes or otherwise begins the work loop, the worker will check for
each type of work in a specific order, therefore giving certain types of work
priority over other types of work. For example, downloads are given priority
over uploads. When the worker performs a piece of work, it will jump back to the
top of the loop, meaning that a continuous stream of higher priority work can
stall out all lower priority work.

When a worker is killed, the worker is responsible for going through the list of
jobs that have been queued and gracefully terminating the jobs, returning or
signaling errors where appropriate.

[workerfetchbackups.go](./workerfetchbackups.go) is a good starting point to see
how a simple job is implemented.

The worker currently supports queueing these jobs:
 - Downloading a chunk [workerdownload.go](./workerdownload.go)
 - Fetching a list of backups stored on a host
   [workerfetchbackups.go](./workerfetchbackups.go)
 - Uploading a chunk [workerupload.go](./workerupload.go)

##### Inbound Complexities
 - `callQueueDownloadChunk` can be used to schedule a job to participate in a
   chunk download
   - `Renter.managedDistributeDownloadChunkToWorkers` will use this method to
	 issue a brand new download project to all of the workers.
   - `unfinishedDownloadChunk.managedCleanUp` will use this method to re-issue
	 work to workers that are known to have passed on a job previously, but may
	 be required now.
 - `callQueueFetchBackupsJob` can be used to schedule a job to retrieve a list
   of backups from a host
   - `Renter.BackupsOnHost` will use this method to fetch a list of snapshot
	 backups that are stored on a particular host.
 - `callQueueUploadChunk` can be used to schedule a job to participate in a
   chunk upload
   - `Renter.managedDistributeChunkToWorkers` will use this method to distribute
	 a brand new upload project to all of the workers.
   - `unfinishedUploadChunk.managedNotifyStandbyWorkers` will use this method to
	 re-issue work to workers that are known to have passed on a job previously,
	 but may be required now.

##### Outbound Complexities
 - `managedPerformFetchBackupsJob` will use `Renter.callDownloadSnapshotTable`
   to fetch the list of backups from the host. The snapshot subsystem is
   responsible for defining what the list of backups looks like and how to fetch
   those backups from the host.
 - `managedPerformDownloadChunkJob` is a mess of complexities and needs to be
   refactored to be compliant with the new subsystem format.
 - `managedPerformUploadChunkJob` is a mess of complexities and needs to be
   refactored to be compliant with the new subsystem format.

### Download Subsystem
**Key Files**
 - [download.go](./download.go)
 - [downloadchunk.go](./downloadchunk.go)
 - [downloaddestination.go](./downloaddestination.go)
 - [downloadheap.go](./downloadheap.go)
 - [workerdownload.go](./workerdownload.go)

*TODO* 
  - expand subsystem description

The download code follows a clean/intuitive flow for getting super high and
computationally efficient parallelism on downloads. When a download is
requested, it gets split into its respective chunks (which are downloaded
individually) and then put into the download heap and download history as a
struct of type `download`.

A `download` contains the shared state of a download with all the information
required for workers to complete it, additional information useful to users
and completion functions which are executed upon download completion.

The download history contains a mapping of all of the downloads' UIDs, which
are randomly assigned upon initialization to their corresponding `download`
struct. Unless cleared, users can retrieve information about ongoing and
completed downloads by either retrieving the full history or a specific
download from the history using the API.

The primary purpose of the download heap is to keep downloads on standby
until there is enough memory available to send the downloads off to the
workers. The heap is sorted first by priority, but then a few other criteria
as well.

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
 - [downloadstreamer.go](./downloadstreamer.go)

*TODO* 
  - fill out subsystem explanation

### Upload Subsystem
**Key Files**
 - [directoryheap.go](./directoryheap.go)
 - [upload.go](./upload.go)
 - [uploadheap.go](./uploadheap.go)
 - [uploadchunk.go](./uploadchunk.go)
 - [workerupload.go](./workerupload.go)

*TODO* 
  - expand subsystem description

The Renter uploads `siafiles` in 40MB chunks. Redundancy kept at the chunk level
which means each chunk will then be split in `datapieces` number of pieces. For
example, a 10/20 scheme would mean that each 40MB chunk will be split into 10
4MB pieces, which is turn will be uploaded to 30 different hosts (10 data pieces
and 20 parity pieces).

Chunks are uploaded by first distributing the chunk to the worker pool. The
chunk is distributed to the worker pool by adding it to the upload queue and
then signalling the worker upload channel. Workers that are waiting for work
will receive this channel and begin the upload. First the worker creates a
connection with the host by creating an `editor`. Next the `editor` is used to
update the file contract with the next data being uploaded. This will update the
merkle root and the contract revision.

**Outbound Complexities**  
 - The upload subsystem calls `callThreadedBubbleMetadata` from the Health Loop
   to update the filesystem of the new upload
 - `Upload` calls `callBuildAndPushChunks` to add upload chunks to the
   `uploadHeap` and then signals the heap's `newUploads` channel so that the
   Repair Loop will work through the heap and upload the chunks

### Upload Streaming Subsystem
**Key Files**
 - [uploadstreamer.go](./uploadstreamer.go)

*TODO* 
  - fill out subsystem explanation

### Health and Repair Subsystem
**Key Files**
 - [metadata.go](./metadata.go)
 - [repair.go](./repair.go)
 - [stuckstack.go](./stuckstack.go)
 - [uploadheap.go](./uploadheap.go)

*TODO*
  - Update naming of bubble methods to updateAggregateMetadata, this will more
    closely match the file naming as well. Update the health loop description to
    match new naming
  - Move HealthLoop and related methods out of repair.go to health.go
  - Pull out repair code from  uploadheap.go so that uploadheap.go is only heap
    related code. Put in repair.go
  - Pull out stuck loop code from uploadheap.go and put in repair.go
  - Review naming of files associated with this subsystem
  - Create benchmark for health loop and add print outs to Health Loop section
  - Break out Health, Repair, and Stuck code into 3 distinct subsystems
  
There are 3 main functions that work together to make up Sia's file repair
mechanism, `threadedUpdateRenterHealth`, `threadedUploadAndRepairLoop`, and
`threadedStuckFileLoop`. These 3 functions will be referred to as the health
loop, the repair loop, and the stuck loop respectively.

The Health and Repair subsystem operates by scanning aggregate information kept
in each directory's metadata. An example of this metadata would be the aggregate
filesystem health. Each directory has a field `AggregateHealth` which represents
the worst aggregate health of any file or subdirectory in the directory. Because
the field is recursive, the `AggregateHealth` of the root directory represents
the worst health of any file in the entire filesystem. Health is defined as the
percent of redundancy missing, this means that a health of 0 is a full health
file.

`threadedUpdateRenterHealth` is responsible for keeping the aggregate
information up to date, while the other two loops use that information to decide
what upload and repair actions need to be performed.

#### Health Loops
The health loop is responsible for ensuring that the health of the renter's file
directory is updated periodically. Along with the health, the metadata for the
files and directories is also updated. 

One of the key directory metadata fields that the health loop uses is
`LastHealthCheckTime` and `AggregateLastHealthCheckTime`. `LastHealthCheckTime`
is the timestamp of when a directory or file last had its health re-calculated
during a bubble call. When determining which directory to start with when
updating the renter's file system, the health loop follows the path of oldest
`AggregateLastHealthCheckTime` to find the directory that is the most out of
date. To do this, the health loop uses `managedOldestHealthCheckTime`. This
method starts at the root level of the renter's file system and begins checking
the `AggregateLastHealthCheckTime` of the subdirectories. It then finds which
one is the oldest and moves into that subdirectory and continues the search.
Once it reaches a directory that either has no subdirectories or has an older
`AggregateLastHealthCheckTime` than any of the subdirectories, it returns that
timestamp and the SiaPath of the directory.

Once the health loop has found the most out of date directory, it calls
`managedBubbleMetadata`, to be referred to as bubble, on that directory. When a
directory is bubbled, the metadata information is recalculated and saved to disk
and then bubble is called on the parent directory until the top level directory
is reached. During this calculation, every file in the directory is opened,
modified, and fsync'd individually. See benchmark results:

*TODO* - add benchmark 

If during a bubble a file is found that meets the threshold health
for repair, then a signal is sent to the repair loop. If a stuck chunk is found
then a signal is sent to the stuck loop. Once the entire renter's directory has
been updated within the healthCheckInterval the health loop sleeps until the
time interval has passed.

Since we are updating the metadata on disk during the bubble calls we want to
ensure that only one bubble is being called on a directory at a time. We do this
through `managedPrepareBubble` and `managedCompleteBubbleUpdate`. The renter has
a `bubbleUpdates` field that tracks all the bubbles and the `bubbleStatus`.
Bubbles can either be active or pending. When bubble is called on a directory,
`managedPrepareBubble` will check to see if there are any active or pending
bubbles for the directory. If there are no bubbles being tracked for that
directory then an active bubble update is added to the renter for the directory
and the bubble is executed immediately. If there is a bubble currently being
tracked for the directory then the bubble status is set to pending and the
bubble is not executed immediately. Once a bubble is finished it will call
`managedCompleteBubbleUpdate` which will check the status of the bubble. If the
status is an active bubble then it is removed from the renter's tracking. If the
status was a pending bubble then the status is set to active and bubble is
called on the directory again. 

**Inbound Complexities**  
 - The Repair loop relies on Health Loop and `callThreadedBubbleMetadata` to
   keep the filesystem accurately updated in order to work through the file
   system in the correct order.
 - `DeleteFile` calls `callThreadedBubbleMetadata` after the file is deleted
 - `RenameFile` calls `callThreadedBubbleMetadata` on the current and new
   directories when a file is renamed
 - The upload subsystem calls `callThreadedBubbleMetadata` from the Health Loop
   to update the filesystem of the new upload

**Outbound Complexities**   
 - The Health Loop triggers the Repair Loop when unhealthy files are found. This
   is done by `managedPerformBubbleMetadata` signaling the
   `r.uploadHeap.repairNeeded` channel when it is at the root directory and the
   `AggregateHealth` is above the `RepairThreshold`.
 - The Health Loop triggers the Stuck Loop when stuck files are found. This is
   done by `managedPerformBubbleMetadata` signaling the
   `r.uploadHeap.stuckChunkFound` channel when it is at the root directory and
   `AggregateNumStuckChunks` is greater than zero.

#### Repair Loop
The repair loop is responsible for uploading new files to the renter and
repairing existing files. The heart of the repair loop is
`threadedUploadAndRepair`, a thread that continually checks for work, schedules
work, and then updates the filesystem when work is completed.

The renter tracks backups and siafiles separately, which essentially means the
renter has a backup filesystem and a siafile filesystem. As such, we need to
check both these filesystems separately with the repair loop. Since the backups
are in a different filesystem, the health loop does not check on the backups
which means that there are no outside triggers for the repair loop that a backup
wasn't uploaded successfully and needs to be repaired. Because of this we always
check for backup chunks first to ensure backups are succeeding. There is a size
limit on the heap to help check memory usage in check, so by adding backup
chunks to the heap first we ensure that we are never skipping over backup chunks
due to a full heap.

For the siafile filesystem the repair loop uses a directory heap to prioritize
which chunks to add. The directoryHeap is a max heap of directory elements
sorted by health. The directory heap is initialized by pushing an unexplored
root directory element. As directory elements are popped of the heap, they are
explored, which means the directory that was popped off the heap as unexplored
gets marked as explored and added back to the heap, while all the subdirectories
are added as unexplored. Each directory element contains the health information
of the directory it represents, both directory health and aggregate health. If a
directory is unexplored the aggregate health is considered, if the directory is
explored the directory health is consider in the sorting of the heap. This is to
allow us to navigate through the filesystem and follow the path of worse health
to find the most in need directories first. When the renter needs chunks to add
to the upload heap, directory elements are popped of the heap and chunks are
pulled from that directory to be added to the upload heap. If all the chunks
that need repairing are added to the upload heap then the directory element is
dropped. If not all the chunks that need repair are added, then the directory
element is added back to the directory heap with a health equal to the next
chunk that would have been added, thus re-prioritizing that directory in the
heap.

To build the upload heap for the siafile filesystem, the repair loop checks if
the file system is healthy by checking the top directory element in the
directory heap. If healthy and there are no chunks currently in the upload heap,
then the repair loop sleeps until it is triggered by a new upload or a repair is
needed. If the filesystem is in need of repair, chunks are added to the upload
heap by popping the directory off the directory heap and adding any chunks that
are a worse health than the next directory in the directory heap. This continues
until the `MaxUploadHeapChunks` is met. The repair loop will then repair those
chunks and call bubble on the directories that chunks were added from to keep
the file system updated. This will continue until the file system is healthy,
which means all files have a health less than the `RepairThreshold`.

When repairing chunks, the Renter will first try and repair the chunk from the
local file on disk. If the local file is not present, the Renter will download
the needed data from its contracts in order to perform the repair. In order for
a remote repair, ie repairing from data downloaded from the Renter's contracts,
to be successful the chunk must be at 1x redundancy or better. If a chunk is
below 1x redundancy and the local file is not present the chunk, and therefore
the file, is considered lost as there is no way to repair it. 

**Inbound Complexities**  
 - `Upload` adds chunks directly to the upload heap by calling
   `callBuildAndPushChunks`
 - Repair loop will sleep until work is needed meaning other threads will wake
   up the repair loop by calling the `repairNeeded` channel
 - There is always enough space in the heap, or the number of backup chunks is
   few enough that all the backup chunks are always added to the upload heap.
 - Stuck chunks get added directly to the upload heap and have priority over
   normal uploads and repairs
 - Streaming upload chunks are added directory to the upload heap and have the
   highest priority

**Outbound Complexities**  
 - The Repair loop relies on Health Loop and `callThreadedBubbleMetadata` to
   keep the filesystem accurately updated in order to work through the file
   system in the correct order.
 - The repair loop passes chunks on to the upload subsystem and expects that
   subsystem to handle the request 
 - `Upload` calls `callBuildAndPushChunks` to add upload chunks to the
   `uploadHeap` and then signals the heap's `newUploads` channel so that the
   Repair Loop will work through the heap and upload the chunks

#### Stuck Loop
File's are marked as `stuck` if the Renter is unable to fully upload the file.
While there are many reasons a file might not be fully uploaded, failed uploads
due to the Renter, ie the Renter shut down, will not cause the file to be marked
as `stuck`. The goal is to mark a chunk as stuck if it is independently unable
to be uploaded. Meaning, this chunk is unable to be repaired but other chunks
are able to be repaired. We mark a chunk as stuck so that the repair loop will
ignore it in the future and instead focus on chunks that are able to be
repaired.

The stuck loop is responsible for targeting chunks that didn't get repaired
properly. There are two methods for adding stuck chunks to the upload heap, the
first method is random selection and the second is using the `stuckStack`. On
start up the `stuckStack` is empty so the stuck loop begins using the random
selection method. Once the `stuckStack` begins to fill, the stuck loop will use
the `stuckStack` first before using the random method.

For the random selection one chunk is selected uniformly at random out of all of
the stuck chunks in the filesystem. The stuck loop does this by first selecting
a directory containing stuck chunks by calling `managedStuckDirectory`. Then
`managedBuildAndPushRandomChunk` is called to select a file with stuck chunks to
then add one stuck chunk from that file to the heap. The stuck loop repeats this
process of finding a stuck chunk until there are `MaxStuckChunksInHeap` stuck
chunks in the upload heap. Stuck chunks are priority in the heap, so limiting it
to `MaxStuckChunksInHeap` at a time prevents the heap from being saturated with
stuck chunks that potentially cannot be repaired which would cause no other
files to be repaired. 

For the stuck loop to begin using the `stuckStack` there needs to have been
successful stuck chunk repairs. If the repair of a stuck chunk is successful,
the SiaPath of the SiaFile it came from is added to the Renter's `stuckStack`
and a signal is sent to the stuck loop so that another stuck chunk can added to
the heap. The `stuckStack` tracks `maxSuccessfulStuckRepairFiles` number of
SiaFiles that have had stuck chunks successfully repaired in a LIFO stack. If
the LIFO stack already has `maxSuccessfulStuckRepairFiles` in it, when a new
SiaFile is pushed onto the stack the oldest SiaFile is dropped from the stack so
the new SiaFile can be added. Additionally, if SiaFile is being added that is
already being tracked, then the originally reference is removed and the SiaFile
is added to the top of the Stack. If there have been successful stuck chunk
repairs, the stuck loop will try and add additional stuck chunks from these
files first before trying to add a random stuck chunk. The idea being that since
all the chunks in a SiaFile have the same redundancy settings and were
presumably uploaded around the same time, if one chunk was able to be repaired,
the other chunks should be able to be repaired as well. Additionally, the reason
a LIFO stack is used is because the more recent a success was the higher
confidence we have for additional successes.

If the repair wasn't successful, the stuck loop will wait for the
`repairStuckChunkInterval` to pass and then try another random stuck chunk. If
the stuck loop doesn't find any stuck chunks, it will sleep until a bubble wakes
it up by finding a stuck chunk.

**Inbound Complexities**  
 - Chunk repair code signals the stuck loop when a stuck chunk is successfully
   repaired
 - Health loop signals the stuck loop when aggregateNumStuckChunks for the root
   directory is > 0

**State Complexities**  
 - The stuck loop and the repair loop use a number of the same methods when
   building `unfinishedUploadChunks` to add to the `uploadHeap`. These methods
   rely on the `repairTarget` to know if they should target stuck chunks or
   unstuck chunks 

**TODOs**  
 - once bubbling metadata has been updated to be more I/O efficient this code
   should be removed and we should call bubble when we clean up the upload chunk
   after a successful repair.

### Backup Subsystem
**Key Files**
 - [backup.go](./backup.go)
 - [backupsnapshot.go](./backupsnapshot.go)

*TODO* 
  - expand subsystem description

The backup subsystem of the renter is responsible for creating local and remote
backups of the user's data, such that all data is able to be recovered onto a
new machine should the current machine + metadata be lost.
