# Renter
The Renter is responsible for tracking all of the files that a user has uploaded
to Sia, as well as the locations and health of these files.

## Submodules
The Renter has several submodules that each perform a specific function for the
Renter.
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
not enough and renewing or refreshing any current contracts as needed.

### HostDB
The HostDB mananges the hosts that have been announced for the Renter. The
HostDB is responsible for scoring and sorting the hosts so that when hosts are
needed for contracts high quality hosts are provided. Additionally the HostDB
scans the hosts for changes so that if a host changes its settings or goes
offline the Renter is notified and can have its Contractor adjust any contracts
accordingly.

### Proto
TODO

### SiaDir
The SiaDir package is the code that defines what a directory is on the Sia network.

### SiaFile
The SiaFile package is the code that defines what a file is on the Sia network.

### Worker
TODO
While the Worker is not currently a submodule, it plays a key role for the Renter.

## Functions of the Renter
The Renter has the following functions that it performs.
 - Downloads
 - Repairs / Uploads

### Downloads
TODO

### Repairs and Uploads
The following describes the work flow of how the Renter repairs files.

There are 3 main functions that work together to make up Sia's file repair
mechanism, threadedUpdateRenterHealth, threadedUploadLoop, and
threadedStuckFileLoop. These 3 functions will be referred to as the health loop,
the repair loop, and the stuck loop respectively.

The health loop is responsible for ensuring that the health of the renter's file
directory is updated periodically. The health information for a directory is
stored in the .siadir metadata file and is the worst values for any of the files
and sub directories. This is true for all directories which means the health of
top level directory of the renter is the health of the worst file in the renter.
For health and stuck health the worst value is the highest value, for timestamp
values the oldest timestamp is the worst value, and for aggregate values (ie
NumStuckChunks) it will be the sum of all the files and sub directories.  The
health loop keeps the renter file directory updated by following the path of
oldest LastHealthCheckTime and then calling threadedBubbleHealth, to be referred
to as bubble, on that directory. When a directory is bubbled, the health
information is recalculated and saved to disk and then bubble is called on the
parent directory until the top level directory is reached. If during a bubble a
file is found that meets the threshold health for repair, then a signal is sent
to the repair loop. If a stuck chunk is found then a signal is sent to the stuck
loop. Once the entire renter's directory has been updated within the
healthCheckInterval the health loop sleeps until the time interval has passed.

The repair loop is responsible for repairing the renter's files, this includes
uploads. The repair loop follows the path of worst health and then adds the
files from the directory with the worst health to the repair heap and begins
repairing. If no directories are unhealthy enough to require repair the repair
loop sleeps until a new upload triggers it to start or it is triggered by a
bubble finding a file that requires repair. While there are files to repair, the
repair loop will continue to work through the renter's directory finding the
worst health directories and adding them to the repair heap. The
rebuildChunkHeapInterval is used to make sure the repair heap doesn't get stuck
on repairing a set of chunks for too long. Once the rebuildChunkheapInterval
passes, the repair loop will continue in it's search for files that need repair.
As chunks are repaired, they will call bubble on their directory to ensure that
the renter directory gets updated.

The stuck loop is responsible for targeting chunks that didn't get repaired
properly. The stuck loop randomly finds a directory containing stuck chunks and
adds those to the repair heap. The repair heap will randomly add one stuck chunk
to the heap at a time. Stuck chunks are priority in the heap, so limiting it to
1 stuck chunk at a time prevents the heap from being saturated with stuck chunks
that potentially cannot be repaired which would cause no other files to be
repaired. If the repair of a stuck chunk is successful, a signal is sent to the
stuck loop and another stuck chunk is added to the heap. If the repair wasn't
successful, the stuck loop will wait for the repairStuckChunkInterval to pass
and then try another random stuck chunk. If the stuck loop doesn't find any
stuck chunks, it will sleep until a bubble triggers it by finding a stuck chunk.