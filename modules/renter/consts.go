package renter

import (
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
)

// Version and system parameters.
const (
	// persistVersion defines the Sia version that the persistence was
	// last updated
	persistVersion = "1.4.0"
)

// Default redundancy parameters.
var (
	// defaultDataPieces is the number of data pieces per erasure-coded chunk
	defaultDataPieces = build.Select(build.Var{
		Dev:      1,
		Standard: 10,
		Testing:  1,
	}).(int)

	// defaultParityPieces is the number of parity pieces per erasure-coded
	// chunk
	defaultParityPieces = build.Select(build.Var{
		Dev:      1,
		Standard: 20,
		Testing:  8,
	}).(int)
)

// Default memory usage parameters.
var (
	// defaultMemory establishes the default amount of memory that the renter
	// will use when performing uploads and downloads. The mapping is currently
	// not perfect due to GC overhead and other places where we don't count all
	// of the memory usage accurately.
	defaultMemory = build.Select(build.Var{
		Dev:      uint64(1 << 28),     // 256 MiB
		Standard: uint64(3 * 1 << 28), // 768 MiB
		Testing:  uint64(1 << 17),     // 128 KiB - 4 KiB sector size, need to test memory exhaustion
	}).(uint64)

	// initialStreamerCacheSize defines the cache size that each streamer will
	// start using when it is created. A lower initial cache size will mean that
	// it will take more requests / round trips for the cache to grow, however
	// the cache size gets set to at least 2x the minimum read size initially
	// anyway, which means any application doing very large reads is going to
	// automatically have the cache size stepped up without having to do manual
	// growth.
	initialStreamerCacheSize = build.Select(build.Var{
		Dev:      int64(1 << 13), // 8 KiB
		Standard: int64(1 << 19), // 512 KiB
		Testing:  int64(1 << 10), // 1 KiB
	}).(int64)

	// maxStreamerCacheSize defines the maximum cache size that each streamer
	// will use before it no longer increases its own cache size. The value has
	// been set fairly low because some applications like mpv will request very
	// large buffer sizes, taking as much data as fast as they can. This results
	// in the cache size on Sia's end growing to match the size of the
	// requesting application's buffer, and harms seek times. Maintaining a low
	// maximum ensures that runaway growth is kept under at least a bit of
	// control.
	//
	// This would be best resolved by knowing the actual bitrate of the data
	// being fed to the user instead of trying to guess a bitrate, however as of
	// time of writing we don't have an easy way to get that information.
	maxStreamerCacheSize = build.Select(build.Var{
		Dev:      int64(1 << 20), // 1 MiB
		Standard: int64(1 << 25), // 32 MiB
		Testing:  int64(1 << 13), // 8 KiB
	}).(int64)
)

// Default bandwidth usage parameters.
const (
	// DefaultMaxDownloadSpeed is set to zero to indicate no limit, the user
	// can set a custom MaxDownloadSpeed through the API
	DefaultMaxDownloadSpeed = 0

	// DefaultMaxUploadSpeed is set to zero to indicate no limit, the user
	// can set a custom MaxUploadSpeed through the API
	DefaultMaxUploadSpeed = 0
)

// Naming conventions for code readability.
const (
	// destinationTypeSeekStream is the destination type used for downloads
	// from the /renter/stream endpoint.
	destinationTypeSeekStream = "httpseekstream"

	// memoryPriorityLow is used to request low priority memory
	memoryPriorityLow = false

	// memoryPriorityHigh is used to request high priority memory
	memoryPriorityHigh = true
)

// Constants that tune the health and repair processes.
const (
	// maxStuckChunksInHeap is the maximum number of stuck chunks that the
	// repair code will add to the heap at a time
	maxStuckChunksInHeap = 5
)

