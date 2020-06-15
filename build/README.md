# Build
The build package contains high level helper functions.

## Subsystems
 - [appdata](#appdata)
 - [commit](#commit)
 - [critical](#critical)
 - [debug](#debug)
 - [errors](#errors)
 - [release](#release)
 - [testing](#testing)
 - [url](#url)
 - [var](#var)
 - [version](#version)
 - [vlong](#vlong)

## Appdata
### Key Files
 - [appdata.go](./appdata.go)
 - [appdata_test.go](./appdata_test.go)

The Appdata subsystem is responsible for providing information about various Sia
application data. This subsystem is used to interact with any environment
variables that are set by the user.

**Environment Variables**
 - `SIA_API_PASSWORD` is the siaAPIPassword environment variable that sets a
   custom API password
 - `SIA_DATA_DIR` siaDataDir is the environment variable that tells siad where 
    to put the general sia data, e.g. api password, configuration, logs, etc.
 - `SIAD_DATA_DIR` siadDataDir is the environment variable which tells siad 
    where to put the siad-specific data
 - `SIA_WALLET_PASSWORD` is the siaWalletPassword environment variable that can
   enable auto unlocking the wallet

## Build Flags
### Key Files
 - [debug_off.go](./debug_off.go)
 - [debug_on.go](./debug_on.go)
 - [release_dev.go](./release_dev.go)
 - [release_standard.go](./release_standard.go)
 - [release_testing.go](./release_testing.go)
 - [vlong_off.go](./vlong_off.go)
 - [vlong_on.go](./vlong_on.go)

TODO...

## Commit
TODO...

## Critical
TODO...

## Errors
TODO...

## Testing
TODO...

## URL
### Key Files
 - [url.go](./url.go)

The URL subsystem is responsible for providing information about Sia URLs that
are in use.

## Var
TODO...

## Version
TODO...
