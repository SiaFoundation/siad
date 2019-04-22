---
title: Sia API Documentation

language_tabs: # must be one of https://git.io/vQNgJ
  - go

toc_footers:
  - <a href='https://sia.tech'>The Official Sia Website
  - <a href='https://gitlab.com/NebulousLabs/Sia'>Sia on GitLab</a>

search: true
---

# Introduction

## Welcome to the Sia Storage Platform API!
> Example GET curl call 

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/wallet/transactions?startheight=1&endheight=250"
```

> Example POST curl call with data

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "amount=123&destination=abcd" "localhost:9980/wallet/siacoins"
```

> Example POST curl call without data or authentication

```go
curl -A "Sia-Agent" -X POST "localhost:9980/gateway/connect/123.456.789.0:9981"
```

Sia uses semantic versioning and is backwards compatible to version v1.0.0.

API calls return either JSON or no content. Success is indicated by 2xx HTTP status codes, while errors are indicated by 4xx and 5xx HTTP status codes. If an endpoint does not specify its expected status code refer to [standard responses](#Standard-Responses).

There may be functional API calls which are not documented. These are not guaranteed to be supported beyond the current release, and should not be used in production.

**Notes:**

- Requests must set their User-Agent string to contain the substring "Sia-Agent".
- By default, siad listens on "localhost:9980". This can be changed using the `--api-addr` flag when running siad.
- **Do not bind or expose the API to a non-loopback address unless you are aware of the possible dangers.**

## Documentation Standards

The following details the documentation standards for the API endpoints.
 - Endpoints should follow the structure of:
    - Parameters
    - Response
 - Each endpoint should have a corresponding curl example
 - All non-standard responses should have a JSON Response example
  with units
 - There should be detailed descriptions of all JSON response fields
 - There should be detailed descriptions of all query string parameters
 - Query String Parameters should be separated into **REQUIRED** and **OPTIONAL** sections
 - Detailed descriptions should be structured as "**field** | units"

Contributors should follow these standards when submitting updates to the API documentation.  If you find API endpoints that do not adhere to these documentation standards please let the Sia team know by submitting an issue [here](https://gitlab.com/NebulousLabs/Sia/issues)

# Standard Responses

## Success
The standard response indicating the request was successfully processed is HTTP status code `204 No Content`. If the request was successfully processed and the server responded with JSON the HTTP status code is `200 OK`. Specific endpoints may specify other 2xx status codes on success.

## Error

```go
{
    "message": String

    // There may be additional fields depending on the specific error.
}
```

The standard error response indicating the request failed for any reason, is a 4xx or 5xx HTTP status code with an error JSON object describing the error.

# Authentication
> Example POST curl call with Authentication

```go
curl -A "Sia-Agent" --user "":<apipassword> --data "amount=123&destination=abcd" "localhost:9980/wallet/siacoins"
```

API authentication is enabled by default, using a password stored in a flat file. The location of this file is:

 - Linux:   `$HOME/.sia/apipassword`
 - MacOS:   `$HOME/Library/Application Support/Sia/apipassword`
 - Windows: `%LOCALAPPDATA%\Sia\apipassword`


Note that the file contains a trailing newline, which must be trimmed before
use.

Authentication is HTTP Basic Authentication as described in [RFC 2617](https://tools.ietf.org/html/rfc2617), however, the username is the empty string. The flag does not enforce authentication on all API endpoints. Only endpoints that expose sensitive information or modify state require authentication.

For example, if the API password is "foobar" the request header should include

`Authorization: Basic OmZvb2Jhcg==`

And for a curl call the following would be included

`--user "":<apipassword>`

Authentication can be disabled by passing the `--authenticate-api=false` flag to siad. You can change the password by modifying the password file, setting the `SIA_API_PASSWORD` environment variable, or passing the `--temp-password` flag to siad.

# Units

Unless otherwise noted, all parameters should be identified in their smallest possible unit. For example, size should always be specified in bytes and Siacoins should always be specified in hastings. JSON values returned by the API will also use the smallest possible unit, unless otherwise noted.

If a number is returned as a string in JSON, it should be treated as an arbitrary-precision number (bignum), and it should be parsed with your language's corresponding bignum library. Currency values are the most common example where this is necessary.

# Consensus

The consensus set manages everything related to consensus and keeps the blockchain in sync with the rest of the network. The consensus set's API endpoint returns information about the state of the blockchain.

## /consensus [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/consensus"
```

Returns information about the consensus set, such as the current block height.
Also returns the set of constants in use in the consensus code.

### JSON Response
> JSON Response Example

```go
{
  "synced":       true, // boolean
  "height":       62248, // blockheight
  "currentblock": "00000000000008a84884ba827bdc868a17ba9c14011de33ff763bd95779a9cf1", // hash
  "target":       [0,0,0,0,0,0,11,48,125,79,116,89,136,74,42,27,5,14,10,31,23,53,226,238,202,219,5,204,38,32,59,165], // hash
  "difficulty":   "1234" // arbitrary-precision integer

  "blockfrequency":         600,        // seconds per block
  "blocksizelimit":         2000000,    // bytes
  "extremefuturethreshold": 10800,      // seconds
  "futurethreshold":        10800,      // seconds
  "genesistimestamp":       1257894000, // Unix time
  "maturitydelay":          144,        // blocks
  "mediantimestampwindow":  11,         // blocks
  "siafundcount":           "10000",    // siafund
  "siafundportion":         "39/1000",  // fraction

  "initialcoinbase": 300000, // Siacoins (see note in Daemon.md)
  "minimumcoinbase": 30000,  // Siacoins (see note in Daemon.md)

  "roottarget": [0,0,0,0,32,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0], // hash
  "rootdepth":  [255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255,255],  // hash

  "siacoinprecision": "1000000000000000000000000" // hastings per siacoin
}
```
**synced** | boolean
True if the consensus set is synced with the network, e.g. it has downloaded the entire blockchain.  

**height** | blockheight
Number of blocks preceding the current block.  

**currentblock** | hash
Hash of the current block.  

**target** | hash
An immediate child block of this block must have a hash less than this target for it to be valid.  

**difficulty** | arbitrary-precision integer
The difficulty of the current block target.  

**blockfrequency** | blocks / second
Target for how frequently new blocks should be mined.  

**blocksizelimit** | bytes
Maximum size, in bytes, of a block. Blocks larger than this will be rejected by peers.  

**extremefuturethreshold** | seconds
Farthest a block's timestamp can be in the future before the block is rejected outright.  

**futurethreshold** | seconds
How far in the future a block can be without being rejected. A block further into the future will not be accepted immediately, but the daemon will attempt to accept the block as soon as it is valid.  

**genesistimestamp** | unix timestamp
Timestamp of the genesis block.  

**maturitydelay** | number of blocks
Number of children a block must have before it is considered "mature."  

**mediantimestampwindow** | number of blocks
Duration of the window used to adjust the difficulty.  

**siafundcount** | siafunds
Total number of siafunds.  

**siafundportion** | fraction
Fraction of each file contract payout given to siafund holders.  

**initialcoinbase** | siacoin
Number of coins given to the miner of the first block. Note that elsewhere in the API currency is typically returned in hastings and as a bignum. This is not the case here.  

**minimumcoinbase** | siacoin
Minimum number of coins paid out to the miner of a block (the coinbase decreases with each block). Note that elsewhere in the API currency is typically returned in hastings and as a bignum. This is not the case here.  

**roottarget** | hash
Initial target.  

**rootdepth** | hash
Initial depth.  

**siacoinprecision** | hastings per siacoin
Number of Hastings in one Siacoin.  

## /consensus/blocks [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/consensus/blocks?height=20032"
```
```go
curl -A "Sia-Agent" "localhost:9980/consensus/blocks?id=00000000000033b9eb57fa63a51adeea857e70f6415ebbfe5df2a01f0d0477f4"
```

Returns the block for a given id or height.

### Query String Parameters
#### REQUIRED
One of the following parameters must be specified.

**id** | blockID
BlockID of the requested block.  

**height** | blockheight
BlockHeight of the requested block.  

### JSON Response
> JSON Response Example

```go
{
    "height": 20032, 
    "id": "00000000000033b9eb57fa63a51adeea857e70f6415ebbfe5df2a01f0d0477f4", 
    "minerpayouts": [
        {
            "unlockhash": "c199cd180e19ef7597bcf4beecdd4f211e121d085e24432959c42bdf9030e32b9583e1c2727c",
            "value": "279978000000000000000000000000"
        }
    ],
    "nonce": [4,12,219,7,0,0,0,0],
    "parentid": "0000000000009615e8db750eb1226aa5e629bfa7badbfe0b79607ec8b918a44c",
    "timestamp": 1444516982,
    "transactions": [
	{
	    // ...
	}
        {
            "arbitrarydata": [],
            "filecontractrevisions": [],
            "filecontracts": [],
            "minerfees": [],
            "siacoininputs": [
                {
                    "parentid": "24cbeb9df7eb2d81d0025168fc94bd179909d834f49576e65b51feceaf957a64",
                    "unlockconditions": {
                        "publickeys": [
                            {
                                "algorithm": "ed25519",
                                "key": "QET8w7WRbGfcnnpKd1nuQfE3DuNUUq9plyoxwQYDK4U="
                            }
                        ],
                        "signaturesrequired": 1,
                        "timelock": 0
                    }
                }
            ],
            "siacoinoutputs": [
                {
                    "unlockhash": "d54f500f6c1774d518538dbe87114fe6f7e6c76b5bc8373a890b12ce4b8909a336106a4cd6db",
                    "value": "1010000000000000000000000000"
                },
                {
                    "unlockhash": "48a56b19bd0be4f24190640acbd0bed9669ea9c18823da2645ec1ad9652f10b06c5d4210f971",
                    "value": "5780000000000000000000000000"
                }
            ],
            "siafundinputs": [],
            "siafundoutputs": [],
            "storageproofs": [],
            "transactionsignatures": [
                {
                    "coveredfields": {
                        "arbitrarydata": [],
                        "filecontractrevisions": [],
                        "filecontracts": [],
                        "minerfees": [],
                        "siacoininputs": [],
                        "siacoinoutputs": [],
                        "siafundinputs": [],
                        "siafundoutputs": [],
                        "storageproofs": [],
                        "transactionsignatures": [],
                        "wholetransaction": true
                    },
                    "parentid": "24cbeb9df7eb2d81d0025168fc94bd179909d834f49576e65b51feceaf957a64",
                    "publickeyindex": 0,
                    "signature": "pByLGMlvezIZWVZmHQs/ynGETETNbxcOY/kr6uivYgqZqCcKTJ0JkWhcFaKJU+3DEA7JAloLRNZe3PTklD3tCQ==",
                    "timelock": 0
                }
            ]
        },
        {
	    // ...
        }
    ]
}
```

## /consensus/validate/transactionset [POST]
> curl example  

```go
curl -A "Sia-Agent" --data "[JSON-encoded-txnset]" "localhost:9980/validate/transactionset"
```

validates a set of transactions using the current utxo set.

### Request Body Bytes

Since transactions may be large, the transaction set is supplied in the POST body, encoded in JSON format.

### Response

standard success or error response. See [standard responses](#standard-responses).

# Daemon

The daemon is responsible for starting and stopping the modules which make up the rest of Sia.

## /daemon/stop [GET]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/daemon/stop"
```

Cleanly shuts down the daemon. This may take a few seconds.

### Response
standard success or error response. See [standard responses](#standard-responses).

## /daemon/version [GET]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/daemon/version"
```

Returns the version of the Sia daemon currently running. 

### JSON Response
> JSON Response Example
 
```go
{
"version": "1.3.7" // string
}
```
**version** | string
This is the version number that is visible to its peers on the network.

# Gateway

The gateway maintains a peer to peer connection to the network and provides a method for calling RPCs on connected peers. The gateway's API endpoints expose methods for viewing the connected peers, manually connecting to peers, and manually disconnecting from peers. The gateway may connect or disconnect from peers on its own.

## /gateway [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/gateway"
```

returns information about the gateway, including the list of connected peers.

### JSON Response
> JSON Response Example
 
```go
{
    "netaddress":"333.333.333.333:9981",  // string
    "peers":[
        {
            "inbound":    alse,                    // boolean
            "local":      false,                   // boolean
            "netaddress": "222.222.222.222:9981",  // string
            "version":    "1.0.0",                 // string
        },
    ],
    "maxdownloadspeed": 1234,  // bytes per second
    "maxuploadspeed":   1234,  // bytes per second
}
```
**netaddress** | string
netaddress is the network address of the gateway as seen by the rest of the network. The address consists of the external IP address and the port Sia is listening on. It represents a `modules.NetAddress`.  

**peers** | array
peers is an array of peers the gateway is connected to. It represents an array of `modules.Peer`s.  
        
**inbound** | boolean
inbound is true when the peer initiated the connection. This field is exposed as outbound peers are generally trusted more than inbound peers, as inbound peers are easily manipulated by an adversary.  

**local** | boolean
local is true if the peer's IP address belongs to a local address range such as 192.168.x.x or 127.x.x.x  

**netaddress** | string
netaddress is the address of the peer. It represents a `modules.NetAddress`.  
        
**version** | string
version is the version number of the peer.  

**maxdownloadspeed** | bytes per second
Max download speed permitted in bytes per second

**maxuploadspeed** | bytes per second
Max upload speed permitted in bytes per second

## /gateway [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "maxdownloadspeed=1000000&maxuploadspeed=20000" "localhost:9980/gateway"
```

Modify settings that control the gateway's behavior.

### Query String Parameters
#### OPTIONAL
**maxdownloadspeed** | bytes per second  
Max download speed permitted in bytes per second  

**maxuploadspeed** | bytes per second  
Max upload speed permitted in bytes per second  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /gateway/connect/:*netaddress* [POST]
> curl example  

```go
curl -A "Sia-Agent" -X POST "localhost:9980/gateway/connect/123.456.789.0:9981"
```

connects the gateway to a peer. The peer is added to the node list if it is not already present. The node list is the list of all nodes the gateway knows about, but is not necessarily connected to.  

### Path Parameters
#### REQUIRED
netaddress is the address of the peer to connect to. It should be a reachable ip address and port number, of the form `IP:port`. IPV6 addresses must be enclosed in square brackets.  

**netaddress** | string
Example IPV4 address: 123.456.789.0:123  
Example IPV6 address: [123::456]:789  

### Response
standard success or error response. See [standard responses](#Standard-Responses).

## /gateway/disconnect/:*netaddress* [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> -X POST "localhost:9980/gateway/disconnect/123.456.789.0:9981"
```

disconnects the gateway from a peer. The peer remains in the node list. Disconnecting from a peer does not prevent the gateway from automatically connecting to the peer in the future.

### Path Parameters
#### REQUIRED
netaddress is the address of the peer to connect to. It should be a reachable ip address and port number, of the form `IP:port`. IPV6 addresses must be enclosed in square brackets.  

**netaddress** | string
Example IPV4 address: 123.456.789.0:123  
Example IPV6 address: [123::456]:789  

### Response
standard success or error response. See [standard responses](#standard-responses).

# Host

The host provides storage from local disks to the network. The host negotiates file contracts with remote renters to earn money for storing other users' files. The host's endpoints expose methods for viewing and modifying host settings, announcing to the network, and managing how files are stored on disk.

## /host [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/host"
```

fetches status information about the host.

### JSON Response
> JSON Response Example
 
```go
{
  "externalsettings": {
    "acceptingcontracts":   true,                 // boolean
    "maxdownloadbatchsize": 17825792,             // bytes
    "maxduration":          25920,                // blocks
    "maxrevisebatchsize":   17825792,             // bytes
    "netaddress":           "123.456.789.0:9982", // string
    "remainingstorage":     35000000000,          // bytes
    "sectorsize":           4194304,              // bytes
    "totalstorage":         35000000000,          // bytes
    "unlockhash":           "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab", // hash
    "windowsize":           144,  // blocks

    "collateral":    "57870370370",                     // hastings / byte / block
    "maxcollateral": "100000000000000000000000000000",  // hastings

    "contractprice":          "30000000000000000000000000", // hastings
    "downloadbandwidthprice": "250000000000000",            // hastings / byte
    "storageprice":           "231481481481",               // hastings / byte / block
    "uploadbandwidthprice":   "100000000000000",            // hastings / byte

    "revisionnumber": 0,      // int
    "version":        "1.0.0" // string
  },

  "financialmetrics": {
    "contractcount":                 2,     // int
    "contractcompensation":          "123", // hastings
    "potentialcontractcompensation": "123", // hastings

    "lockedstoragecollateral": "123", // hastings
    "lostrevenue":             "123", // hastings
    "loststoragecollateral":   "123", // hastings
    "potentialstoragerevenue": "123", // hastings
    "riskedstoragecollateral": "123", // hastings
    "storagerevenue":          "123", // hastings
    "transactionfeeexpenses":  "123", // hastings

    "downloadbandwidthrevenue":          "123", // hastings
    "potentialdownloadbandwidthrevenue": "123", // hastings
    "potentialuploadbandwidthrevenue":   "123", // hastings
    "uploadbandwidthrevenue":            "123"  // hastings
  },

  "internalsettings": {
    "acceptingcontracts":   true,                 // boolean
    "maxdownloadbatchsize": 17825792,             // bytes
    "maxduration":          25920,                // blocks
    "maxrevisebatchsize":   17825792,             // bytes
    "netaddress":           "123.456.789.0:9982", // string
    "windowsize":           144,                  // blocks
    
    "collateral":       "57870370370",                     // hastings / byte / block
    "collateralbudget": "2000000000000000000000000000000", // hastings
    "maxcollateral":    "100000000000000000000000000000",  // hastings
    
    "minbaserpcprice":           "123",                        //hastings
    "mincontractprice":          "30000000000000000000000000", // hastings
    "mindownloadbandwidthprice": "250000000000000",            // hastings / byte
    "minsectoraccessprice":      "123",                        //hastings
    "minstorageprice":           "231481481481",               // hastings / byte / block
    "minuploadbandwidthprice":   "100000000000000"             // hastings / byte
  },

  "networkmetrics": {
    "downloadcalls":     0,   // int
    "errorcalls":        1,   // int
    "formcontractcalls": 2,   // int
    "renewcalls":        3,   // int
    "revisecalls":       4,   // int
    "settingscalls":     5,   // int
    "unrecognizedcalls": 6    // int
  },

  "connectabilitystatus": "checking", // string
  "workingstatus":        "checking"  // string
}
```
#### externalsettings  
The settings that get displayed to untrusted nodes querying the host's status.  
  
**acceptingcontracts** | boolean  
Whether or not the host is accepting new contracts.  

**maxdownloadbatchsize** | bytes  
The maximum size of a single download request from a renter. Each download request has multiple round trips of communication that exchange money. Larger batch sizes mean fewer round trips, but more financial risk for the host - the renter can get a free batch when downloading by refusing to provide a signature.  

**maxduration** | blocks  
The maximum duration that a host will allow for a file contract. The host commits to keeping files for the full duration under the threat of facing a large penalty for losing or dropping data before the duration is complete. The storage proof window of an incoming file contract must end before the current height + maxduration.  

**maxrevisebatchsize** | bytes  
The maximum size of a single batch of file contract revisions. The renter can perform DoS attacks on the host by uploading a batch of data then refusing to provide a signature to pay for the data. The host can reduce this exposure by limiting the batch size. Larger batch sizes allow for higher throughput as there is significant communication overhead associated with performing a batch upload.  

**netaddress** | string
The IP address or hostname (including port) that the host should be contacted at.  

**remainingstorage** | bytes  
The amount of unused storage capacity on the host in bytes. It should be noted that the host can lie.  

**sectorsize** | bytes  
The smallest amount of data in bytes that can be uploaded or downloaded when performing calls to the host.  

**totalstorage** | bytes  
The total amount of storage capacity on the host. It should be noted that the host can lie.  

**unlockhash** | hash
The unlock hash is the address at which the host can be paid when forming file contracts.  

**windowsize** | blocks  
The storage proof window is the number of blocks that the host has to get a storage proof onto the blockchain. The window size is the minimum size of window that the host will accept in a file contract.  

**collateral** | hastings / byte / block  
The maximum amount of money that the host will put up as collateral for storage that is contracted by the renter.  

**maxcollateral** | hastings  
The maximum amount of collateral that the host will put into a single file contract.  

**contractprice** | hastings  
The price that a renter has to pay to create a contract with the host. The payment is intended to cover transaction fees for the file contract revision and the storage proof that the host will be submitting to the blockchain.  

**downloadbandwidthprice** | hastings / byte  
The price that a renter has to pay when downloading data from the host.  

**storageprice** | hastings / byte / block  
The price that a renter has to pay to store files with the host.  

**uploadbandwidthprice** | hastings / byte  
The price that a renter has to pay when uploading data to the host.  

**revisionnumber** | int 
The revision number indicates to the renter what iteration of settings the host is currently at. Settings are generally signed. If the renter has multiple conflicting copies of settings from the host, the renter can expect the one with the higher revision number to be more recent.  

**version** | string
The version of external settings being used. This field helps coordinate updates while preserving compatibility with older nodes.  

#### financialmetrics  
The financial status of the host.  
  
**contractcount** | int
Number of open file contracts.  

**contractcompensation** | hastings  
The amount of money that renters have given to the host to pay for file contracts. The host is required to submit a file contract revision and a storage proof for every file contract that gets created, and the renter pays for the miner fees on  these objects.  

**potentialcontractcompensation** | hastings  
The amount of money that renters have given to the host to pay for file contracts which have not been confirmed yet. The potential compensation becomes compensation after the storage proof is submitted.  

**lockedstoragecollateral** | hastings  
The amount of storage collateral which the host has tied up in file contracts. The host has to commit collateral to a file contract even if there is no storage, but the locked collateral will be returned even if the host does not submit a storage proof - the collateral is not at risk, it is merely set aside so that it can be put at risk later.  

**lostrevenue** | hastings  
The amount of revenue, including storage revenue and bandwidth revenue, that has been lost due to failed file contracts and failed storage proofs.  

**loststoragecollateral** | hastings  
The amount of collateral that was put up to protect data which has been lost due to failed file contracts and missed storage proofs.  

**potentialstoragerevenue** | hastings  
The amount of revenue that the host stands to earn if all storage proofs are submitted corectly and in time.  

**riskedstoragecollateral** | hastings  
The amount of money that the host has risked on file contracts. If the host starts missing storage proofs, the host can forfeit up to this many coins. In the event of a missed storage proof, locked storage collateral gets returned, but risked storage collateral does not get returned.  

**storagerevenue** | hastings  
The amount of money that the host has earned from storing data. This money has been locked down by successful storage proofs.  

**transactionfeeexpenses** | hastings  
The amount of money that the host has spent on transaction fees when submitting host announcements, file contract revisions, and storage proofs.  

**downloadbandwidthrevenue** | hastings  
The amount of money that the host has made from renters downloading their files. This money has been locked in by successsful storage proofs.  

**potentialdownloadbandwidthrevenue** | hastings  
The amount of money that the host stands to make from renters that downloaded their files. The host will only realize this revenue if the host successfully submits storage proofs for the related file contracts.  

**potentialuploadbandwidthrevenue** | hastings  
The amount of money that the host stands to make from renters that uploaded files. The host will only realize this revenue if the host successfully submits storage proofs for the related file contracts.  

**uploadbandwidthrevenue** | hastings  
The amount of money that the host has made from renters uploading their files. This money has been locked in by successful storage proofs.  

#### internalsettings  
The settings of the host. Most interactions between the user and the host occur by changing the internal settings.  

**acceptingcontracts** | boolean  
When set to true, the host will accept new file contracts if the terms are reasonable. When set to false, the host will not accept new file contracts at all.  

**maxdownloadbatchsize** | bytes  
The maximum size of a single download request from a renter. Each download request has multiple round trips of communication that exchange money. Larger batch sizes mean fewer round trips, but more financial risk for the host - the renter can get a free batch when downloading by refusing to provide a signature.  

**maxduration** | blocks  
The maximum duration of a file contract that the host will accept. The storage proof window must end before the current height + maxduration.  

**maxrevisebatchsize** | bytes  
The maximum size of a single batch of file contract revisions. The renter can perform DoS attacks on the host by uploading a batch of data then refusing to provide a signature to pay for the data. The host can reduce this exposure by limiting the batch size. Larger batch sizes allow for higher throughput as there is significant communication overhead associated with performing a batch upload.  

**netaddress** | string
The IP address or hostname (including port) that the host should be contacted at. If left blank, the host will automatically figure out its ip address and use that. If given, the host will use the address given.  

**windowsize** | blocks  
The storage proof window is the number of blocks that the host has to get a storage proof onto the blockchain. The window size is the minimum size of window that the host will accept in a file contract.  

**collateral** | hastings / byte / block  
The maximum amount of money that the host will put up as collateral per byte per block of storage that is contracted by the renter.  

**collateralbudget** | hastings  
The total amount of money that the host will allocate to collateral across all file contracts.  

**maxcollateral** | hastings  
The maximum amount of collateral that the host will put into a
single file contract.

**minbaserpcprice** | hastings  
The minimum price that the host will demand from a renter for interacting with
the host. This is charged for every interaction a renter has with a host to pay
for resources consumed during the interaction. It is added to the
`mindownloadbandwidthprice` and `minuploadbandwidthprice` when uploading or
downloading files from the host.

**mincontractprice** | hastings  
The minimum price that the host will demand from a renter when forming a contract. Typically this price is to cover transaction fees on the file contract revision and storage proof, but can also be used if the host has a low amount of collateral. The price is a minimum because the host may automatically adjust the price upwards in times of high demand.  

**mindownloadbandwidthprice** | hastings / byte  
The minimum price that the host will demand from a renter when the renter is downloading data. If the host is saturated, the host may increase the price from the minimum.

**minsectoraccessprice** | hastings  
The minimum price that the host will demand from a renter for accessing a sector
of data on disk. Since the host has to read at least a full 4MB sector from disk
regardless of how much the renter intends to download this is charged to pay for
the physical disk resources the host uses. It is multiplied by the number of
sectors read then added to the `mindownloadbandwidthprice` when downloading a
file.

**minstorageprice** | hastings / byte / block  
The minimum price that the host will demand when storing data for extended periods of time. If the host is low on space, the price of storage may be set higher than the minimum.  

**minuploadbandwidthprice** | hastings / byte  
The minimum price that the host will demand from a renter when the renter is uploading data. If the host is saturated, the host may increase the price from the minimum.  

#### networkmetrics  
Information about the network, specifically various ways in which renters have contacted the host.  

**downloadcalls** | int 
The number of times that a renter has attempted to download something from the host.  

**errorcalls** | int
The number of calls that have resulted in errors. A small number of errors are expected, but a large number of errors indicate either buggy software or malicious network activity. Usually buggy software.  

**formcontractcalls** | int  
The number of times that a renter has tried to form a contract with the host.  

**renewcalls** | int
The number of times that a renter has tried to renew a contract with the host.  

**revisecalls** | int
The number of times that the renter has tried to revise a contract with the host.  

**settingscalls** | int
The number of times that a renter has queried the host for the host's settings. The settings include the price of bandwidth, which is a price that can adjust every few minutes. This value is usually very high compared to the others.  

**unrecognizedcalls** | int
The number of times that a renter has attempted to use an unrecognized call. Larger numbers typically indicate buggy software.  

**connectabilitystatus** | string
connectabilitystatus is one of "checking", "connectable", or "not connectable", and indicates if the host can connect to itself on its configured NetAddress.  

**workingstatus** | string
workingstatus is one of "checking", "working", or "not working" and indicates if the host is being actively used by renters.  

## /host [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> -X POST "localhost:9980/host?acceptingcontracts=true&maxduration=12096&windowsize=1008"
```

Configures hosting parameters. All parameters are optional; unspecified parameters will be left unchanged.

### Query String Parameters
#### OPTIONAL
**acceptingcontracts** | boolean
When set to true, the host will accept new file contracts if the terms are reasonable. When set to false, the host will not accept new file contracts at all.  

**maxdownloadbatchsize** | bytes
The maximum size of a single download request from a renter. Each download request has multiple round trips of communication that exchange money. Larger batch sizes mean fewer round trips, but more financial risk for the host - the renter can get a free batch when downloading by refusing to provide a signature.  

**maxduration** | blocks
The maximum duration of a file contract that the host will accept. The storage proof window must end before the current height + maxduration.  

**maxrevisebatchsize** | bytes
The maximum size of a single batch of file contract revisions. The renter can perform DoS attacks on the host by uploading a batch of data then refusing to provide a signature to pay for the data. The host can reduce this exposure by limiting the batch size. Larger batch sizes allow for higher throughput as there is significant communication overhead associated with performing a batch upload.  

**netaddress** | string  
The IP address or hostname (including port) that the host should be contacted at. If left blank, the host will automatically figure out its ip address and use that. If given, the host will use the address given.  

**windowsize** | blocks
// The storage proof window is the number of blocks that the host has to get a storage proof onto the blockchain. The window size is the minimum size of window that the host will accept in a file contract.

**collateral** | hastings / byte / block
The maximum amount of money that the host will put up as collateral per byte per block of storage that is contracted by the renter.  

**collateralbudget** | hastings
The total amount of money that the host will allocate to collateral across all file contracts.  

**maxcollateral** | hastings
The maximum amount of collateral that the host will put into a single file contract.  

**minbaserpcprice** | hastings  
The minimum price that the host will demand from a renter for interacting with
the host. This is charged for every interaction a renter has with a host to pay
for resources consumed during the interaction. It is added to the
`mindownloadbandwidthprice` and `minuploadbandwidthprice` when uploading or
downloading files from the host.

**mincontractprice** | hastings
The minimum price that the host will demand from a renter when forming a contract. Typically this price is to cover transaction fees on the file contract revision and storage proof, but can also be used if the host has a low amount of collateral. The price is a minimum because the host may automatically adjust the price upwards in times of high demand.  

**minsectoraccessprice** | hastings  
The minimum price that the host will demand from a renter for accessing a sector
of data on disk. Since the host has to read at least a full 4MB sector from disk
regardless of how much the renter intends to download this is charged to pay for
the physical disk resources the host uses. It is multiplied by the number of
sectors read then added to the `mindownloadbandwidthprice` when downloading a
file.

**mindownloadbandwidthprice** | hastings / byte
The minimum price that the host will demand from a renter when the renter is downloading data. If the host is saturated, the host may increase the price from the minimum.  

**minstorageprice** | hastings / byte / block
The minimum price that the host will demand when storing data for extended periods of time. If the host is low on space, the price of storage may be set higher than the minimum.  

**minuploadbandwidthprice** | hastings / byte
The minimum price that the host will demand from a renter when the renter is uploading data. If the host is saturated, the host may increase the price from the minimum.  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /host/announce [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> -X POST "localhost:9980/host/announce"
```

Announce the host to the network as a source of storage. Generally only needs to be called once.

Note that even after the host has been announced, it will not accept new contracts unless configured to do so. To configure the host to accept contracts, see [/host](## /host [POST]).

### Query String Parameters
#### OPTIONAL
**netaddress string** | string  
The address to be announced. If no address is provided, the automatically discovered address will be used instead.  

### Response

standard success or error response. See [standard responses](#Standard-Responses).

## /host/contracts [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/host/contracts"
```


Get contract information from the host database. This call will return all storage obligations on the host. Its up to the caller to filter the contracts based on their needs.

### JSON Response
> JSON Response Example
 
```go
{
  "contracts": [
    {
      "contractcost":			        "1234",		          // hastings
      "datasize":			            500000,		          // bytes
      "lockedcollateral":		      "1234",		          // hastings
      "obligationid": "fff48010dcbbd6ba7ffd41bc4b25a3634ee58bbf688d2f06b7d5a0c837304e13", // hash
      "potentialdownloadrevenue":	"1234",             // hastings
      "potentialstoragerevenue":	"1234",             // hastings
      "potentialuploadrevenue":		"1234",             // hastings
      "riskedcollateral":		      "1234",             // hastings
      "sectorrootscount":		      2,                  // int
      "transactionfeesadded":		  "1234",		          // hastings
      "expirationheight":		      123456,		          // blocks
      "negotiationheight":		    123456,		          // blocks
      "proofdeadline":			      123456,	  	        // blocks
      "obligationstatus":		      "obligationFailed", // string
      "originconfirmed":		      true,               // boolean
      "proofconfirmed":			      true,               // boolean
      "proofconstructed":		      true,               // boolean
      "revisionconfirmed":		    false,              // boolean
      "revisionconstructed":		  false,              // boolean
    }
  ]
}
```
**contractcost** | hastings
Amount in hastings to cover the transaction fees for this storage obligation.

**datasize** | bytes
Size of the data that is protected by the contract.

**lockedcollateral** | hastings
Amount that is locked as collateral for this storage obligation.

**obligationid** | hash
Id of the storageobligation, which is defined by the file contract id of the file contract that governs the storage obligation.

**potentialdownloadrevenue** | hastings
Potential revenue for downloaded data that the host will reveive upon successful completion of the obligation.

**potentialstoragerevenue** | hastings
Potential revenue for storage of data that the host will reveive upon successful completion of the obligation.

**potentialuploadrevenue** | hastings
Potential revenue for uploaded data that the host will reveive upon successful completion of the obligation.

**riskedcollateral** | hastings
Amount that the host might lose if the submission of the storage proof is not successful.

**sectorrootscount** | int
Number of sector roots.

**transactionfeesadded** | hastings
Amount for transaction fees that the host added to the storage obligation.

**expirationheight** | blockheight
Expiration height is the height at which the storage obligation expires.

**negotiationheight** | blockheight
Negotion height is the height at which the storage obligation was negotiated.

**proofdeadline** | blockheight
The proof deadline is the height by which the storage proof must be submitted.

**obligationstatus** | string
Status of the storage obligation. There are 4 different statuses:
 - `obligationFailed`:	the storage obligation failed, potential revenues and risked collateral are lost
 - `obligationRejected`:	the storage obligation was never started, no revenues gained or lost
 - `obligationSucceeded`:	the storage obligation was completed, revenues were gained
 - `obligationUnresolved`: 	the storage obligation has an uninitialized value. When the **proofdeadline** is in the past this might be a stale obligation.

**originconfirmed** | hash
Origin confirmed indicates whether the file contract was seen on the blockchain for this storage obligation.

**proofconfirmed** | boolean
Proof confirmed indicates whether there was a storage proof seen on the blockchain for this storage obligation.

**proofconstructed** | boolean
The host has constructed a storage proof

**revisionconfirmed** | boolean
Revision confirmed indicates whether there was a file contract revision seen on the blockchain for this storage obligation.

**revisionconstructed** | boolean
Revision constructed indicates whether there was a file contract revision constructed for this storage obligation.

## /host/storage [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/host/storage"
```

Gets a list of folders tracked by the host's storage manager.

### JSON Response
> JSON Response Example
 
```go
{
  "folders": [
    {
      "path":              "/home/foo/bar", // string
      "capacity":          50000000000,     // bytes
      "capacityremaining": 100000,          // bytes

      "failedreads":      0,  // int
      "failedwrites":     1,  // int
      "successfulreads":  2,  // int
      "successfulwrites": 3,  // int
    }
  ]
}
```
**path** | string
Absolute path to the storage folder on the local filesystem.  

**capacity** | bytes
Maximum capacity of the storage folder in bytes. The host will not store more than this many bytes in the folder. This capacity is not checked against the drive's remaining capacity. Therefore, you must manually ensure the disk has sufficient capacity for the folder at all times. Otherwise you risk losing renter's data and failing storage proofs.  

**capacityremaining** | bytes   
Unused capacity of the storage folder in bytes.  

**failedreads, failedwrites** | int  
Number of failed disk read & write operations. A large number of failed reads or writes indicates a problem with the filesystem or drive's hardware.  

**successfulreads, successfulwrites** | int  
Number of successful read & write operations.  

## /host/storage/folders/add [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "path=foo/bar&size=1000000000000" "localhost:9980/host/storage/add"
```

adds a storage folder to the manager. The manager may not check that there is enough space available on-disk to support as much storage as requested

### Query String Parameters
#### REQUIRED
**path** | string  
Local path on disk to the storage folder to add.  

**size** | bytes  
Initial capacity of the storage folder. This value isn't validated so it is possible to set the capacity of the storage folder greater than the capacity of the disk. Do not do this.  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /host/storage/folders/remove [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "path=foo/bar&force=false" "localhost:9980/host/storage/remove"
```

Remove a storage folder from the manager. All sotrage on the folder will be moved to other stoarge folders, meaning that no data will be lost. If the manager is unable to save data, an error will be returned and the operation will be stopped.

### Query String Parameters
#### REQUIRED
**path** | string  
Local path on disk to the storage folder to removed.  

#### OPTIONAL
**force** | boolean  
If `force` is true, the storage folder will be removed even if the data in the storage folder cannot be moved to other storage folders, typically because they don't have sufficient capacity. If `force` is true and the data cannot be moved, data will be lost.  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /host/storage/folders/resize [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "path=foo/bar&newsize=1000000000000" "localhost:9980/host/storage/resize"
```

Grows or shrinks a storage file in the manager. The manager may not check that there is enough space on-disk to support growing the storasge folder, but should gracefully handle running out of space unexpectedly. When shrinking a storage folder, any data in the folder that neeeds to be moved will be placed into other storage folders, meaning that no data will be lost. If the manager is unable to migrate the data, an error will be returned and the operation will be stopped.

### Query String Parameters
#### REQUIRED
**path** | string  
Local path on disk to the storage folder to resize.  

**newsize** | bytes  
Desired new size of the storage folder. This will be the new capacity of the storage folder.  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /host/storage/sectors/delete/:*merkleroot* [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> -X POST "localhost:9980/host/storage/sectors/delete/[merkleroot]"
```

Deletes a sector, meaning that the manager will be unable to upload that sector and be unable to provide a storage proof on that sector. This endpoint is for removing the data entirely, and will remove instances of the sector appearing at all heights. The primary purpose is to comply with legal requests to remove data.

### Path Parameters
#### REQUIRED
**merkleroot** | merkleroot
Merkleroot of the sector to delete.  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /host/estimatescore [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/host/estimatescore"
```

Returns the estimated HostDB score of the host using its current settings, combined with the provided settings.

### Query String Parameters
#### OPTIONAL
See [host internal settings](#internalsettings)
 - acceptingcontracts   
 - maxdownloadbatchsize 
 - maxduration          
 - maxrevisebatchsize   
 - netaddress           
 - windowsize           
 - collateral        
 - collateralbudget 
 - maxcollateral    
 - mincontractprice          
 - mindownloadbandwidthprice  
 - minstorageprice            
 - minuploadbandwidthprice    

### JSON Response
> JSON Response Example

```go
{
	"estimatedscore": "123456786786786786786786786742133",  // big int
	"conversionrate": 95  // float64
}
```
**estimatedscore** | big int
estimatedscore is the estimated HostDB score of the host given the settings passed to estimatescore.  
  
**conversionrate** | float64
conversionrate is the likelihood given the settings passed to estimatescore that the host will be selected by renters forming contracts.  

# Host DB

The hostdb maintains a database of all hosts known to the network. The database identifies hosts by their public key and keeps track of metrics such as price.

## /hostdb [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/hostdb"
```

Shows some general information about the state of the hostdb.

### JSON Response
> JSON Response Example
 
```go
{
    "initialscancomplete": false  // boolean
}
```
**initialscancomplete** | boolean
indicates if all known hosts have been scanned at least once.

## /hostdb/active [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/hostdb/active"
```

lists all of the active hosts known to the renter, sorted by preference.

### Query String Parameters
#### OPTIONAL
**numhosts** | int  
Number of hosts to return. The actual number of hosts returned may be less if there are insufficient active hosts. Optional, the default is all active hosts.  

### JSON Response
> JSON Response Example
 
```go
{
  "hosts": [
        {
      "acceptingcontracts":     true,                 // boolean
      "maxdownloadbatchsize":   17825792,             // bytes
      "maxduration":            25920,                // blocks
      "maxrevisebatchsize":     17825792,             // bytes
      "netaddress":             "123.456.789.0:9982"  // string 
      "remainingstorage":       35000000000,          // bytes
      "sectorsize":             4194304,              // bytes
      "totalstorage":           35000000000,          // bytes
      "unlockhash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab", // hash
      "windowsize":             144,                            // blocks
      "collateral":             "20000000000"                   // hastings / byte / block
      "maxcollateral":          "1000000000000000000000000000"  // hastings
      "contractprice":          "1000000000000000000000000"     // hastings
      "downloadbandwidthprice": "35000000000000"                // hastings / byte
      "storageprice":           "14000000000"                   // hastings / byte / block
      "uploadbandwidthprice":   "3000000000000"                 // hastings / byte
      "revisionnumber":         12733798,                       // int
      "version":                "1.3.4"                         // string
      "firstseen":              160000,                         // blocks
      "historicdowntime":       0,                              // nanoseconds
      "historicuptime":         41634520900246576,              // nanoseconds
      "scanhistory": [
        {
          "success": true,  // boolean
          "timestamp": "2018-09-23T08:00:00.000000000+04:00"  // unix timestamp
        },
        {
          "success": true,  // boolean
          "timestamp": "2018-09-23T06:00:00.000000000+04:00"  // unix timestamp
        },
        {
          "success": true,  // boolean// boolean
          "timestamp": "2018-09-23T04:00:00.000000000+04:00"  // unix timestamp
        }
      ],
      "historicfailedinteractions":     0,      // int
      "historicsuccessfulinteractions": 5,      // int
      "recentfailedinteractions":       0,      // int
      "recentsuccessfulinteractions":   0,      // int
      "lasthistoricupdate":             174900, // blocks
      "ipnets": [
        "1.2.3.0",  // string
        "2.1.3.0"   // string
      ],
      "lastipnetchange": "2015-01-01T08:00:00.000000000+04:00", // unix timestamp
      "publickey": {
        "algorithm": "ed25519", // string
        "key":       "RW50cm9weSBpc24ndCB3aGF0IGl0IHVzZWQgdG8gYmU=" // string
      },
      "publickeystring": "ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",  // string
      "filtered": false, // boolean
    }
  ]
}
```

#### hosts
**acceptingcontracts** | boolean  
true if the host is accepting new contracts.  

**maxdownloadbatchsize** | bytes  
Maximum number of bytes that the host will allow to be requested by a single download request.  

**maxduration** | blocks  
Maximum duration in blocks that a host will allow for a file contract. The host commits to keeping files for the full duration under the threat of facing a large penalty for losing or dropping data before the duration is complete. The storage proof window of an incoming file contract must end before the current height + maxduration.  

There is a block approximately every 10 minutes. e.g. 1 day = 144 blocks  

**maxrevisebatchsize** | bytes  
Maximum size in bytes of a single batch of file contract revisions. Larger batch sizes allow for higher throughput as there is significant communication overhead associated with performing a batch upload.  

**netaddress** | sting
Remote address of the host. It can be an IPv4, IPv6, or hostname, along with the port. IPv6 addresses are enclosed in square brackets.  

**remainingstorage** | bytes  
Unused storage capacity the host claims it has.  

**sectorsize** | bytes  
Smallest amount of data in bytes that can be uploaded or downloaded to or from the host.  

**totalstorage** | bytes  
Total amount of storage capacity the host claims it has.  

**unlockhash** | hash
Address at which the host can be paid when forming file contracts.  

**windowsize** | blocks  
A storage proof window is the number of blocks that the host has to get a storage proof onto the blockchain. The window size is the minimum size of window that the host will accept in a file contract.  

**collateral** | hastings / byte / block  
The maximum amount of money that the host will put up as collateral for storage that is contracted by the renter.  

**maxcollateral** | hastings  
The maximum amount of collateral that the host will put into a single file contract.  

**contractprice** | hastings  
The price that a renter has to pay to create a contract with the host. The payment is intended to cover transaction fees for the file contract revision and the storage proof that the host will be submitting to the blockchain.  

**downloadbandwidthprice** | hastings / byte  
The price that a renter has to pay when downloading data from the host.  

**storageprice** | hastings / byte / block  
The price that a renter has to pay to store files with the host.  

**uploadbandwidthprice** | hastings / byte  
The price that a renter has to pay when uploading data to the host.  

**revisionnumber** | int
The revision number indicates to the renter what iteration of settings the host is currently at. Settings are generally signed. If the renter has multiple conflicting copies of settings from the host, the renter can expect the one with the higher revision number to be more recent.  

**version** | string
The version of the host.  

**firstseen** | blocks  
Firstseen is the last block height at which this host was announced.  

**historicdowntime** | nanoseconds
Total amount of time the host has been offline.  

**historicuptime** | nanoseconds  
Total amount of time the host has been online.  

**scanhistory** 
Measurements that have been taken on the host. The most recent measurements are kept in full detail.  

**historicfailedinteractions** | int
Number of historic failed interactions with the host.  

**historicsuccessfulinteractions** | int 
Number of historic successful interactions with the host.  

**recentfailedinteractions** | int  
Number of recent failed interactions with the host.  

**recentsuccessfulinteractions** | int
Number of recent successful interactions with the host.  

**lasthistoricupdate** | blocks  
The last time that the interactions within scanhistory have been compressed into the historic ones.  

**ipnets**  
List of IP subnet masks used by the host. For IPv4 the /24 and for IPv6 the /54 subnet mask is used. A host can have either one IPv4 or one IPv6 subnet or one of each. E.g. these lists are valid: [ "IPv4" ], [ "IPv6" ] or [ "IPv4", "IPv6" ]. The following lists are invalid: [ "IPv4", "IPv4" ], [ "IPv4", "IPv6", "IPv6" ]. Hosts with an invalid list are ignored.  

**lastipnetchange** | date  
The last time the list of IP subnet masks was updated. When equal subnet masks are found for different hosts, the host that occupies the subnet mask for a longer time is preferred.  

**publickey** 
Public key used to identify and verify hosts.  
        
**algorithm** | string
Algorithm used for signing and verification. Typically "ed25519".  

**key** | hash
Key used to verify signed host messages.  

**publickeystring** | string 
The string representation of the full public key, used when calling /hostdb/hosts.  

**filtered** | boolean
Indicates if the host is currently being filtered from the HostDB

## /hostdb/all [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/hostdb/all"
```

Lists all of the hosts known to the renter. Hosts are not guaranteed to be in any particular order, and the order may change in subsequent calls.

### JSON Response 
Repsonse is the same as [`/hostdb/active`](#hosts)

## /hostdb/hosts/:*pubkey* [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/hostdb/hosts/ed25519:8a95848bc71e9689e2f753c82c35dc47a1d62867f77c0113ebb6fa5b51723215"
```

fetches detailed information about a particular host, including metrics regarding the score of the host within the database. It should be noted that each renter uses different metrics for selecting hosts, and that a good score on in one hostdb does not mean that the host will be successful on the network overall.

### Path Parameters
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/hostdb/hosts/<pubkey>"
```
#### REQUIRED
**pubkey**  
The public key of the host. Each public key identifies a single host.  

Example Pubkey: ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef  

### JSON Response 
> JSON Response Example
 
```go
{
  "entry": {
    // same as hosts
  },
  "scorebreakdown": {
    "score":                      1,        // big int
    "conversionrate":             9.12345,  // float64
    "ageadjustment":              0.1234,   // float64
    "burnadjustment":             0.1234,   // float64
    "collateraladjustment":       23.456,   // float64
	"durationadjustment":         1,        // float64
    "interactionadjustment":      0.1234,   // float64
    "priceadjustment":            0.1234,   // float64
    "storageremainingadjustment": 0.1234,   // float64
    "uptimeadjustment":           0.1234,   // float64
    "versionadjustment":          0.1234,   // float64
  }
}
```
Response is the same as [`/hostdb/active`](#hosts) with the additional of the **scorebreakdown**

**scorebreakdown**  
A set of scores as determined by the renter. Generally, the host's final score is all of the values multiplied together. Modified renters may have additional criteria that they use to judge a host, or may ignore certin criteia. In general, these fields should only be used as a loose guide for the score of a host, as every renter sees the world differently and uses different metrics to evaluate hosts.  

**score** | big int
The overall score for the host. Scores are entriely relative, and are consistent only within the current hostdb. Between different machines, different configurations, and different versions the absolute scores for a given host can be off by many orders of magnitude. When displaying to a human, some form of normalization with respect to the other hosts (for example, divide all scores by the median score of the hosts) is recommended.  

**conversionrate** | float64 
conversionrate is the likelihood that the host will be selected by renters forming contracts.  

**ageadjustment** | float64 
The multiplier that gets applied to the host based on how long it has been a host. Older hosts typically have a lower penalty.  

**burnadjustment** | float64 
The multiplier that gets applied to the host based on how much proof-of-burn the host has performed. More burn causes a linear increase in score.  

**collateraladjustment** | float64 
The multiplier that gets applied to a host based on how much collateral the host is offering. More collateral is typically better, though above a point it can be detrimental.  

**durationadjustment** | float64
The multiplier that gets applied to a host based on the max duration it accepts for file contracts. Typically '1' for hosts with an acceptable max duration, and '0' for hosts that have a max duration which is not long enough.

**interactionadjustment** | float64 
The multipler that gets applied to a host based on previous interactions with the host. A high ratio of successful interactions will improve this hosts score, and a high ratio of failed interactions will hurt this hosts score. This adjustment helps account for hosts that are on unstable connections, don't keep their wallets unlocked, ran out of funds, etc.  

**pricesmultiplier** | float64 
The multiplier that gets applied to a host based on the host's price. Lower prices are almost always better. Below a certain, very low price, there is no advantage.  

**storageremainingadjustment** | float64 
The multiplier that gets applied to a host based on how much storage is remaining for the host. More storage remaining is better, to a point.  

**uptimeadjustment** | float64 
The multiplier that gets applied to a host based on the uptime percentage of the host. The penalty increases extremely quickly as uptime drops below 90%.  

**versionadjustment** | float64 
The multiplier that gets applied to a host based on the version of Sia that they are running. Versions get penalties if there are known bugs, scaling limitations, performance limitations, etc. Generally, the most recent version is always the one with the highest score.  

## /hostdb/filtermode [POST]
> curl example  

```go
curl -A "Sia-Agent" --user "":<apipassword> --data '{"filtermode" : "whitelist","hosts" : ["ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef","ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef","ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"]}' "localhost:9980/hostdb/filtermode"
```  
```go
curl -A "Sia-Agent" --user "":<apipassword> --data '{"filtermode" : "disable"}' "localhost:9980/hostdb/filtermode"
```
Lets you enable and disable a filter mode for the hostdb. Currenlty the two modes supported are `blacklist` mode and `whitelist` mode. In `blacklist` mode, any hosts you identify as being on the `blacklist` will not be used to form contracts. In `whitelist` mode, only the hosts identified as being on the `whitelist` will be used to form contracts. In both modes, hosts that you are blacklisted will be filtered from your hostdb. To enable either mode, set `filtermode` to the desired mode and submit a list of host pubkeys as the corresponding `blacklist` or `whitelist`. To disable either list, the `host` field can be left blank (e.g. empty slice) and the `filtermode` should be set to `disable`.  

**NOTE:** Enabling and disabling a filter mode can result in changes with your current contracts with can result in an increase in contract fee spending. For example, if `blacklist` mode is enabled, any hosts that you currently have contracts with that are also on the provide list of `hosts` will have their contracts replaced with non-blacklisted hosts. When `whitelist` mode is enabled, contracts will be replaced until there are only contracts with whitelisted hosts. Even disabling a filter mode can result in a change in contracts if there are better scoring hosts in your hostdb that were previously being filtered out.  

### Query String Parameters
#### REQUIRED
**filtermode** | string  
Can be either whitelist, blacklist, or disable.  

**hosts** | array of string
Comma separated pubkeys.  

### Response

standard success or error response. See [standard responses](#standard-responses).

# Miner

The miner provides endpoints for getting headers for work and submitting solved headers to the network. The miner also provides endpoints for controlling a basic CPU mining implementation.

## /miner [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/miner"
```
returns the status of the miner.

### JSON Response 
> JSON Response Example
 
```go
{
  "blocksmined":      9001,   // int
  "cpuhashrate":      1337,   // hashes / second
  "cpumining":        false,  // boolean
  "staleblocksmined": 0,      // int
}
```
**blocksmined** | int
Number of mined blocks. This value is remembered after restarting.  

**cpuhashrate** | hashes / second
How fast the cpu is hashing, in hashes per second.  

**cpumining** | boolean
true if the cpu miner is active.  

**staleblocksmined** | int  
Number of mined blocks that are stale, indicating that they are not included in the current longest chain, likely because some other block at the same height had its chain extended first.  


## /miner/start [GET]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/miner/start"
```

Starts a single threaded CPU miner. Does nothing if the CPU miner is already running.

### Response

standard success or error response. See [standard responses](#standard-responses).

## /miner/stop [GET]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/miner/stop"
```

stops the cpu miner. Does nothing if the cpu miner is not running.

### Response

standard success or error response. See [standard responses](#standard-responses).

## /miner/header [GET]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/miner/header"
```

provides a block header that is ready to be grinded on for work.

### Byte Response
For efficiency the header for work is returned as a raw byte encoding of the header, rather than encoded to JSON.

Blocks are mined by repeatedly changing the nonce of the header, hashing the header's bytes, and comparing the resulting hash to the target. The block with that nonce is valid if the hash is less than the target. If none of the 2^64 possible nonces result in a header with a hash less than the target, call /miner/header [GET] again to get a new block header with a different merkle root. The above process can then be repeated for the new block header.  

The other fields can generally be ignored. The parent block ID field is the hash of the parent block's header. Modifying this field will result in an orphan block. The timestamp is the time at which the block was mined and is set by the Sia Daemon. Modifying this field can result in invalid block. The merkle root is the merkle root of a merkle tree consisting of the timestamp, the miner outputs (one leaf per payout), and the transactions (one leaf per transaction). Modifying this field will result in an invalid block.

Field | Byte range within response | Byte range within header
-------------- | -------------- | --------------
target | [0-32)
header | [32-112)
parent block ID | [32-64) | [0-32)
nonce | [64-72) | [32-40)
timestamp | [72-80) | [40-48)
merkle root | [80-112) | [48-80)

## /miner/header [POST]
> curl example  

```go
curl -A "Sia-Agent" -data "<byte-encoded-header>" -u "":<apipassword> "localhost:9980/miner"
```

submits a header that has passed the POW.

### Byte Response
For efficiency headers are submitted as raw byte encodings of the header in the body of the request, rather than as a query string parameter or path parameter. The request body should contain only the 80 bytes of the encoded header. The encoding is the same encoding used in `/miner/header [GET]` endpoint.

Blocks are mined by repeatedly changing the nonce of the header, hashing the header's bytes, and comparing the resulting hash to the target. The block with that nonce is valid if the hash is less than the target. If none of the 2^64 possible nonces result in a header with a hash less than the target, call /miner/header [GET] again to get a new block header with a different merkle root. The above process can then be repeated for the new block header.  

The other fields can generally be ignored. The parent block ID field is the hash of the parent block's header. Modifying this field will result in an orphan block. The timestamp is the time at which the block was mined and is set by the Sia Daemon. Modifying this field can result in invalid block. The merkle root is the merkle root of a merkle tree consisting of the timestamp, the miner outputs (one leaf per payout), and the transactions (one leaf per transaction). Modifying this field will result in an invalid block.

Field | Byte range within response | Byte range within header
-------------- | -------------- | --------------
target | [0-32)
header | [32-112)
parent block ID | [32-64) | [0-32)
nonce | [64-72) | [32-40)
timestamp | [72-80) | [40-48)
merkle root | [80-112) | [48-80)

# Renter

The renter manages the user's files on the network. The renter's API endpoints expose methods for managing files on the network and managing the renter's allocated funds.

## /renter [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/renter"
```

Returns the current settings along with metrics on the renter's spending.

### JSON Response
> JSON Response Example
 
```go
{
  "settings": {
    "allowance": {
      "funds":       "1234",  // hastings
      "hosts":       24,      // int
      "period":      6048,    // blocks
      "renewwindow": 3024     // blocks
    },
    "maxuploadspeed":     1234, // BPS
    "maxdownloadspeed":   1234, // BPS
    "streamcachesize":    4     // int
  },
  "financialmetrics": {
    "contractfees":     "1234", // hastings
    "contractspending": "1234", // hastings (deprecated, now totalallocated)
    "downloadspending": "5678", // hastings
    "storagespending":  "1234", // hastings
    "totalallocated":   "1234", // hastings
    "uploadspending":   "5678", // hastings
    "unspent":          "1234"  // hastings
  },
  "currentperiod": 200  // blockheight
}
```
#### settings  
Settings that control the behavior of the renter.  

#### allowance 
Allowance dictates how much the renter is allowed to spend in a given period. Note that funds are spent on both storage and bandwidth.  

**funds** | hastings  
Amount of money allocated for contracts. Funds are spent on both storage and bandwidth.  

**hosts** | int
Number of hosts that contracts will be formed with.  

**period** | blocks  
Duration of contracts formed, in number of blocks.  

**renewwindow** | blocks  
If the current blockheight + the renew window >= the height the contract is scheduled to end, the contract is renewed automatically. Is always nonzero.  

**maxuploadspeed** | bytes per second  
MaxUploadSpeed by default is unlimited but can be set by the user to manage bandwidth.  

**maxdownloadspeed** | bytes per second  
MaxDownloadSpeed by default is unlimited but can be set by the user to manage bandwidth.  

**streamcachesize** | int
The StreamCacheSize is the number of data chunks that will be cached during streaming.  

#### financialmetrics  
Metrics about how much the Renter has spent on storage, uploads, and downloads.  

**contractfees** | hastings  
Amount of money spent on contract fees, transaction fees and siafund fees.  

**contractspending** | hastings, (deprecated, now totalallocated)  
How much money, in hastings, the Renter has spent on file contracts, including fees.  

**downloadspending** | hastings  
Amount of money spent on downloads.  

**storagespending** | hastings  
Amount of money spend on storage.  

**totalallocated** | hastings  
Total amount of money that the renter has put into contracts. Includes spent money and also money that will be returned to the renter.  

**uploadspending** | hastings  
Amount of money spent on uploads.  

**unspent** | hastings  
Amount of money in the allowance that has not been spent.  

**currentperiod** | blockheight
Height at which the current allowance period began.  

## /renter [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "period=12096&renewwindow=4032&funds=1000&hosts=50" "localhost:9980/renter"
```

Modify settings that control the renter's behavior.

### Query Response Parameters
#### OPTIONAL
Any of the renter settings can be set, see fields [here](#settings)

**checkforipviolation** | boolean  
Enables or disables the check for hosts using the same ip subnets within the hostdb. It's turned on by default and causes Sia to not form contracts with hosts from the same subnet and if such contracts already exist, it will deactivate the contract which has occupied that subnet for the shorter time.  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /renter/contract/cancel [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "id=bd7ef21b13fb85eda933a9ff2874ec50a1ffb4299e98210bf0dd343ae1632f80" "localhost:9980/renter/contract/cancel"
```

cancels a specific contract of the Renter.

### Query String Parameters
#### REQUIRED
**id** | hash
ID of the file contract

### Response

standard success or error response. See [standard responses](#standard-responses).

## /renter/backup [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "destination=/home/backups/01-01-1968.backup" "localhost:9980/renter/backup"
```

Creates a backup of all siafiles in the renter at the specified path.

### Query Response Parameters
#### REQUIRED
**destination** | string
The path on disk where the backup will be created. Needs to be an absolute
path.

### Response

standard success or error response. See [standard responses](#standard-responses).

## /renter/recoverbackup [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "source=/home/backups/01-01-1968.backup" "localhost:9980/renter/recoverbackup"
```

Recovers an existing backup from the specified path by adding all the siafiles
contained within it to the renter. Should a siafile for a certain path already
exist, a number will be added as a suffix. e.g. 'myfile_1.sia'

### Query Response Parameters
#### REQUIRED
**source** | string
The path on disk where the backup will be recovered from. Needs to be an
absolute path.

### Response

standard success or error response. See [standard responses](#standard-responses).

## /renter/contracts [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/renter/contracts?inactive=true&expired=true&recoverable=false"
```

Returns the renter's contracts.  Active contracts are contracts that the Renter is currently using to store, upload, and download data, and are returned by default. Inactive contracts are contracts that are in the current period but are marked as not good for renew, these contracts have the potential to become active again but currently are not storing data.  Expired contracts are contracts not in the current period, where not more data is being stored and excess funds have been released to the renter. Recoverable contracts are contracts which the contractor is currently trying to recover and which haven't expired yet. 

### Query String Parameters
#### OPTIONAL
**inactive** | boolean
flag indicating if inactive contracts should be returned.

**expired** | boolean
flag indicating if expired contracts should be returned.

**recoverable** | boolean
flag indicating if recoverable contracts should be returned.

### JSON Response
> JSON Response Example
 
```go
{
  "activecontracts": [
    {
      "downloadspending": "1234", // hastings
      "endheight":        50000,  // block height
      "fees":             "1234", // hastings
      "hostpublickey": {
        "algorithm": "ed25519",   // string
        "key": "RW50cm9weSBpc24ndCB3aGF0IGl0IHVzZWQgdG8gYmU=" // hash
      },
      "hostversion":      "1.4.0",  // string
      "id": "1234567890abcdef0123456789abcdef0123456789abcdef0123456789abcdef", // hash
      "lasttransaction": {},                // transaction
      "netaddress":       "12.34.56.78:9",  // string
      "renterfunds":      "1234",           // hastings
      "size":             8192,             // bytes
      "startheight":      50000,            // block height
      "storagespending":  "1234",           // hastings
      "totalcost":        "1234",           // hastings
      "uploadspending":   "1234"            // hastings
      "goodforupload":    true,             // boolean
      "goodforrenew":     false,            // boolean
    }
  ],
  "inactivecontracts": [],
  "expiredcontracts": [],
  "recoverablecontracts": [],
}
```
**downloadspending** | hastings  
Amount of contract funds that have been spent on downloads.  

**endheight** | block height  
Block height that the file contract ends on.  

**fees** | hastings  
Fees paid in order to form the file contract.  

**hostpublickey** 
Public key of the host that the file contract is formed with.  
       
**hostversion** | string
The version of the host. 

**algorithm** | string
Algorithm used for signing and verification. Typically "ed25519".  

**key** | hash
Key used to verify signed host messages.  

**id** | hash
ID of the file contract.  

**lasttransaction** | transaction 
A signed transaction containing the most recent contract revision.  

**netaddress** | string
Address of the host the file contract was formed with.  

**renterfunds** | hastings  
Remaining funds left for the renter to spend on uploads & downloads.  

**size** | bytes  
Size of the file contract, which is typically equal to the number of bytes that have been uploaded to the host.

**startheight** | block height  
Block height that the file contract began on.  

**storagespending** | hastings  
Amount of contract funds that have been spent on storage.  

**totalcost** | hastings  
Total cost to the wallet of forming the file contract. This includes both the fees and the funds allocated in the contract.  

**uploadspending** | hastings  
Amount of contract funds that have been spent on uploads.  

**goodforupload** | boolean  
Signals if contract is good for uploading data.  

**goodforrenew** | boolean  
Signals if contract is good for a renewal.  

## /renter/dir/*siapath [GET]
> curl example  

> The root siadir path is "" so submitting the API call without an empty siapath
will return the root siadir information.  

```go
curl -A "Sia-Agent" "localhost:9980/renter/dir/"
```  
```go
curl -A "Sia-Agent" "localhost:9980/renter/dir/mydir"
```

retrieves the contents of a directory on the sia network

### Path Parameters
#### REQUIRED
**siapath** | string
Path to the directory on the sia netork  

### JSON Response
> JSON Response Example

```go
{
  "directories": [
    {
      "aggregatenumfiles":        2,    // uint64
      "aggregatenumstuckchunks":  4,    // uint64
      "aggregatesize":            4096, // uint64
      "heatlh":                   1.0,  // float64
      "lasthealtchecktime": "2018-09-23T08:00:00.000000000+04:00" // timestamp
      "maxhealth":                0.5,  // float64
      "minredundancy":            2.6,  // float64
      "mostrecentmodtime":  "2018-09-23T08:00:00.000000000+04:00" // timestamp
      "stuckhealth":              1.0,  // float64

      "numfiles":   3,        // uint64
      "numsubdirs": 2,        // uint64
      "siapath":    "foo/bar" // string
    }
  ],
  "files": []
}
```
**directories**
An array of sia directories

**aggregatenumfiles** | uint64
the total number of files in the sub directory tree

**aggregatenumstuckchunks** | uint64
the total number of stuck chunks in the sub directory tree

**aggregatenumsize** | uint64

**health** | float64
This is the worst health of any of the files or subdirectories. Health is the percent of parity pieces missing.
 - health = 0 is full redundancy
 - health <= 1 is recoverable
 - health > 1 needs to be repaired from disk

**lasthealthchecktime** | timestamp
The oldest time that the health of the directory or any of its files or sub directories' health was checked.

**maxhealth** | float64
This is the worst health when comparing stuck health vs health

**minredundancy** | float64
the lowest redundancy of any file or directory in the sub directory tree

**mostrecentmodtime** | timestamp
the most recent mod time of any file or directory in the sub directory tree

**numfiles** | uint64
the number of files in the directory

**numsubdirs** | uint64
the number of directories in the directory

**siapath** | string
The path to the directory on the sia network

**files**
Same response as [files](#files)

## /renter/dir/*siapath [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "action=delete" "localhost:9980/renter/dir/mydir"
```

performs various functions on the renter's directories

### Path Parameters
#### REQUIRED
**siapath** | string
Location where the directory will reside in the renter on the network. The path must be non-empty, may not include any path traversal strings ("./", "../"), and may not begin with a forward-slash character.  

### Query String Parameters
#### REQUIRED
**action** | string
Action can be either `create` or `delete`.
 - `create` will create an empty directory on the sia network
 - `delete` will remove a directory and its contents from the sia network

### Response

standard success or error response. See [standard responses](#standard-responses).

## /renter/downloads [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/renter/downloads"
```

Lists all files in the download queue.

### JSON Response
> JSON Response Example
 
```go
{
  "downloads": [
    {
      "destination":     "/home/users/alice/bar.txt", // string
      "destinationtype": "file",                      // string
      "length":          8192,                        // bytes
      "offset":          2000,                        // bytes
      "siapath":         "foo/bar.txt",               // string

      "completed":           true,                    // boolean
      "endtime":             "2009-11-10T23:10:00Z",  // RFC 3339 time
      "error":               "",                      // string
      "received":            8192,                    // bytes
      "starttime":           "2009-11-10T23:00:00Z",  // RFC 3339 time
      "totaldatatransfered": 10031                    // bytes
    }
  ]
}
```
**destination** | string
Local path that the file will be downloaded to.  

**destinationtype** | string
What type of destination was used. Can be "file", indicating a download to disk, can be "buffer", indicating a download to memory, and can be "http stream", indicating that the download was streamed through the http API.  

**length** | bytes  
Length of the download. If the download was a partial download, this will indicate the length of the partial download, and not the length of the full file.  

**offset** | bytes
Offset within the file of the download. For full file downloads, the offset will be '0'. For partial downloads, the offset may be anywhere within the file. offset+length will never exceed the full file size.  

**siapath** | string
Siapath given to the file when it was uploaded.  

**completed** | oolean  
Whether or not the download has completed. Will be false initially, and set to true immediately as the download has been fully written out to the file, to the http stream, or to the in-memory buffer. Completed will also be set to true if there is an error that causes the download to fail.  

**endtime** | date, RFC 3339 time  
Time at which the download completed. Will be zero if the download has not yet completed.  

**error** | string
Error encountered while downloading. If there was no error (yet), it will be the empty string.  

**received** | bytes  
Number of bytes downloaded thus far. Will only be updated as segments of the file complete fully. This typically has a resolution of tens of megabytes.  

**starttime** | date, RFC 3339 time  
Time at which the download was initiated.

**totaldatatransfered** | bytes
The total amount of data transfered when downloading the file. This will eventually include data transferred during contract + payment negotiation, as well as data from failed piece downloads.  

## /renter/downloads/clear [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> -X POST "localhost:9980/renter/downloads/clear?before=1551398400&after=1552176000"
```

Clears the download history of the renter for a range of unix time stamps.  Both parameters are optional, if no parameters are provided, the entire download history will be cleared.  To clear a single download, provide the timestamp for the download as both parameters.  Providing only the before parameter will clear all downloads older than the timestamp. Conversely, providing only the after parameter will clear all downloads newer than the timestamp.

### Query String Parameters
#### OPTIONAL
**before** | unix timestamp  
unix timestamp found in the download history

**after** | unix timestamp   
unix timestamp found in the download history

### Response

standard success or error response. See [standard responses](#standard-responses).

## /renter/prices [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/renter/prices"
```

Lists the estimated prices of performing various storage and data operations. An allowance can be submitted to provide a more personalized estimate. If no allowance is submitted then the current set allowance will be used, if there is no allowance set then sane defaults will be used. Submitting an allowance is optional, but when submitting an allowance all the components of the allowance are required. The allowance used to create the estimate is returned with the estimate.

### Query String Parameters
##### REQUIRED or OPTIONAL
Allowance settings, see the fields [here](#allowance)

### JSON Response
> JSON Response Example
 
```go
{
  "downloadterabyte":      "1234",  // hastings
  "formcontracts":         "1234",  // hastings
  "storageterabytemonth":  "1234",  // hastings
  "uploadterabyte":        "1234",  // hastings
  "funds":                 "1234",  // hastings
  "hosts":                     24,  // int
  "period":                  6048,  // blocks
  "renewwindow":             3024   // blocks
}
```
**downloadterabyte** | hastings  
The estimated cost of downloading one terabyte of data from the network.  

**formcontracts** | hastings  
The estimated cost of forming a set of contracts on the network. This cost also applies to the estimated cost of renewing the renter's set of contracts.  

**storageterabytemonth** | hastings  
The estimated cost of storing one terabyte of data on the network for a month, including accounting for redundancy.  

**uploadterabyte** | hastings  
The estimated cost of uploading one terabyte of data to the network, including accounting for redundancy.  

The allowance settings used for the estimation are also returned, see the fields [here](#allowance)

## /renter/files [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/renter/files"
```

lists the status of all files.

### JSON Response
> JSON Response Example
 
```go
{
  "files": [
    {
      "accesstime":       12578940002019-02-20T17:46:20.34810935+01:00,  // timestamp
      "available":        true,                 // boolean
      "changetime":       12578940002019-02-20T17:46:20.34810935+01:00,  // timestamp
      "ciphertype":       "threefish",          // string   
      "createtime":       12578940002019-02-20T17:46:20.34810935+01:00,  // timestamp
      "expiration":       60000,                // block height
      "filesize":         8192,                 // bytes
      "health":           0.5,                  // float64
      "localpath":        "/home/foo/bar.txt",  // string
      "maxhealth":        0.0,                  // float64  
      "maxhealthpercent": 100%,                 // float64
      "modtime":          12578940002019-02-20T17:46:20.34810935+01:00,  // timestamp
      "numstuckchunks":   0,                    // uint64
      "ondisk":           true,                 // boolean
      "recoverable":      true,                 // boolean
      "redundancy":       5,                    // float64
      "renewing":         true,                 // boolean
      "siapath":          "foo/bar.txt",        // string
      "stuck":            false,                // bool
      "stuckhealth":      0.0,                  // float64
      "uploadedbytes":    209715200,            // total bytes uploaded
      "uploadprogress":   100,                  // percent
    }
  ]
}
```
#### files

**accesstime** | timestamp
indicates the last time the siafile was accessed

**available** | boolean  
true if the file is available for download. Files may be available before they are completely uploaded.  

**changetime** | timestamp
indicates the last time the siafile metadata was updated

**ciphertype** | string
indicates the encryption used for the siafile

**createtime** | timestamp
indicates when the siafile was created

**expiration** | block height
Block height at which the file ceases availability.  

**filesize** | bytes  
Size of the file in bytes.  

**health** | float64 
health is an indication of the amount of redundancy missing where 0 is full redundancy and >1 means the file is not available. The health of the siafile is the health of the worst unstuck chunk.

**localpath** | string
Path to the local file on disk.  

**maxhealth** | float64
the maxhealth is either the health or the stuckhealth of the siafile, whichever is worst

**maxhealthpercent** | float64
maxhealthpercent is the maxhealth converted to be out of 100% to be more easily understood

**modtime** | timestamp
indicates the last time the siafile contents where modified

**numstuckchunks** | uint64
indicates the number of stuck chunks in a file. A chunk is stuck if it cannot reach full redundancy

**ondisk** | boolean
indicates if the source file is found on disk

**recoverable** | boolean
indicates if the siafile is recoverable

**redundancy** | float64
Average redundancy of the file on the network. Redundancy is calculated by dividing the amount of data uploaded in the file's open contracts by the size of the file. Redundancy does not necessarily correspond to availability. Specifically, a redundancy >= 1 does not indicate the file is available as there could be a chunk of the file with 0 redundancy.  

**renewing** | boolean  
true if the file's contracts will be automatically renewed by the renter.  

**siapath** | string
Path to the file in the renter on the network.  

**stuck** | bool
a file is stuck if there are any stuck chunks in the file, which means the file cannot reach full redundancy

**stuckhealth** | float64
stuckhealth is the worst health of any of the stuck chunks.

**uploadedbytes** | bytes  
Total number of bytes successfully uploaded via current file contracts. This number includes padding and rendundancy, so a file with a size of 8192 bytes might be padded to 40 MiB and, with a redundancy of 5, encoded to 200 MiB for upload.  

**uploadprogress** | percent  
Percentage of the file uploaded, including redundancy. Uploading has completed when uploadprogress is 100. Files may be available for download before upload progress is 100.  

## /renter/file/*siapath* [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/renter/file/myfile"
```

Lists the status of specified file.

### Path Parameters
#### REQUIRED
**siapath** | string
Path to the file in the renter on the network.

### JSON Response
Same response as [files](#files)

## /renter/file/*siapath* [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "trackingpath=/home/myfile" "localhost:9980/renter/file/myfile"
```

endpoint for changing file metadata.

### Path Parameters
#### REQUIRED
**siapath** | string
SiaPath of the file on the network. The path must be non-empty, may not include any path traversal strings ("./", "../"), and may not begin with a forward-slash character.

### Query String Parameters
#### OPTIONAL
**trackingpath** | string
If provided, this parameter changes the tracking path of a file to the  specified path. Useful if moving the file to a different location on disk.

### Response

standard success or error response. See [standard responses](#standard-responses).

## /renter/delete/*siapath* [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> -X POST "localhost:9980/renter/delete/myfile"
```

deletes a renter file entry. Does not delete any downloads or original files, only the entry in the renter.

### Path Parameters
#### REQUIRED
**siapath** | string
Path to the file in the renter on the network.

### Response

standard success or error response. See [standard responses](#standard-responses).

## /renter/download/*siapath* [GET]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/renter/download/myfile?httpresp=true"
```

downloads a file to the local filesystem. The call will block until the file has been downloaded.

### Path Parameters
#### REQUIRED
**siapath** | string
Path to the file in the renter on the network.

### Query String Parameters
#### REQUIRED (Either one or the other)
**destination** | string
Location on disk that the file will be downloaded to.  

**httpresp** | boolean
If httresp is true, the data will be written to the http response.

#### OPTIONAL
**async** | boolean
If async is true, the http request will be non blocking. Can't be used with:  

**length** | bytes
Length of the requested data. Has to be <= filesize-offset.  

**offset** | bytes
Offset relative to the file start from where the download starts.  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /renter/downloadsync/*siapath* [GET]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/renter/downloadasync/myfile?destination=/home/myfile"
```

downloads a file to the local filesystem. The call will return immediately.

### Path Parameters
#### REQUIRED
**siapath** | string
Path to the file in the renter on the network.

### Query String Parameters
#### REQUIRED
**destination** | string
Location on disk that the file will be downloaded to.  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /renter/recoveryscan [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> -X POST "localhost:9980/renter/recoveryscan"
```

starts a rescan of the whole blockchain to find recoverable contracts. The
contractor will periodically try to recover found contracts every 10 minutes
until they are recovered or expired.

### Response

standard success or error response. See [standard responses](#standard-responses).

## /renter/recoveryscan [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/renter/recoveryscan"
```

Returns some information about a potentially ongoing recovery scan.

### JSON Response
> JSON Response Example

```go
{
  "scaninprogress": true // boolean
  "scannedheight" : 1000 // uint64
}
```
**scaninprogress** | boolean
indicates if a scan for recoverable contracts is currently in progress.

**scannedheight** | uint64
indicates the progress of a currently ongoing scan in terms of number of blocks
that have already been scanned.

## /renter/rename/*siapath* [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "newsiapath=myfile2" "localhost:9980/renter/rename/myfile"
```

### Path Parameters
#### REQUIRED
**siapath** | string
Path to the file in the renter on the network.

### Query String Parameters
#### REQUIRED
**newsiapath** | string 
New location of the file in the renter on the network.  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /renter/stream/*siapath* [GET]
> curl example  

> Stream the whole file.  

```go
curl -A "Sia-Agent" "localhost:9980/renter/stream/myfile"
```  
> The file can be streamed partially by using standard partial http requests which means setting the "Range" field in the http header.  

```go
curl -A "Sia-Agent" -H "Range: bytes=0-1023" "localhost:9980/renter/stream/myfile"
```

downloads a file using http streaming. This call blocks until the data is received. The streaming endpoint also uses caching internally to prevent siad from re-downloading the same chunk multiple times when only parts of a file are requested at once. This might lead to a substantial increase in ram usage and therefore it is not recommended to stream multiple files in parallel at the moment. This restriction will be removed together with the caching once partial downloads are supported in the future. If you want to stream multiple files you should increase the size of the Renter's `streamcachesize` to at least 2x the number of files you are steaming.

### Path Parameters
#### REQUIRED
**siapath** | string
Path to the file in the renter on the network.

### Response

standard success or error response. See [standard responses](#standard-responses).

## /renter/upload/*siapath* [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "source=/home/myfile" "localhost:9980/renter/upload/myfile"
```

uploads a file to the network from the local filesystem.

### Path Parameters
#### REQUIRED
**siapath** | string
Location where the file will reside in the renter on the network. The path must be non-empty, may not include any path traversal strings ("./", "../"), and may not begin with a forward-slash character.  

### Query String Parameters
#### REQUIRED
**source** | string
Location on disk of the file being uploaded.  

#### OPTIONAL
**datapieces** | int  
The number of data pieces to use when erasure coding the file.  

**paritypieces** | int  
The number of parity pieces to use when erasure coding the file. Total redundancy of the file is (datapieces+paritypieces)/datapieces.  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /renter/uploadstream/*siapath* [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/renter/upload/myfile?datapieces=10&paritypieces=20" --data-binary @myfile.dat
```

uploads a file to the network using a stream.

### Path Parameters
#### REQUIRED
**siapath** | string
Location where the file will reside in the renter on the network. The path must be non-empty, may not include any path traversal strings ("./", "../"), and may not begin with a forward-slash character.  

### Query String Parameters
#### OPTIONAL
**datapieces** | int  
The number of data pieces to use when erasure coding the file.  

**paritypieces** | int  
The number of parity pieces to use when erasure coding the file. Total redundancy of the file is (datapieces+paritypieces)/datapieces.  

### Response

standard success or error response. See [standard responses](#standard-responses).

# Transaction Pool

## /tpool/confirmed/:id [GET]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/tpool/confirmed/22e8d5428abc184302697929f332fa0377ace60d405c39dd23c0327dc694fae7"
```

returns whether the requested transaction has been seen on the blockchain. Note, however, that the block containing the transaction may later be invalidated by a reorg.

### Path Parameters
#### REQUIRED
**id** | hash
id of the transaction being queried

### JSON Response
> JSON Response Example
 
```go
{
  "confirmed": true // boolean
}
```
**confirmed** | boolean
indicates if a transaction is confirmed on the blockchain

## /tpool/fee [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/tpool/fee"
```

returns the minimum and maximum estimated fees expected by the transaction pool.

### JSON Response
> JSON Response Example
 
```go
{
  "minimum": "1234", // hastings / byte
  "maximum": "5678"  // hastings / byte
}
```
**minimum** | hastings / byte
the minimum estimated fee

**maximum** | hastings / byte
the maximum estimated fee

## /tpool/raw/:id [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/tpool/raw/22e8d5428abc184302697929f332fa0377ace60d405c39dd23c0327dc694fae7"
```

returns the ID for the requested transaction and its raw encoded parents and transaction data.

### Path Parameters
#### REQUIRED
**id** | hash 
id of the transaction being queried

### JSON Response
> JSON Response Example
 
```go
{
	// id of the transaction
	"id": "124302d30a219d52f368ecd94bae1bfb922a3e45b6c32dd7fb5891b863808788",

	// raw, base64 encoded transaction data
	"transaction": "AQAAAAAAAADBM1ca/FyURfizmSukoUQ2S0GwXMit1iNSeYgrnhXOPAAAAAAAAAAAAQAAAAAAAABlZDI1NTE5AAAAAAAAAAAAIAAAAAAAAACdfzoaJ1MBY7L0fwm7O+BoQlFkkbcab5YtULa6B9aecgEAAAAAAAAAAQAAAAAAAAAMAAAAAAAAAAM7Ljyf0IA86AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEAAAAAAAAACgAAAAAAAACe0ZTbGbI4wAAAAAAAAAAAAAABAAAAAAAAAMEzVxr8XJRF+LOZK6ShRDZLQbBcyK3WI1J5iCueFc48AAAAAAAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEAAAAAAAAAA+z4P1wc98IqKxykTSJxiVT+BVbWezIBnIBO1gRRlLq2x/A+jIc6G7/BA5YNJRbdnqPHrzsZvkCv4TKYd/XzwBA==",
	"parents": "AQAAAAAAAAABAAAAAAAAAJYYmFUdXXfLQ2p6EpF+tcqM9M4Pw5SLSFHdYwjMDFCjAAAAAAAAAAABAAAAAAAAAGVkMjU1MTkAAAAAAAAAAAAgAAAAAAAAAAHONvdzzjHfHBx6psAN8Z1rEVgqKPZ+K6Bsqp3FbrfjAQAAAAAAAAACAAAAAAAAAAwAAAAAAAAAAzvNDjSrme8gwAAA4w8ODnW8DxbOV/JribivvTtjJ4iHVOug0SXJc31BdSINAAAAAAAAAAPGHY4699vggx5AAAC2qBhm5vwPaBsmwAVPho/1Pd8ecce/+BGv4UimnEPzPQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAQAAAAAAAACWGJhVHV13y0NqehKRfrXKjPTOD8OUi0hR3WMIzAxQowAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAABnt64wN1qxym/CfiMgOx5fg/imVIEhY+4IiiM7gwvSx8qtqKniOx50ekrGv8B+gTKDXpmm2iJibWTI9QLZHWAY=",
}
```
**id** | string  
id of the transaction.  

**transaction** | string  
raw, base64 encoded transaction data  

## /tpool/raw [POST]
> curl example  

```go
curl -A "Sia-Agent" --data "<raw-encoded-tset>" "localhost:9980/tpool/raw"
```

submits a raw transaction to the transaction pool, broadcasting it to the transaction pool's peers.  

### Query String Parameters
#### REQUIRED
**parents** | string 
JSON- or base64-encoded transaction parents

**transaction** | string
JSON- or base64-encoded transaction

### Response

standard success or error response. See [standard responses](#standard-responses).

## /tpool/confirmed/:id [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/tpool/confirmed/9028cc285ad755b81c0cfead3710c1344044175e59cdc4ca097374fe188c9acf"
```

### Query String Parameters
#### REQUIRED
**id** | hash
id of the transaction being queried

### JSON Response
> JSON Response Example

```go
{
  "confirmed": true,
}
```
**confirmed** | boolean
boolean that indicates if transaction was confirmed

# Wallet

## /wallet [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/wallet"
```

Returns basic information about the wallet, such as whether the wallet is locked or unlocked.

### JSON Response
> JSON Response Example

```go
{
  "encrypted":  true,   // boolean
  "unlocked":   true,   // boolean
  "rescanning": false,  // boolean

  "confirmedsiacoinbalance":     "123456", // hastings, big int
  "unconfirmedoutgoingsiacoins": "0",      // hastings, big int
  "unconfirmedincomingsiacoins": "789",    // hastings, big int

  "siafundbalance":      "1",    // siafunds, big int
  "siacoinclaimbalance": "9001", // hastings, big int

  "dustthreshold": "1234", // hastings / byte, big int
}
```
**encrypted** | boolean  
Indicates whether the wallet has been encrypted or not. If the wallet has not been encrypted, then no data has been generated at all, and the first time the wallet is unlocked, the password given will be used as the password for encrypting all of the data. 'encrypted' will only be set to false if the wallet has never been unlocked before (the unlocked wallet is still encryped - but the encryption key is in memory).  

**unlocked** | boolean  
Indicates whether the wallet is currently locked or unlocked. Some calls become unavailable when the wallet is locked.  

**rescanning** | boolean  
Indicates whether the wallet is currently rescanning the blockchain. This will be true for the duration of calls to /unlock, /seeds, /init/seed, and /sweep/seed.  

**confirmedsiacoinbalance** | hastings, big int  
Number of siacoins, in hastings, available to the wallet as of the most recent block in the blockchain.  

**unconfirmedoutgoingsiacoins** | hastings, big int  
Number of siacoins, in hastings, that are leaving the wallet according to the set of unconfirmed transactions. Often this number appears inflated, because outputs are frequently larger than the number of coins being sent, and there is a refund. These coins are counted as outgoing, and the refund is counted as incoming. The difference in balance can be calculated using 'unconfirmedincomingsiacoins' - 'unconfirmedoutgoingsiacoins'  

**unconfirmedincomingsiacoins** | hastings, big int  
Number of siacoins, in hastings, are entering the wallet according to the set of unconfirmed transactions. This number is often inflated by outgoing siacoins, because outputs are frequently larger than the amount being sent. The refund will be included in the unconfirmed incoming siacoins balance.  

**siafundbalance** | big int  
Number of siafunds available to the wallet as of the most recent block in the blockchain.  

**siacoinclaimbalance** | hastings, big int  
Number of siacoins, in hastings, that can be claimed from the siafunds as of the most recent block. Because the claim balance increases every time a file contract is created, it is possible that the balance will increase before any claim transaction is confirmed.  

**dustthreshold** | hastings / byte, big int  
Number of siacoins, in hastings per byte, below which a transaction output cannot be used because the wallet considers it a dust output.  

## /wallet/033x [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "source=/home/legacy-wallet&encryptionpassword=mypassword" "localhost:9980/wallet/033x"
```

Loads a v0.3.3.x wallet into the current wallet, harvesting all of the secret keys. All spendable addresses in the loaded wallet will become spendable from the current wallet.

### Query String Parameters
#### REQUIRED
**source**  
Path on disk to the v0.3.3.x wallet to be loaded.  

**encryptionpassword**  
Encryption key of the wallet.  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /wallet/address [GET]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/wallet/address"
```

Gets a new address from the wallet generated by the primary seed. An error will be returned if the wallet is locked.

### JSON Response
> JSON Response Example
 
```go
{
  "address": "1234567890abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab"
}
```
**address** | hash
Wallet address that can receive siacoins or siafunds. Addresses are 76 character long hex strings.  

## /wallet/addresses [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/wallet/addresses"
```

Fetches the list of addresses from the wallet. If the wallet has not been created or unlocked, no addresses will be returned. After the wallet is unlocked, this call will continue to return its addresses even after the wallet is locked again.

### JSON Response
> JSON Response Example
 
```go
{
  "addresses": [
    "1234567890abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab",
    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  ]
}
```
**addresses**  
Array of wallet addresses owned by the wallet.  

## /wallet/seedaddrs [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/wallet/seedaddrs"
```

Fetches addresses generated by the wallet in reverse order. The last address
generated by the wallet will be the first returned. This also means that
addresses which weren't generated using the wallet's seed can't be retrieved
with this endpoint.

### Query String Parameters
#### OPTIONAL
**count**  
Number of addresses that should be returned. If count is not specified or if
count is bigger than the number of addresses generated by the wallet, all
addresses will be returned.

### JSON Response
> JSON Response Example

```go
{
  "addresses": [
    "1234567890abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab",
    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  ]
}
```
**addresses**  
Array of wallet addresses previously generated by the wallet.

## /wallet/backup [GET]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/wallet/backup?destination=/home/wallet-settings.backup"
```

Creates a backup of the wallet settings file. Though this can easily be done manually, the settings file is often in an unknown or difficult to find location. The /wallet/backup call can spare users the trouble of needing to find their wallet file.

### Query String Parameters
#### REQUIRED
**destination**  
Path to the location on disk where the backup file will be saved.  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /wallet/changepassword [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> -X POST "localhost:9980/wallet/changepassword?encryptionpassword=<currentpassword>&newpassword=<newpassword>"
```

Changes the wallet's encryption key.  

### Query String Parameters
#### REQUIRED
**encryptionpassword** | string
encryptionpassword is the wallet's current encryption password.  

**newpassword** | string
newpassword is the new password for the wallet.  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /wallet/init [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "encryptionpassword=<password>&force=false" "localhost:9980/wallet/init"
```

Initializes the wallet. After the wallet has been initialized once, it does not need to be initialized again, and future calls to /wallet/init will return an error. The encryption password is provided by the api call. If the password is blank, then the password will be set to the same as the seed.

### Query String Parameters
#### OPTIONAL WALLET PARAMETERS
**encryptionpassword** | string
Password that will be used to encrypt the wallet. All subsequent calls should use this password. If left blank, the seed that gets returned will also be the encryption password.  

**dictionary** | string  
Name of the dictionary that should be used when encoding the seed. 'english' is the most common choice when picking a dictionary.  

**force** | boolean  
When set to true /wallet/init will Reset the wallet if one exists instead of returning an error. This allows API callers to reinitialize a new wallet.

### JSON Response
> JSON Response Example
 
```go
{
  "primaryseed": "hello world hello world hello world hello world hello world hello world hello world hello world hello world hello world hello world hello world hello world hello world hello"
}
```
**primaryseed**  
Wallet seed used to generate addresses that the wallet is able to spend.  

## /wallet/init/seed [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "seed=<seed>&encryptionpassword=<password>&force=false" "localhost:9980/wallet/init/seed"
```

Initializes the wallet using a preexisting seed. After the wallet has been initialized once, it does not need to be initialized again, and future calls to /wallet/init/seed will return an error. The encryption password is provided by the api call. If the password is blank, then the password will be set to the same as the seed. Note that loading a preexisting seed requires scanning the blockchain to determine how many keys have been generated from the seed. For this reason, /wallet/init/seed can only be called if the blockchain is synced.

### Query String Parameters
#### REQUIRED WALLET PARAMETERS
**seed** | string
Dictionary-encoded phrase that corresponds to the seed being used to initialize the wallet.  

#### OPTIONAL
[Optional Wallet Parameters](#optional-wallet-parameters)

### Response

standard success or error response. See [standard responses](#standard-responses).

## /wallet/seed [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "seed=<seed>" "localhost:9980/wallet/seed"
```

Gives the wallet a seed to track when looking for incoming transactions. The wallet will be able to spend outputs related to addresses created by the seed. The seed is added as an auxiliary seed, and does not replace the primary seed. Only the primary seed will be used for generating new addresses.

### Query String Parameters
#### REQUIRED
[Required Wallet Parameters](#required-wallet-parameters)

#### OPTIONAL | string
[Optional Wallet Parameters](#optional-wallet-parameters)

### Response

standard success or error response. See [standard responses](#standard-responses).

## /wallet/seeds [GET]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/wallet/seeds"
```

Returns the list of seeds in use by the wallet. The primary seed is the only seed that gets used to generate new addresses. This call is unavailable when the wallet is locked.

### Query String Parameters
#### REQUIRED
**dictionary** | string 
Name of the dictionary that should be used when encoding the seed. 'english' is the most common choice when picking a dictionary.  

### JSON Response
> JSON Response Example

```go
{
  "primaryseed":        "hello world hello world hello world hello world hello world hello world hello world hello world hello world hello world hello world hello world hello world hello world hello",
  "addressesremaining": 2500,
  "allseeds":           [
    "hello world hello world hello world hello world hello world hello world hello world hello world hello world hello world hello world hello world hello world hello world hello",
    "foo bar foo bar foo bar foo bar foo bar foo bar foo bar foo bar foo bar foo bar foo bar foo bar foo bar foo bar foo",
  ]
}
```
**primaryseed**  
Seed that is actively being used to generate new addresses for the wallet.  

**addressesremaining**  
Number of addresses that remain in the primary seed until exhaustion has been reached. Once exhaustion has been reached, new addresses will continue to be generated but they will be more difficult to recover in the event of a lost wallet file or encryption password.  

**allseeds**  
Array of all seeds that the wallet references when scanning the blockchain for outputs. The wallet is able to spend any output generated by any of the seeds, however only the primary seed is being used to generate new addresses.  

## /wallet/siacoins [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "amount=1000&destination=c134a8372bd250688b36867e6522a37bdc391a344ede72c2a79206ca1c34c84399d9ebf17773" "localhost:9980/wallet/siacoins"
```

Sends siacoins to an address or set of addresses. The outputs are arbitrarily selected from addresses in the wallet. If 'outputs' is supplied, 'amount' and 'destination' must be empty.  

### Query String Parameters
#### REQUIRED
Amount and Destination or Outputs are required

**amount** | hastings  
Number of hastings being sent. A hasting is the smallest unit in Sia. There are 10^24 hastings in a siacoin.

**destination** | address  
Address that is receiving the coins.  

**OR**

**outputs**  
JSON array of outputs. The structure of each output is: {"unlockhash": "<destination>", "value": "<amount>"}  

### JSON Response
> JSON Response Example

```go
{
  "transactionids": [
    "1234567890abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  ]
}
```
**transactionids**  
Array of IDs of the transactions that were created when sending the coins. The last transaction contains the output headed to the 'destination'. Transaction IDs are 64 character long hex strings.  

## /wallet/siafunds [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "amount=10&destination=c134a8372bd250688b36867e6522a37bdc391a344ede72c2a79206ca1c34c84399d9ebf17773" "localhost:9980/wallet/siafunds"
```

Sends siafunds to an address. The outputs are arbitrarily selected from addresses in the wallet. Any siacoins available in the siafunds being sent (as well as the siacoins available in any siafunds that end up in a refund address) will become available to the wallet as siacoins after 144 confirmations. To access all of the siacoins in the siacoin claim balance, send all of the siafunds to an address in your control (this will give you all the siacoins, while still letting you control the siafunds).

### Query String Parameters
#### REQUIRED
**amount** | siafunds  
Number of siafunds being sent.  

**destination** | address  
Address that is receiving the funds.  

### JSON Response
> JSON Response Example
 
```go
{
  "transactionids": [
    "1234567890abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  ]
}
```
**transactionids**  
Array of IDs of the transactions that were created when sending the coins. The last transaction contains the output headed to the 'destination'. Transaction IDs are 64 character long hex strings.  

## /wallet/siagkey [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "encryptionpassword=<password>&keyfiles=/file1,/home/file2" "localhost:9980/wallet/siagkey"
```

Loads a key into the wallet that was generated by siag. Most siafunds are currently in addresses created by siag.

### Query String Parameters
#### REQUIRED
**encryptionpassword**  
Key that is used to encrypt the siag key when it is imported to the wallet.  

**keyfiles**  
List of filepaths that point to the keyfiles that make up the siag key. There should be at least one keyfile per required signature. The filenames need to be commna separated (no spaces), which means filepaths that contain a comma are not allowed.  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /wallet/sign [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "<requestbody>" "localhost:9980/wallet/sign"
```

Signs a transaction. The wallet will attempt to sign each input specified. The transaction's TransactionSignatures should be complete except for the Signature field. If `tosign` is provided, the wallet will attempt to fill in signatures for each TransactionSignature specified. If `tosign` is not provided, the wallet will add signatures for every TransactionSignature that it has keys for.

### Request Body
> Request Body Example

```go
{
  // Unsigned transaction
  "transaction": {
    "siacoininputs": [
      {
        "parentid": "af1a88781c362573943cda006690576b150537c1ae142a364dbfc7f04ab99584",
        "unlockconditions": {
          "timelock": 0,
          "publickeys": [ "ed25519:8b845bf4871bcdf4ff80478939e508f43a2d4b2f68e94e8b2e3d1ea9b5f33ef1" ],
          "signaturesrequired": 1
        }
      }
    ],
    "siacoinoutputs": [
      {
        "value": "5000000000000000000000000",
        "unlockhash": "17d25299caeccaa7d1598751f239dd47570d148bb08658e596112d917dfa6bc8400b44f239bb"
      },
      {
        "value": "299990000000000000000000000000",
        "unlockhash": "b4bf662170622944a7c838c7e75665a9a4cf76c4cebd97d0e5dcecaefad1c8df312f90070966"
      }
    ],
    "minerfees": [ "1000000000000000000000000" ],
    "transactionsignatures": [
      {
        "parentid": "af1a88781c362573943cda006690576b150537c1ae142a364dbfc7f04ab99584",
        "publickeyindex": 0,
        "coveredfields": {"wholetransaction": true}
      }
    ]
  },

  // Optional IDs to sign; each should correspond to a parentid in the transactionsignatures.
  "tosign": [
    "af1a88781c362573943cda006690576b150537c1ae142a364dbfc7f04ab99584"
  ]
}
```

### JSON Response
> JSON Response Example
 
```go
{
  // signed transaction
  "transaction": {
    "siacoininputs": [
      {
        "parentid": "af1a88781c362573943cda006690576b150537c1ae142a364dbfc7f04ab99584",
        "unlockconditions": {
          "timelock": 0,
          "publickeys": [ "ed25519:8b845bf4871bcdf4ff80478939e508f43a2d4b2f68e94e8b2e3d1ea9b5f33ef1" ],
          "signaturesrequired": 1
        }
      }
    ],
    "siacoinoutputs": [
      {
        "value": "5000000000000000000000000",
        "unlockhash": "17d25299caeccaa7d1598751f239dd47570d148bb08658e596112d917dfa6bc8400b44f239bb"
      },
      {
        "value": "299990000000000000000000000000",
        "unlockhash": "b4bf662170622944a7c838c7e75665a9a4cf76c4cebd97d0e5dcecaefad1c8df312f90070966"
      }
    ],
    "minerfees": [ "1000000000000000000000000" ],
    "transactionsignatures": [
      {
        "parentid": "af1a88781c362573943cda006690576b150537c1ae142a364dbfc7f04ab99584",
        "publickeyindex": 0,
        "coveredfields": {"wholetransaction": true},
        "signature": "CVkGjy4The6h+UU+O8rlZd/O3Gb1xRJdyQ2vzBFEb/5KveDKDrrieCiFoNtUaknXEQbdxlrDqMujc+x3aZbKCQ=="
      }
    ]
  }
}
```

## /wallet/sweep/seed [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "seed=<seed>" "localhost:9980/wallet/sweep/seed"
```

Scans the blockchain for outputs belonging to a seed and send them to an address owned by the wallet.

### Query String Parameters
#### REQUIRED
**seed** | string
Dictionary-encoded phrase that corresponds to the seed being added to the wallet.  

#### OPTIONAL
**dictionary** | string  
Name of the dictionary that should be used when decoding the seed. 'english' is the most common choice when picking a dictionary.  

### JSON Response
 > JSON  Response Example
```go
{
  "coins": "123456", // hastings, big int
  "funds": "1",      // siafunds, big int
}
```
**coins** | hastings, big int  
Number of siacoins, in hastings, transferred to the wallet as a result of the sweep.  

**funds** | siafunds, big int  
Number of siafunds transferred to the wallet as a result of the sweep.  

## /wallet/lock [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> -X POST "localhost:9980/wallet/lock"
```

Locks the wallet, wiping all secret keys. After being locked, the keys are encrypted. Queries for the seed, to send siafunds, and related queries become unavailable. Queries concerning transaction history and balance are still available.

### Response

standard success or error response. See [standard responses](#standard-responses).

## /wallet/transaction/:*id* [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/wallet/transaction/22e8d5428abc184302697929f332fa0377ace60d405c39dd23c0327dc694fae7"
```

Gets the transaction associated with a specific transaction id.

### Path Parameters
#### REQUIRED
**id** | hash
ID of the transaction being requested.  

### JSON Response
> JSON Response Example

```go
{
  "transaction": {
    "transaction": {
      // See types.Transaction in https://gitlab.com/NebulousLabs/Sia/blob/master/types/transactions.go
    },
    "transactionid":         "1234567890abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "confirmationheight":    50000,
    "confirmationtimestamp": 1257894000,
    "inputs": [
      {
        "parentid":       "1234567890abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
        "fundtype":       "siacoin input",
        "walletaddress":  false,
        "relatedaddress": "1234567890abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab",
        "value":          "1234", // hastings or siafunds, depending on fundtype, big int
      }
    ],
    "outputs": [
      {
        "id":             "1234567890abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
        "fundtype":       "siacoin output",
        "maturityheight": 50000,
        "walletaddress":  false,
        "relatedaddress": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        "value":          "1234", // hastings or siafunds, depending on fundtype, big int
      }
    ]
  }
}
```
**transaction**  
Raw transaction. The rest of the fields in the resposne are determined from this raw transaction. It is left undocumented here as the processed transaction (the rest of the fields in this object) are usually what is desired.  

See types.Transaction in https://gitlab.com/NebulousLabs/Sia/blob/master/types/transactions.go  

**transactionid**  
ID of the transaction from which the wallet transaction was derived.  

**confirmationheight**  
Block height at which the transaction was confirmed. If the transaction is unconfirmed the height will be the max value of an unsigned 64-bit integer.  

**confirmationtimestamp**  
Time, in unix time, at which a transaction was confirmed. If the transaction is unconfirmed the timestamp will be the max value of an unsigned 64-bit integer.  

**inputs**  
Array of processed inputs detailing the inputs to the transaction.  

**parentid**  
The id of the output being spent.  

**fundtype**  
Type of fund represented by the input. Possible values are 'siacoin input' and 'siafund input'.  

**walletaddress** | Boolean  
true if the address is owned by the wallet.  

**relatedaddress**  
Address that is affected. For inputs (outgoing money), the related address is usually not important because the wallet arbitrarily selects which addresses will fund a transaction.  

**value** | hastings or siafunds, depending on fundtype, big int  
Amount of funds that have been moved in the input.  

**outputs**  
Array of processed outputs detailing the outputs of the transaction. Outputs related to file contracts are excluded.  

**id**  
The id of the output that was created.  

**fundtype**  
Type of fund is represented by the output. Possible values are 'siacoin output', 'siafund output', 'claim output', and 'miner payout'. Siacoin outputs and claim outputs both relate to siacoins.  

Siafund outputs relate to siafunds.  

Miner payouts point to siacoins that have been spent on a miner payout. Because the destination of the miner payout is determined by the block and not the transaction, the data 'maturityheight', 'walletaddress', and 'relatedaddress' areleft blank.  

**maturityheight**  
Block height the output becomes available to be spent. Siacoin outputs and siafund outputs mature immediately - their maturity height will always be the confirmation height of the transaction. Claim outputs cannot be spent until they have had 144 confirmations, thus the maturity height of a claim output will always be 144 larger than the confirmation height of the transaction.  

**walletaddress** | boolean  
true if the address is owned by the wallet.  

**relatedaddress**  
Address that is affected. For outputs (incoming money), the related address field can be used to determine who has sent money to the wallet.  

**value** | hastings or siafunds, depending on fundtype, big int  
Amount of funds that have been moved in the output.  

## /wallet/transactions [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/wallet/transactions"
```

Returns a list of transactions related to the wallet in chronological order.

### Query String Parameters
#### REQUIRED
**startheight** | block height  
// Height of the block where transaction history should begin.

**endheight** | block height  
Height of of the block where the transaction history should end. If 'endheight' is greater than the current height, or if it is '-1', all transactions up to and including the most recent block will be provided.

### JSON Response
> JSON Response Example

```go
{
  "confirmedtransactions": [
    {
      // See the documentation for '/wallet/transaction/:id' for more information.
    }
  ],
  "unconfirmedtransactions": [
    {
      // See the documentation for '/wallet/transaction/:id' for more information.
    }
  ]
}
```
**confirmedtransactions**  
All of the confirmed transactions appearing between height 'startheight' and height 'endheight' (inclusive).  

See the documentation for '/wallet/transaction/:id' for more information.  

**unconfirmedtransactions**  
All of the unconfirmed transactions.  

See the documentation for '/wallet/transaction/:id' for more information.  

## /wallet/transactions/:addr [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/wallet/transactions/abf1ba4ad65820ce2bd5d63466b8555d0ec9bfe5f5fa920b4fef6ad98f443e2809e5ae619b74"
```

Returns all of the transactions related to a specific address.

### Path Parameters
#### REQUIRED
**addr** | hash
Unlock hash (i.e. wallet address) whose transactions are being requested.  

### JSON Response
> JSON Response Example

```go
{
  "transactions": [
    {
      // See the documentation for '/wallet/transaction/:id' for more information.
    }
  ]
}
```
**transactions**  
Array of processed transactions that relate to the supplied address.  

See the documentation for '/wallet/transaction/:id' for more information.  

## /wallet/unlock [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "encryptionpassword=<password>" "localhost:9980/wallet/unlock"
```

Unlocks the wallet. The wallet is capable of knowing whether the correct password was provided.

### Query String Parameters
#### REQUIRED
**encryptionpassword** | string  
Password that gets used to decrypt the file. Most frequently, the encryption password is the same as the primary wallet seed.  

### Response

standard success or error response. See [standard responses](#standard-responses).

## /wallet/unlockconditions/:addr [GET]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/wallet/unlockconditions/2d6c6d705c80f17448d458e47c3fb1a02a24e018a82d702cda35262085a3167d98cc7a2ba339"
```

Returns the unlock conditions of :addr, if they are known to the wallet.

### Path Parameters
#### REQUIRED
**addr** | hash
Unlock hash (i.e. wallet address) whose transactions are being requested.  

### JSON Response
> JSON Response Example

```go
{
  "unlockconditions": {
    "timelock": 0,
    "publickeys": [{
      "algorithm": "ed25519",
      "key": "/XUGj8PxMDkqdae6Js6ubcERxfxnXN7XPjZyANBZH1I="
    }],
    "signaturesrequired": 1
  }
}
```
**timelock**  
The minimum blockheight required.  

**signaturesrequired**  
The number of signatures required.  

**publickeys**  
The set of keys whose signatures count towards signaturesrequired.  

## /wallet/unspent [GET]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/wallet/unspent"
```

Returns a list of outputs that the wallet can spend.

### JSON Response
> JSON Response Example

```go
{
  "outputs": [
    {
      "id": "1234567890abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      "fundtype": "siacoin output",
      "confirmationheight": 50000,
      "unlockhash": "1234567890abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab",
      "value": "1234", // big int
      "iswatchonly": false
    }
  ]
}
```
**outputs**  
Array of outputs that the wallet can spend.  

**id**  
The id of the output.  

**fundtype**  
Type of output, either 'siacoin output' or 'siafund output'.  

**confirmationheight**  
Height of block in which the output appeared. To calculate the number of confirmations, subtract this number from the current block height.  

**unlockhash**  
Hash of the output's unlock conditions, commonly known as the "address".  

**value** | big int  
Amount of funds in the output; hastings for siacoin outputs, and siafunds for siafund outputs.  

**iswatchonly** | Boolean  
Whether the output comes from a watched address or from the wallet's seed.  

## /wallet/verify/address/:addr [GET]
> curl example  

```go
curl -A "Sia-Agent" "localhost:9980/wallet/verify/address/75d9a7351022681ba3539d7e0c5699d143ab5a7747604998cace1299ab6c04c5ea2aa2e87aac"
```

Takes the address specified by :addr and returns a JSON response indicating if the address is valid.

### Path Parameters
#### REQUIRED
**addr** | hash
Unlock hash (i.e. wallet address) whose transactions are being requested.  

### JSON Response
> JSON Response Example

```go
{
	"valid": true
}
```
**valid**  
valid indicates if the address supplied to :addr is a valid UnlockHash.  

## /wallet/watch [GET]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> "localhost:9980/wallet/watch"
```

Returns the set of addresses that the wallet is watching. This set only includes addresses that were explicitly requested to be watched; addresses that were generated automatically by the wallet, or by /wallet/address, are not included.

### JSON Response
> JSON Response Example

```go
{
  "addresses": [
    "1234567890abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "abcdef0123456789abcdef0123456789abcd1234567890ef0123456789abcdef"
  ]
}
```
**addresses**  
The addresses currently watched by the wallet.  

## /wallet/watch [POST]
> curl example  

```go
curl -A "Sia-Agent" -u "":<apipassword> --data "<requestbody>" "localhost:9980/wallet/watch"
```

### Request Body
> Request Body Example

```go
{
  // The addresses to add or remove from the current set.
  "addresses": [
    "1234567890abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "abcdef0123456789abcdef0123456789abcd1234567890ef0123456789abcdef"
  ],

  // If true, remove the addresses instead of adding them.
  "remove": false,

  // If true, the wallet will not rescan the blockchain. Only set this flag if
  // the addresses have never appeared in the blockchain.
  "unused": true,
```

### Response

standard success or error response. See [standard responses](#standard-responses).

# Version
<a href='https://sia.tech/docs/v140'>**v140**