var (
	// healthCheckInterval defines the maximum amount of time that should pass
	// in between checking the health of a file or directory.
	healthCheckInterval = build.Select(build.Var{
		Dev:      15 * time.Minute,
		Standard: 1 * time.Hour,
		Testing:  5 * time.Second,
	}).(time.Duration)

	// healthLoopErrorSleepDuration indicates how long the health loop should
	// sleep before retrying if there is an error preventing progress.
	healthLoopErrorSleepDuration = build.Select(build.Var{
		Dev:      10 * time.Second,
		Standard: 30 * time.Second,
		Testing:  3 * time.Second,
	}).(time.Duration)

	// minUploadHeapSize is the minimum number of chunks we want in the upload
	// heap before trying to add more in order to maintain back pressure on the
	// workers, repairs, and uploads
	minUploadHeapSize = build.Select(build.Var{
		Dev:      5,
		Standard: 20,
		Testing:  1,
	}).(int)

	// offlineCheckFrequency is how long the renter will wait to check the
	// online status if it is offline.
	offlineCheckFrequency = build.Select(build.Var{
		Dev:      3 * time.Second,
		Standard: 10 * time.Second,
		Testing:  250 * time.Millisecond,
	}).(time.Duration)

	// repairStuckChunkInterval defines how long the renter sleeps between
	// trying to repair a stuck chunk. The uploadHeap prioritizes stuck chunks
	// so this interval is to allow time for unstuck chunks to be repaired.
	// Ideally the uploadHeap is spending 95% of its time repairing unstuck
	// chunks.
	repairStuckChunkInterval = build.Select(build.Var{
		Dev:      90 * time.Second,
		Standard: 10 * time.Minute,
		Testing:  5 * time.Second,
	}).(time.Duration)

	// uploadAndRepairErrorSleepDuration indicates how long a repair process
	// should sleep before retrying if there is an error fetching the metadata
	// of the root directory of the renter's filesystem.
	uploadAndRepairErrorSleepDuration = build.Select(build.Var{
		Dev:      20 * time.Second,
		Standard: 15 * time.Minute,
		Testing:  3 * time.Second,
	}).(time.Duration)

	// snapshotSyncSleepDuration defines how long the return sleeps between
	// trying to synchronize snapshots across hosts.
	snapshotSyncSleepDuration = build.Select(build.Var{
		Dev:      10 * time.Second,
		Standard: 5 * time.Minute,
		Testing:  5 * time.Second,
	}).(time.Duration)
)

// Constants that tune the worker swarm.
var (
	// downloadFailureCooldown defines how long to wait for a worker after a
	// worker has experienced a download failure.
	downloadFailureCooldown = time.Second * 3

	// maxConsecutivePenalty determines how many times the timeout/cooldown for
	// being a bad host can be doubled before a maximum cooldown is reached.
	maxConsecutivePenalty = build.Select(build.Var{
		Dev:      4,
		Standard: 10,
		Testing:  3,
	}).(int)

	// uploadFailureCooldown is how long a worker will wait initially if an
	// upload fails. This number is prime to increase the chance to avoid
	// intersecting with regularly occurring events which may cause failures.
	uploadFailureCooldown = build.Select(build.Var{
		Dev:      time.Second * 7,
		Standard: time.Second * 61,
		Testing:  time.Second,
	}).(time.Duration)

	// workerPoolUpdateTimeout is the amount of time that can pass before the
	// worker pool should be updated.
	workerPoolUpdateTimeout = build.Select(build.Var{
		Dev:      30 * time.Second,
		Standard: 5 * time.Minute,
		Testing:  3 * time.Second,
	}).(time.Duration)
)

// Constants which don't fit into another category very well.
const (
	// defaultFilePerm defines the default permissions used for a new file if no
	// permissions are supplied.
	defaultFilePerm = 0666

	// PriceEstimationSafetyFactor is the factor of safety used in the price
	// estimation to account for any missed costs
	PriceEstimationSafetyFactor = 1.2
)

// Deprecated consts.
//
// TODO: Tear out all related code and drop these consts.
const (
	// DefaultStreamCacheSize is the default cache size of the /renter/stream cache in
	// chunks, the user can set a custom cache size through the API
	DefaultStreamCacheSize = 2
)
