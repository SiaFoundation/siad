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
 - [var](#var)
 - [version](#version)
 - [vlong](#vlong)

## Appdata
### Key Files
 - [appdata.go](./appdata.go)
 - [appdata_test.go](./appdata_test.go)

The Appdata subsystem is responsible for providing information about various sia
application data. This subsystem is used to interact with any environment
variables that are set by the user.

**Environment Variables**
 - `SIA_API_PASSWORD` is the siaAPIPassword environment variable that sets a
   custom API password
 - `SIA_DATA_DIR` is the siaDataDir environment variable that tells siad where
   to put the sia data
 - `SIA_WALLET_PASSWORD` is the siaWalletPassword environment variable that can
   enable auto unlocking the wallet
 - `SKYNET_DATA_DIR` is the skynetDataDir environment variable that tells siad
   where to put the miscellaneous skynet data

## Commit
TODO...

## Critical
TODO...

## Debug
TODO...

## Errors
TODO...

## Release
TODO...

## Testing
TODO...

## Var
TODO...

## Version
TODO...

## VLong
TODO...
