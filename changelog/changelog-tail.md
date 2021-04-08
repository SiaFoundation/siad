## Apr 7, 2021:
### v1.5.6
**Key Updates**
- Add repair information to `FileInfo`
- Add checking for duplicates when updating the skynet blocklist.
- Remove the `wal` from the `.siadir`
- Add `bubbleScheduler` to the renter to manage the bubble update requests
- Create an `accounting` module for the Sia node to provide basic accounting information about the modules.
- Track EA spending through the account spending details to see what the money
  from an ephemeral account is being spent on.
- add license to monetization
- remove rhp1 and rhp2 renew rpcs that don't clear the old contract upon renewal
- Add parsing of module names to `siad -M` and automatically enable the
    `accounting`and `feemanager` modules if the `wallet` is enabled.
- add ability to specify monetizers when uploading a skyfile
- update contract size we consider large from 2TB to 10TB to allow for larger contracts before slowing down updates to them.
- prevent large contracts from renewing if legacy renewal RPCs are used. 

**Bugs Fixed**
+ Changed the minimum acceptable version of gateway peers to 1.5.4 for the Foundation hardfork
- Fixed panic condition in the repair loop for the unique refresh paths.

**Other**
- Update `.gitlab-ci.yml` and `Makefile` to fix Windows nightly tests.
- adds a new endpoint /host/contracts/*contractID* that returns a single contract from the host's database
- extend account persistence to include spending details fields
- Add `FundAccountSpending` to the financial metrics in the Renter.
- Add `MaintenanceSpending` to the financial metrics in the Renter.

## Feb 18, 2021:
### v1.5.5
**Key Updates**
- Add the ability to backup a skylink and restore it from disk
- Add Skynet stats to the `.siadir` metadata.
- Add repair information to `.siadir` metadata
- Add `stucksize` to the directory metadata to show the amount of data being handled by the stuck loop.
- Change http status code for blocked content from 500 to 451
- have host send an "ok" response when unsubscribing from a registry value
- include the public key of the signed entry in the subscription notification response
- Add `/renter/bubble` route to be able to manually trigger bubble updates
- siac breaks down memory consumption of the individual memory managers
- Improve download speeds and consistency.
- Add `siac skynet backup` and `siac skynet restore` commands for backing up and restoring skyfiles.
- Update default redundancy to be 10-30 for Skyfiles that exceed a single sector
  in size.

**Bugs Fixed**
- The 'siac skynet upload' '-s' flag has been removed to fix a collision with the new global '-s' flag.
- Introduce overflow file for sectors where the location counter has reached a value of >= 2^16 to fix uploads failing for all zero sectors

**Other**
- Add deletion of files that contain blocked skylinks in the bubble code.

## Jan 12, 2021:
### v1.5.4
**Key Updates**
- Adds `/skynet/root` GET request to allow downloading a specific sector of a skyfile
- allow for migrating registry to custom path
- extend `siac renter` with optional currency conversion based on
    SIA_EXCHANGE_RATE env variable
- Update health loop to batch bubbles by subtree instead of individual
    directories
- add RHP3 RPC for atomic contract renewal
- Add `/renter/clean` API route with `siac renter clean` to remove lost files
 from renter. Also added `siac renter lost` to view lost files that would be
 removed.

**Bugs Fixed**
- Fixed `uploadHead` panic related to streaming caused by bad condition check.
- Fixed debug code that checks for contract header corruption.
- Fixed bug in skyfile fanout encoding that allowed for encoding empty piece root hashes.
- Fix panic condition in siafile conversion when attempted with encryption.
- Fixed bug in the skynet blocklist persist compat code where the compat code
    was not being triggered.

**Other**
- Add worker groups to the bubble code
- Add `Portals` to the `TestGroup` for skynet related testing.
- Add 'no-response-metadata' query string parameter that allows hiding the
  'Skynet-File-Metadata' header from the response.
- Added a job to `.gitlab-ci.yml` to trigger Sia Antfarm version tests on Sia
  master updates and on Sia nightly executions.
- Add `siac skynet isblocked` command as a helper to check if a skylink is
blocked since `siac skynet blocklist` returns a list of hashed merkleroots, so
the list cannot be visually verified.

## Nov 10, 2020:
### v1.5.3
**Bugs Fixed**
- Updated siafile snapshots to only store range of chunks needed for repair to
 address OOM during large file repairs

## Nov 9, 2020:
### v1.5.2
**Key Updates**
- RHP3 Renewal RPC now only uses the Host's price table

**Bugs Fixed**
- Add missing error check in registry creation

**Other**
- Registry lookup to always return 404 even for timeouts

## Nov 2, 2020:
### v1.5.1
**Key Updates**
- Add basic watchdog to the feemanager
- Add `/skynet/basesector` to the API.
- Add `--portal` flag to `siac skynet pin`
- Add the ability to start and stop the profile via the API
- Add Skykey flags to convert command for encryption
- Enable adding or removing hashes of skylink merkleroots to the skynet
  blacklist
- allow for migrating registry to cstom path
- Update the upload streamer code to send chunks directly to the workers instead
  of through the `uploadHeap`.
- new acceptcontractadjustment in score breakdown
- reduced default period to 2 months
- extend /host [GET] to contain pricetable
- add API support for setting registry size
- move health summary from `siac renter -v` to `siac renter health`
- add timeout param to read registry api call
- add root flag to download endpoints
- add root flag to list downloads endpoint
- Add support for skykey delete in siac.
- Add support for uploading entire directories as skyfiles (e.g. `siac skynet
  upload dir skyfile_name`). The previous behavior of uploading all files
  individually is now available when the `--separately` flag is passed.
  Additional flags: `--defaultpath` and `--disabledefaultpath`. Those are full
  equivalents to the flags with the same names on the `/skynet/skyfile/*siapath*
  [POST]` endpoint.
- Added feature to use pipes with 'siac skynet upload'. e.g. 
  'dd if=/dev/zero bs=1M count=1000 | siac skynet upload 1GB.dat'

**Bugs Fixed**
- Fix unit of `EphemeralAccountExpiry` in the host persistence.
- Fix bug in append only persist code that left a file handle open.
- Ensure that only full paths are accepted when resolving skylinks.
- Properly handle URL-encoded characters in `GET /skynet/skylink` route.
 - Fix bug in Filesystem list that duplicated directories returned for recursive
     calls
- Fixed edge case with the health loop where it would not find the correct
  directory to call bubble on due to the metadatas being out of sync from
  a shutdown with pending bubbles.
- Fix skykey default type in siac

**Other**
- Split out `siac renter workers` download and upload info
- Rename `skynetblacklist` to `skynetblocklist`
- Add ETag response header
- Fix setting `GORACE` for `test-vlong` in `Makefile` for Windows Gitlab
  runner.
- Update the `README` to remove the outdated raspberry pi info and link the
  official release locations.
- Add `Skynet-Skylink` response headers.
- Add the ability to parse base32 encoded Skylinks

## Aug 5, 2020:
### v1.5.0
**Key Updates**
- Add `zip` download format and set it as default format.
- add support for write MDM programs to host
- Added `defaultpath` - a new optional path parameter when creating Skylinks. It
  determines which is the default file to open in a multi-file skyfile.
- Add `configModules` to the API so that the siad modules can be return in
  `/daemon/settings [GET]`
- Allow the renew window to be larger than the period
- Convert skynetblacklist from merkleroots to hashes of the merkleroots
- split up the custom http status code returned by the API for unloaded modules
  into 2 distinct codes.
- Add `daemon/stack` endpoint to get the current stack trace.
- Add Skykey delete methods to API.
- Add `disabledefaultpath` - a new optional path parameter when creating
  Skylinks. It disables the default path functionality, guaranteeing that the
  user will not be automatically redirected to `/index.html` if it exists in the
  skyfile.
- Add 'siac' commands for the FeeManager
- Add `TypePrivateID` Skykeys with skyfile encryption support
- Added available and priority memory output to `siac renter -v`

**Bugs Fixed**
- Set 'Content-Disposition' header for archives.
- fixed bug in rotation of fingerprint buckets
- fix issue where priority tasks could wait for low priority tasks to complete
- Fix panic in backup code due to not using `newJobGeneric`
- Skynet filenames are now validated when uploading. Previously you could upload
  files called e.g. "../foo" which would be inaccessible.
- The Skykey encryption API docs were updated to fix some discrepancies. In
  particular, the skykeyid section was removed.
- The createskykey endpoint was fixed as it was not returning the full Skykey
  that was created.
- integrade download cooldown system into download jobs
- fix bug which could prevent downloads from making progress
- Fix panic in the wal of the siadir and siafile if a delete update was
  submitted as the last update in a set of updates.

**Other**
- Add `EphemeralAccountExpiry` and `MaxEphemeralAccountBalance` to the Host's
  ExternalSettings
- Add testing infrastructure to validate the output of siac commands.
- Add root siac Cobra command test with subtests.
- Optimise writes when we execute an MDM program on the host to lower overall
  (upload) bandwidth consumption.
- Change status returned when module is not loaded from 404 to 490
- Add `siac renter workers ea` command to siac
- Add `siac renter workers pt` command to siac
- Add `siac renter workers rj` command to siac
- Add `siac renter workers hsj` command to siac
- Add testing for blacklisting skylinks associated with siafile conversions
- Rename `Gateway` `blacklist` to `blocklist`
- Allow host netAddress and announcements with local network IP on dev builds.
- Add default timeouts to opening a stream on the mux
- Update to bolt version with upstream fixes. This enables builds with Go 1.14.

## Jun 5, 2020:
### v1.4.11
**Bugs Fixed**
- Fixed bug where a Sia dir could be created with the same path as an already
existing Sia dir and no error was returned.
- Fixed bug that prevented downloading from old hosts

**Other**
- persist/log.go has been extracted and is now a simple wrapper around the new
log repo.
- Use external changelog generator v1.0.1.

## Jun 3, 2020:
### v1.4.10
**Bugs Fixed**
- fixed issue where workers would freeze for a bit after a new block appeared

**Other**
- Add Skykey Name and ID to skykey GET responses

## May 29, 2020:
### v1.4.9
**Key Updates**
- Add `FeeManager` to siad to allow for applications to charge a fee
- Add start time for the API server for siad uptime
- Add new `/consensus/subscribe/:id` endpoint to allow subscribing to consensus
  change events
- Add /skykeys endpoint and `siac skykey ls` command
- Updated skykey encoding and format

**Bugs Fixed**
- fix call to expensive operation in tight loop
- fix an infinite loop which would block uploads from progressing

**Other**
- Optimize bandwidth consumption for RPC write calls
- Extend `/daemon/alerts` with `criticalalerts`, `erroralerts` and
  `warningalerts` fields along with `alerts`.
- Update skykey siac functions to accept httpClient and remove global httpClient
  reference from siac testing
- Skykeycmd test broken down to subtests.
- Create siac testing helpers.
- Add engineering guidelines to /doc
- Introduce PaymentProvider interface on the renter.
- Skynet persistence subsystems into shared system.
- Update Cobra from v0.0.5 to v1.0.0.

## May 11, 2020:
### v1.4.8
**Key Updates**
- Enable FundEphemeralAccountRPC on the host
- Enable UpdatePriceTableRPC on the host
- Add `startheight` and `endheight` flags for `siac wallet transactions`
  pagination
- Add progress bars to Skynet uploads. Those can be disabled by passing the
  `--silent` flag.
- Add the Execute Program RPC to the host
- Added Skykey API endpoints and siac commands.
- Add /skynet/portals API endpoints.
- Add MinBaseRPCPrice and MinSectorAccessPrice to `siac host -v`
- Add `basePriceAdjustment` to the host score to check for `BaseRPCPrice` and
  `SectorAccessPrice` price violations.
- Add support for unpinning directories from Skynet.
- Add support for unpinning multiple files in a single command.
- Change payment processing to always use ephemeral accounts, even for contract
  payments
- Increase renew alert severity in 2nd half of renew window
- Prioritize remote repairs
- Add SIAD_DATA_DIR environment variable which tells `siad` where to store the
  siad-specific data. This complements the SIA_DATA_DIR variable which tells
  `siad` where to store general Sia data, such as the API password,
  configuration, etc.
- Update the `siac renter` summaries to use the `root` flags for the API calls
- Add `root` flag to `renter/rename` so that all file in the filesystem can be
  renamed
- Allow for `wallet/verifypassword` endpoint to accept the primary seed as well
  as a password
- Add `/renter/workers` API endpoint to get the current status of the workers.
  This pulls it out of the log files as well. 
- Add `siac renter workers` command to siac
- Add valid and missed proof outputs to StorageObligation for `/host/contracts` 

**Bugs Fixed**
- Fix decode bug for the rpcResponse object
- Fix bug in rotation of fingerprint buckets
- fix hostdb log being incorrectly named
- Refactor the environment variables into the `build` package to address bug
  where `siac` and `siad` could be using different API Passwords.
- Fix bug in converting siafile to skyfile and enable testing.
- Fixed bug in bubble code that would overwrite the `siadir` metadata with old
  metadata
- Fixed the output of `siac skynet ls` not counting subdirectories.
- Fix a bug in `parsePercentages` and added randomized testing
- Fixed bug where backups where not being repaired
- The `AggregateNumSubDirs` count was fixed as it was always 0. This is a piece
  of metadata keeping track of the number of all subdirs of a directory, counted
  recursively.
- Address missed locations of API error returns for handling of Modules not
  running
- add missing local ranges to IsLocal function
- workers now more consistently use the most recent contract
- improved performance logging in repair.log, especially in debug mode
- general upload performance improvements (minor)
- Fixed bug in `siac renter -v` where the health summary wasn't considering
  `OnDisk` when deciding if the file was recoverable
- Fix panic condition in Renter's `uploadheap` due to change in chunk's stuck
  status
- renewed contracts must be marked as not good for upload and not good for renew

**Other**
- Add 'AccountFunding' to the Host's financial metrics
- Support multiple changelog items in one changelog file.
- Add updating changelog tail to changelog generator.
- Generate 2 patch level and 1 minor level upcoming changelog directories.
- Fixed checking number of contracts in testContractInterrupted test.
- Move generate-changelog.sh script to changelog directory.
- Generate changelog from any file extension (.md is not needed)
- Fix permission issues for Windows runner, do not perform linting during
  Windows tests.
- Move filenames to ignore in changelog generator to `.changelogignore` file
- Created `Merge Request.md` to document the merge request standards and
  process.
- Remove backslash check in SiaPath validation, add `\` to list of accepted
  characters
- `siac skynet upload` with the `--dry-run` flag will now print more clear
  messages to emphasize that no files are actually uploaded.
- Move `scanCheckInterval` to be a build variable for the `hostdb`
- Skynet portals and blacklist persistence errors have been made more clear and
  now include the persist file locations.
- add some performance stats for upload and download speeds to /skynet/stats
- set the password and user agent automatically in client.New
- Publish test logs also for regular pipelines (not only for nightly pipelines).
- Setup Windows runner for nightly test executions.

## Apr 2, 2020:
### v1.4.7
**Key Updates**
- Split up contract files into a .header and .roots file. Causes contract
  insertion to be ACID and fixes a rare panic when loading the contractset.
- Add `--dry-run` parameter to Skynet upload
- Set ratio for `MinBaseRPCPrice` and `MinSectorAccessPrice` with
  `MinDownloadBandwidthPrice`

**Bugs Fixed**
- Don't delete hosts the renter has a contract with from hostdb
- Initiate a hostdb rescan on startup if a host the renter has a contract with
  isn't in the host tree
- Increase max host downtime in hostbd from 10 days to 20 days.
- Remove `build.Critical` and update to a metadata update

**Other**
- Add PaymentProcessor interface (host-side)
- Move golangci-lint to `make lint` and remove `make lint-all`.
- Add whitespace lint to catch extraneous whitespace and newlines.
- Expand `SiaPath` unit testing to address more edge cases.

## Mar 25, 2020:
### v1.4.6
**Bugs Fixed**
- Fix panic when metadata of skyfile upload exceeds modules.SectorSize
- Fix curl example for `/skynet/skyfile/` post

## Mar 24, 2020:
### v1.4.5
**Key Updates**
- Alerts returned by /daemon/alerts route are sorted by severity
- Add `--fee-included` parameter to `siac wallet send siacoins` that allows
   sending an exact wallet balance with the fees included.
- Extend `siac hostdb view` to include all the fields returned from the API.
- `siac renter delete` now accepts a list of files.
- add pause and resume uploads to siac
- Extended `siac renter` to include number of passive and disabled contracts
- Add contract data to `siac renter`
- Add getters and setter to `FileContract` and `FileContractRevision` types to
  prevent index-out-of-bounds panics after a `RenewAndClear`.

**Bugs Fixed**
- Fixed file health output of `siac renter -v` not adding to 100% by adding
  parsePercentage function.
- Fix `unlock of unlocked mutex` panic in the download destination writer.
- Fix potential channel double closed panic in DownloadByRootProject 
- Fix divide by zero panic in `renterFileHealthSummary` for `siac renter -v`
- Fix negative currency panic in `siac renter contracts view`

**Other**
- Add timeout parameter to Skylink pin route
- Also apply timeout when fetching the individual chunks
- Add SiaMux stream handler to the host
- Fix TestAccountExpiry NDF
- Add benchmark test for bubble metadata
- Add additional format instructions to the API docs and fix format errors
- Created Minor Merge Request template.
- Updated `Resources.md` with links to filled out README files
- Add version information to the stats endpoint
- Extract environment variables to constants and add to API docs.

## Mar 17, 2020:
### v1.4.4
**Key Updates**
- Add a delay when modifying large contracts on hosts to prevent hosts from
  becoming unresponsive due to massive disk i/o.
- Add `--root` parameter to `siac renter delete` that allows passing absolute
  instead of relative file paths.
- Add ability to blacklist skylinks by merkleroot.
- Uploading resumes more quickly after restart.
- Add `HEAD` request for skylink
- Add ability to pack many files into the same or adjacent sectors while
  producing unique skylinks for each file.
- Fix default expected upload/download values displaying 0 when setting an
  initial allowance.
- `siac skynet upload` now supports uploading directories. All files are
  uploaded individually and result in separate skylinks.
- No user-agent needed for Skylink downloads.
- Add `go get` command to `make dependencies`.
- Add flags for tag and targz for skyfile streaming.
- Add new endpoint `/skynet/stats` that provides statistical information about
  skynet, how many files were uploaded and the combined size of said files.
- The `siac renter setallowance` UX is considerably improved.
- Add XChaCha20 CipherKey.
- Add Skykey Manager.
- Add `siac skynet unpin` subcommand.
- Extend `siac renter -v` to show breakdown of file health.
- Add Skynet-Disable-Force header to allow disabling the force update feature
  on Skynet uploads
- Add bandwidth usage to `siac gateway`

**Bugs Fixed**
- Fixed bug in startup where an error being returned by the renter's blocking
  startup process was being missed
- Fix repair bug where unused hosts were not being properly updated for a
  siafile
- Fix threadgroup violation in the watchdog that allowed writing to the log
  file after a shutdown
- Fix bug where `siac renter -v` wasn't working due to the wrong flag being
  used.
- Fixed bug in siafile snapshot code where the `hostKey()` method was not used
  to safely acquire the host pubkey.
- Fixed `siac skynet ls` not working when files were passed as input. It is now
  able to access specific files in the Skynet folder.
- Fixed a deadlock when performing a Skynet download with no workers
- Fix a parsing bug for malformed skylinks
- fix siac update for new release verification
- Fix parameter delimiter for skylinks
- Fixed race condition in host's `RPCLoopLock`
- Fixed a bug which caused a call to `build.Critical` in the case that a
  contract in the renew set was marked `!GoodForRenew` while the contractor
  lock was not held

**Other**
- Split out renter siatests into 2 groups for faster pipelines.
- Add README to the `siatest` package 
- Bump golangci-lint version to v1.23.8
- Add timeout parameter to Skylink route - Add `go get` command to `make
  dependencies`.
- Update repair loop to use `uniqueRefreshPaths` to reduce unnecessary bubble
  calls
- Add Skynet-Disable-Force header to allow disabling the force update feature
  on Skynet uploads
- Create generator for Changelog to improve changelog update process

## Feb 2020:
### v1.4.3
**Key Updates**
- Introduced Skynet with initial feature set for portals, web portals, skyfiles,
  skylinks, uploads, downloads, and pinning
- Add `data-pieces` and `parity-pieces` flags to `siac renter upload`
- Integrate SiaMux
- Initialize defaults for the host's ephemeral account settings
- Add SIA_DATA_DIR environment variable for setting the data directory for
  siad/siac
- Made build process deterministic. Moved related scripts into `release-scripts`
- Add directory support to Skylinks.
- Enabled Lockcheck code anaylzer
- Added Bandwidth monitoring to the host module
 
**Bugs Fixed**
- HostDB Data race fixed and documentation updated to explain the data race
  concern
- `Name` and `Dir` methods of the Siapath used the `filepath` package when they
  should have used the `strings` package to avoid OS path separator bugs
- Fixed panic where the Host's contractmanager `AddSectorBatch` allowed for
  writing to a file after the contractmanager had shutdown
- Fixed panic where the watchdog would try to write to the contractor's log
  after the contractor had shutdown

**Other**
- Upgrade host metadata to v1.4.3
- Removed stubs from testing

## Jan 2020:
### v1.4.2.1
**Key Updates**
- Wallet can generate an address before it finishes scanning the blockchain
- FUSE folders can now be mounted with 'AllowOther' as an option
- Added alerts for when contracts can't be renewed or refreshed
- Smarter fund allocation when initially forming contracts
- Decrease memory usage and cpu usage when uploading and downloading
- When repairing files from disk, an integrity check is performed to ensure
  that corrupted / altered data is not used to perform repairs

**Bugs Fixed**
- Repair operations would sometimes perform useless and redundant repairs
- Siafiles were not pruning hosts correctly
- Unable to upload a new file if 'force' is set and no file exists to delete
- Siac would not always delete a file or folder correctly
- Divide by zero error when setting the allowance with an empty period
- Host would sometimes deadlock upon shutdown due to thread group misuse
- Crash preventing host from starting up correctly after an unclean shutdown
  while resizing a storage folder

## Dec 2019:
### v1.4.2.0
**Key Updates**
- Allowance in Backups
- Wallet Password Reset
- Bad Contract Utility Add
- FUSE
- Renter Watchdog
- Contract Churn Limiter
- Serving Downloads from Disk
- Verify Wallet Password Endpoint
- Siafilesystem
- Sia node scanner
- Gateway blacklisting
- Contract Extortion Checker
- Instant Boot
- Alert System
- Remove siafile chunks from memory
- Additional price change protection for the Renter
- siac Alerts command
- Critical alerts displayed on every siac call
- Single File Get in siac
- Gateway bandwidth monitoring
- Ability to pause uploads/repairs

**Bugs Fixed**
- Missing return statements in API (http: superfluous response.WriteHeader call)
- Stuck Loop fixes (chunks not being added due to directory siapath never being set)
- Rapid Cycle repair loop on start up
- Wallet Init with force flag when no wallet exists previous would error

**Other**
- Module READMEs
- staticcheck and gosec added
- Security.md file created
- Community images added for Built On Sia
- JSON tag code analyzer 
- ResponseWriter code analyzer
- boltdb added to gitlab.com/NebulousLabs

Sep 2019:

v1.4.1.2 (hotfix)
- Fix memory leak
- Add /tpool/transactions endpoint
- Second fix to transaction propagation bug

Aug 2019:

v1.4.1.1 (hotfix)
- Fix download corruption bug
- Fix transaction propagation bug

Jul 2019:

v1.4.1 (minor release)
- Support upload streaming
- Enable seed-based snapshot backups

Apr 2019:

v1.4.0 (minor release)
- Support "snapshot" backups
- Switch to new renter-host protocol
- Further scalability improvements

Oct 2018:

v1.3.7 (patch release)
- Adjust difficulty for ASIC hardfork

v1.3.6 (patch release)
- Enable ASIC hardfork

v1.3.5 (patch release)
- Add offline signing functionality
- Overhaul hostdb weighting
- Add siac utils

Sep 2018:

v1.3.4 (patch release)
- Fix contract spending metrics
- Add /renter/contract/cancel endpoint
- Move project to GitLab

May 2018:

v1.3.3 (patch release)
- Add Streaming API endpoints
- Faster contract formation
- Improved wallet scaling

March 2018:

v1.3.2 (patch release)
- Improve renter throughput and stability
- Reduce host I/O when idle
- Add /tpool/confirmed endpoint

December 2017:

v1.3.1 (patch release)
- Add new efficient, reliable contract format
- Faster and smoother file repairs
- Fix difficulty adjustment hardfork

July 2017:

v1.3.0 (minor release)
- Add remote file repair
- Add wallet 'lookahead'
- Introduce difficulty hardfork

May 2017:

v1.2.2 (patch release)
- Faster + smaller wallet database
- Gracefully handle missing storage folders
- >2500 lines of new testing + bug fixes

April 2017:

v1.2.1 (patch release)
- Faster host upgrading
- Fix wallet bugs
- Add siac command to cancel allowance

v1.2.0 (minor release)
- Host overhaul
- Wallet overhaul
- Tons of bug fixes and efficiency improvements

March 2017:

v1.1.2 (patch release)
- Add async download endpoint
- Fix host storage proof bug

February 2017:

v1.1.1 (patch release)
- Renter now performs much better at scale
- Myriad HostDB improvements
- Add siac command to support storage leaderboard

January 2017:

v1.1.0 (minor release)
- Greatly improved upload/download speeds
- Wallet now regularly "defragments"
- Better contract metrics

December 2016:

v1.0.4 (LTS release)

October 2016:

v1.0.3 (patch release)
- Greatly improved renter stability
- Smarter HostDB
- Numerous minor bug fixes

July 2016:

v1.0.1 (patch release)
- Restricted API address to localhost
- Fixed renter/host desynchronization
- Fixed host silently refusing new contracts

June 2016:

v1.0.0 (major release)
- Finalized API routes
- Add optional API authentication
- Improve automatic contract management

May 2016:

v0.6.0 (minor release)
- Switched to long-form renter contracts
- Added support for multiple hosting folders
- Hosts are now identified by their public key

January 2016:

v0.5.2 (patch release)
- Faster initial blockchain download
- Introduced headers-only broadcasting

v0.5.1 (patch release)
- Fixed bug severely impacting performance
- Restored (but deprecated) some siac commands
- Added modules flag, allowing modules to be disabled

v0.5.0 (minor release)
- Major API changes to most modules
- Automatic contract renewal
- Data on inactive hosts is reuploaded
- Support for folder structure
- Smarter host

October 2015:

v0.4.8 (patch release)
- Restored compatibility with v0.4.6

v0.4.7 (patch release)
- Dropped support for v0.3.3.x

v0.4.6 (patch release)
- Removed over-aggressive consistency check

v0.4.5 (patch release)
- Fixed last prominent bug in block database
- Closed some dangling resource handles

v0.4.4 (patch release)
- Uploading is much more reliable
- Price estimations are more accurate
- Bumped filesize limit to 20 GB

v0.4.3 (patch release)
- Block database is now faster and more stable
- Wallet no longer freezes when unlocked during IBD
- Optimized block encoding/decoding

September 2015:

v0.4.2 (patch release)
- HostDB is now smarter
- Tweaked renter contract creation

v0.4.1 (patch release)
- Added support for loading v0.3.3.x wallets
- Better pruning of dead nodes
- Improve database consistency

August 2015:

v0.4.0: Second stable currency release.
- Wallets are encrypted and generated from seed phrases
- Files are erasure-coded and transferred in parallel
- The blockchain is now fully on-disk
- Added UPnP support

June 2015:

v0.3.3.3 (patch release)
- Host announcements can be "forced"
- Wallets can be merged
- Unresponsive addresses are pruned from the node list

v0.3.3.2 (patch release)
- Siafunds can be loaded and sent
- Added block explorer
- Patched two critical security vulnerabilities

v0.3.3.1 (hotfix)
- Mining API sends headers instead of entire blocks
- Slashed default hosting price

v0.3.3: First stable currency release.
- Set release target
- Added progress bars to uploads
- Rigorous testing of consensus code

May 2015:

v0.3.2: Fourth open beta release.
- Switched encryption from block cipher to stream cipher
- Updates are now signed
- Added API calls to support external miners

v0.3.1: Third open beta release.
- Blocks are now stored on-disk in a database
- Files can be shared via .sia files or ASCII-encoded data
- RPCs are now multiplexed over one physical connection

March 2015:

v0.3.0: Second open beta release.

Jan 2015:

v0.2.0: First open beta release.

Dec 2014:

v0.1.0: Closed beta release.
