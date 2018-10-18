Host DB API
===========

This document contains detailed descriptions of the hostdb's API routes. For an
overview of the hostdb's API routes, see [API.md#host-db](/doc/API.md#host-db).
For an overview of all API routes, see [API.md](/doc/API.md)

There may be functional API calls which are not documented. These are not
guaranteed to be supported beyond the current release, and should not be used
in production.

Overview
--------

The hostdb maintains a database of all hosts known to the network. The database
identifies hosts by their public key and keeps track of metrics such as price.

Index
-----

| Request                                                       | HTTP Verb | Examples                      |
| ------------------------------------------------------------- | --------- | ----------------------------- |
| [/hostdb](#hostdb-get-example)                                | GET       | [HostDB Get](#hostdb-get)     |
| [/hostdb/active](#hostdbactive-get-example)                   | GET       | [Active hosts](#active-hosts) |
| [/hostdb/all](#hostdball-get-example)                         | GET       | [All hosts](#all-hosts)       |
| [/hostdb/hosts/___:pubkey___](#hostdbhostspubkey-get-example) | GET       | [Hosts](#hosts)               |
| [/hostdb/listmode](#hostdblistmode-post)                      | POST      |                               |

#### /hostdb [GET] [(example)](#hostdb-get)

shows some general information about the state of the hostdb.

###### JSON Response 

Either the following JSON struct or an error response. See [#standard-responses](#standard-responses).

```javascript
{
    "initialscancomplete": false // indicates if all known hosts have been scanned at least once.
}
```

#### /hostdb/active [GET] [(example)](#active-hosts)

lists all of the active hosts known to the renter, sorted by preference.

###### Query String Parameters
```
// Number of hosts to return. The actual number of hosts returned may be less
// if there are insufficient active hosts. Optional, the default is all active
// hosts.
numhosts
```

###### JSON Response
```javascript
{
  "hosts": [
    {
      // true if the host is accepting new contracts.
      "acceptingcontracts": true,

      // The maximum amount of money that the host will put up as collateral
      // for storage that is contracted by the renter
      "collateral": "20000000000", // hastings / byte / block

      // The price that a renter has to pay to create a contract with the
      // host. The payment is intended to cover transaction fees
      // for the file contract revision and the storage proof that the host
      // will be submitting to the blockchain.
      "contractprice": "1000000000000000000000000", // hastings

      // The price that a renter has to pay when downloading data from the
      // host
      "downloadbandwidthprice": "35000000000000", // hastings / byte

      // Firstseen is the last block height at which this host was announced.
      "firstseen": 160000, // blocks

      // Total amount of time the host has been offline.
      "historicdowntime": 0,

      // Number of historic failed interactions with the host.
      "historicfailedinteractions": 0,

      // Number of historic successful interactions with the host.
      "historicsuccessfulinteractions": 5,

      // Total amount of time the host has been online.
      "historicuptime": 41634520900246576,

      // List of IP subnet masks used by the host. For IPv4 the /24 and for IPv6 the /54 subnet mask
      // is used. A host can have either one IPv4 or one IPv6 subnet or one of each. E.g. these
      // lists are valid: [ "IPv4" ], [ "IPv6" ] or [ "IPv4", "IPv6" ]. The following lists are 
      // invalid: [ "IPv4", "IPv4" ], [ "IPv4", "IPv6", "IPv6" ]. Hosts with an invalid list are ignored.
      "ipnets": [
        "1.2.3.0",
        "2.1.3.0"
      ],

      // The last time that the interactions within scanhistory have been compressed into the historic ones
      "lasthistoricupdate": 174900, // blocks

      // The last time the list of IP subnet masks was updated. When equal subnet masks are found for
      // different hosts, the host that occupies the subnet mask for a longer time is preferred.
      "lastipnetchange": "2015-01-01T08:00:00.000000000+04:00",

      // The maximum amount of collateral that the host will put into a
      // single file contract.
      "maxcollateral": "1000000000000000000000000000", // hastings

      // Maximum number of bytes that the host will allow to be requested by a
      // single download request.
      "maxdownloadbatchsize": 17825792, // bytes

      // Maximum duration in blocks that a host will allow for a file contract.
      // The host commits to keeping files for the full duration under the
      // threat of facing a large penalty for losing or dropping data before
      // the duration is complete. The storage proof window of an incoming file
      // contract must end before the current height + maxduration.
      //
      // There is a block approximately every 10 minutes.
      // e.g. 1 day = 144 blocks
      "maxduration": 25920, // blocks

      // Maximum size in bytes of a single batch of file contract
      // revisions. Larger batch sizes allow for higher throughput as there is
      // significant communication overhead associated with performing a batch
      // upload.
      "maxrevisebatchsize": 17825792, // bytes

      // Remote address of the host. It can be an IPv4, IPv6, or hostname,
      // along with the port. IPv6 addresses are enclosed in square brackets.
      "netaddress": "123.456.789.0:9982",

      // Public key used to identify and verify hosts.
      "publickey": {
        // Algorithm used for signing and verification. Typically "ed25519".
        "algorithm": "ed25519",

        // Key used to verify signed host messages.
        "key": "RW50cm9weSBpc24ndCB3aGF0IGl0IHVzZWQgdG8gYmU="
      },

      // The string representation of the full public key, used when calling
      // /hostdb/hosts.
      "publickeystring": "ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",

      // Number of recent failed interactions with the host.
      "recentfailedinteractions": 0,

      // Number of recent successful interactions with the host.
      "recentsuccessfulinteractions": 0,

      // Unused storage capacity the host claims it has.
      "remainingstorage": 35000000000, // bytes

      // The revision number indicates to the renter what iteration of
      // settings the host is currently at. Settings are generally signed.
      // If the renter has multiple conflicting copies of settings from the
      // host, the renter can expect the one with the higher revision number
      // to be more recent.
      "revisionnumber": 12733798,

      // Measurements that have been taken on the host. The most recent measurements
      // are kept in full detail.
      "scanhistory": [
        {
          "success": true,
          "timestamp": "2018-09-23T08:00:00.000000000+04:00"
        },
        {
          "success": true,
          "timestamp": "2018-09-23T06:00:00.000000000+04:00"
        },
        {
          "success": true,
          "timestamp": "2018-09-23T04:00:00.000000000+04:00"
        }
      ],

      // Smallest amount of data in bytes that can be uploaded or downloaded to
      // or from the host.
      "sectorsize": 4194304, // bytes

      // The price that a renter has to pay to store files with the host.
      "storageprice": "14000000000", // hastings / byte / block

      // Total amount of storage capacity the host claims it has.
      "totalstorage": 35000000000, // bytes

      // Address at which the host can be paid when forming file contracts.
      "unlockhash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab",

      "uploadbandwidthprice": "3000000000000", // hastings / byte

      // The version of the host.
      "version": "1.3.4",

      // A storage proof window is the number of blocks that the host has to
      // get a storage proof onto the blockchain. The window size is the
      // minimum size of window that the host will accept in a file contract.
      "windowsize": 144 // blocks
    }
  ]
}
```

#### /hostdb/all [GET] [(example)](#all-hosts)

lists all of the hosts known to the renter. Hosts are not guaranteed to be in
any particular order, and the order may change in subsequent calls.

###### JSON Response
```javascript
{
  "hosts": [
    {
      // true if the host is accepting new contracts.
      "acceptingcontracts": true,

      // The maximum amount of money that the host will put up as collateral
      // for storage that is contracted by the renter
      "collateral": "20000000000", // hastings / byte / block

      // The price that a renter has to pay to create a contract with the
      // host. The payment is intended to cover transaction fees
      // for the file contract revision and the storage proof that the host
      // will be submitting to the blockchain.
      "contractprice": "1000000000000000000000000", // hastings

      // The price that a renter has to pay when downloading data from the
      // host
      "downloadbandwidthprice": "35000000000000", // hastings / byte

      // Firstseen is the last block height at which this host was announced.
      "firstseen": 160000, // blocks

      // Total amount of time the host has been offline.
      "historicdowntime": 0,

      // Number of historic failed interactions with the host.
      "historicfailedinteractions": 0,

      // Number of historic successful interactions with the host.
      "historicsuccessfulinteractions": 5,

      // Total amount of time the host has been online.
      "historicuptime": 41634520900246576,

      // List of IP subnet masks used by the host. For IPv4 the /24 and for IPv6 the /54 subnet mask
      // is used. A host can have either one IPv4 or one IPv6 subnet or one of each. E.g. these
      // lists are valid: [ "IPv4" ], [ "IPv6" ] or [ "IPv4", "IPv6" ]. The following lists are 
      // invalid: [ "IPv4", "IPv4" ], [ "IPv4", "IPv6", "IPv6" ]. Hosts with an invalid list are ignored.
      "ipnets": [
        "1.2.3.0",
        "2.1.3.0"
      ],

      // The last time that the interactions within scanhistory have been compressed into the historic ones
      "lasthistoricupdate": 174900, // blocks

      // The last time the list of IP subnet masks was updated. When equal subnet masks are found for
      // different hosts, the host that occupies the subnet mask for a longer time is preferred.
      "lastipnetchange": "2015-01-01T08:00:00.000000000+04:00",

      // The maximum amount of collateral that the host will put into a
      // single file contract.
      "maxcollateral": "1000000000000000000000000000", // hastings

      // Maximum number of bytes that the host will allow to be requested by a
      // single download request.
      "maxdownloadbatchsize": 17825792, // bytes

      // Maximum duration in blocks that a host will allow for a file contract.
      // The host commits to keeping files for the full duration under the
      // threat of facing a large penalty for losing or dropping data before
      // the duration is complete. The storage proof window of an incoming file
      // contract must end before the current height + maxduration.
      //
      // There is a block approximately every 10 minutes.
      // e.g. 1 day = 144 blocks
      "maxduration": 25920, // blocks

      // Maximum size in bytes of a single batch of file contract
      // revisions. Larger batch sizes allow for higher throughput as there is
      // significant communication overhead associated with performing a batch
      // upload.
      "maxrevisebatchsize": 17825792, // bytes

      // Remote address of the host. It can be an IPv4, IPv6, or hostname,
      // along with the port. IPv6 addresses are enclosed in square brackets.
      "netaddress": "123.456.789.0:9982",

      // Public key used to identify and verify hosts.
      "publickey": {
        // Algorithm used for signing and verification. Typically "ed25519".
        "algorithm": "ed25519",

        // Key used to verify signed host messages.
        "key": "RW50cm9weSBpc24ndCB3aGF0IGl0IHVzZWQgdG8gYmU="
      },

      // The string representation of the full public key, used when calling
      // /hostdb/hosts.
      "publickeystring": "ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",

      // Number of recent failed interactions with the host.
      "recentfailedinteractions": 0,

      // Number of recent successful interactions with the host.
      "recentsuccessfulinteractions": 0,

      // Unused storage capacity the host claims it has.
      "remainingstorage": 35000000000, // bytes

      // The revision number indicates to the renter what iteration of
      // settings the host is currently at. Settings are generally signed.
      // If the renter has multiple conflicting copies of settings from the
      // host, the renter can expect the one with the higher revision number
      // to be more recent.
      "revisionnumber": 12733798,

      // Measurements that have been taken on the host. The most recent measurements
      // are kept in full detail.
      "scanhistory": [
        {
          "success": true,
          "timestamp": "2018-09-23T08:00:00.000000000+04:00"
        },
        {
          "success": true,
          "timestamp": "2018-09-23T06:00:00.000000000+04:00"
        },
        {
          "success": true,
          "timestamp": "2018-09-23T04:00:00.000000000+04:00"
        }
      ],

      // Smallest amount of data in bytes that can be uploaded or downloaded to
      // or from the host.
      "sectorsize": 4194304, // bytes

      // The price that a renter has to pay to store files with the host.
      "storageprice": "14000000000", // hastings / byte / block

      // Total amount of storage capacity the host claims it has.
      "totalstorage": 35000000000, // bytes

      // Address at which the host can be paid when forming file contracts.
      "unlockhash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab",

      "uploadbandwidthprice": "3000000000000", // hastings / byte

      // The version of the host.
      "version": "1.3.4",

      // A storage proof window is the number of blocks that the host has to
      // get a storage proof onto the blockchain. The window size is the
      // minimum size of window that the host will accept in a file contract.
      "windowsize": 144 // blocks
    }
  ]
}
```

#### /hostdb/hosts/___:pubkey___ [GET] [(example)](#hosts)

fetches detailed information about a particular host, including metrics
regarding the score of the host within the database. It should be noted that
each renter uses different metrics for selecting hosts, and that a good score on
in one hostdb does not mean that the host will be successful on the network
overall.

###### Path Parameters
```
// The public key of the host. Each public key identifies a single host.
//
// Example Pubkey: ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef
:pubkey
```

###### JSON Response
```javascript
{
  "entry": {
    // true if the host is accepting new contracts.
    "acceptingcontracts": true,

    // The maximum amount of money that the host will put up as collateral
    // for storage that is contracted by the renter
    "collateral": "20000000000", // hastings / byte / block

    // The price that a renter has to pay to create a contract with the
    // host. The payment is intended to cover transaction fees
    // for the file contract revision and the storage proof that the host
    // will be submitting to the blockchain.
    "contractprice": "1000000000000000000000000", // hastings

    // The price that a renter has to pay when downloading data from the
    // host
    "downloadbandwidthprice": "35000000000000", // hastings / byte

    // Firstseen is the last block height at which this host was announced.
    "firstseen": 160000, // blocks

    // Total amount of time the host has been offline.
    "historicdowntime": 0,

    // Number of historic failed interactions with the host.
    "historicfailedinteractions": 0,

    // Number of historic successful interactions with the host.
    "historicsuccessfulinteractions": 5,

    // Total amount of time the host has been online.
    "historicuptime": 41634520900246576,

    // List of IP subnet masks used by the host. For IPv4 the /24 and for IPv6 the /54 subnet mask
    // is used. A host can have either one IPv4 or one IPv6 subnet or one of each. E.g. these
    // lists are valid: [ "IPv4" ], [ "IPv6" ] or [ "IPv4", "IPv6" ]. The following lists are 
    // invalid: [ "IPv4", "IPv4" ], [ "IPv4", "IPv6", "IPv6" ]. Hosts with an invalid list are ignored.
    "ipnets": [
      "1.2.3.0",
      "2.1.3.0"
    ],

    // The last time that the interactions within scanhistory have been compressed into the historic ones
    "lasthistoricupdate": 174900, // blocks

    // The last time the list of IP subnet masks was updated. When equal subnet masks are found for
    // different hosts, the host that occupies the subnet mask for a longer time is preferred.
    "lastipnetchange": "2015-01-01T08:00:00.000000000+04:00",

    // The maximum amount of collateral that the host will put into a
    // single file contract.
    "maxcollateral": "1000000000000000000000000000", // hastings

    // Maximum number of bytes that the host will allow to be requested by a
    // single download request.
    "maxdownloadbatchsize": 17825792, // bytes

    // Maximum duration in blocks that a host will allow for a file contract.
    // The host commits to keeping files for the full duration under the
    // threat of facing a large penalty for losing or dropping data before
    // the duration is complete. The storage proof window of an incoming file
    // contract must end before the current height + maxduration.
    //
    // There is a block approximately every 10 minutes.
    // e.g. 1 day = 144 blocks
    "maxduration": 25920, // blocks

    // Maximum size in bytes of a single batch of file contract
    // revisions. Larger batch sizes allow for higher throughput as there is
    // significant communication overhead associated with performing a batch
    // upload.
    "maxrevisebatchsize": 17825792, // bytes

    // Remote address of the host. It can be an IPv4, IPv6, or hostname,
    // along with the port. IPv6 addresses are enclosed in square brackets.
    "netaddress": "123.456.789.0:9982",

    // Public key used to identify and verify hosts.
    "publickey": {
      // Algorithm used for signing and verification. Typically "ed25519".
      "algorithm": "ed25519",

      // Key used to verify signed host messages.
      "key": "RW50cm9weSBpc24ndCB3aGF0IGl0IHVzZWQgdG8gYmU="
    },

    // The string representation of the full public key, used when calling
    // /hostdb/hosts.
    "publickeystring": "ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",

    // Number of recent failed interactions with the host.
    "recentfailedinteractions": 0,

    // Number of recent successful interactions with the host.
    "recentsuccessfulinteractions": 0,

    // Unused storage capacity the host claims it has.
    "remainingstorage": 35000000000, // bytes

    // The revision number indicates to the renter what iteration of
    // settings the host is currently at. Settings are generally signed.
    // If the renter has multiple conflicting copies of settings from the
    // host, the renter can expect the one with the higher revision number
    // to be more recent.
    "revisionnumber": 12733798,

    // Measurements that have been taken on the host. The most recent measurements
    // are kept in full detail.
    "scanhistory": [
      {
        "success": true,
        "timestamp": "2018-09-23T08:00:00.000000000+04:00"
      },
      {
        "success": true,
        "timestamp": "2018-09-23T06:00:00.000000000+04:00"
      },
      {
        "success": true,
        "timestamp": "2018-09-23T04:00:00.000000000+04:00"
      }
    ],

    // Smallest amount of data in bytes that can be uploaded or downloaded to
    // or from the host.
    "sectorsize": 4194304, // bytes

    // The price that a renter has to pay to store files with the host.
    "storageprice": "14000000000", // hastings / byte / block

    // Total amount of storage capacity the host claims it has.
    "totalstorage": 35000000000, // bytes

    // Address at which the host can be paid when forming file contracts.
    "unlockhash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab",

    "uploadbandwidthprice": "3000000000000", // hastings / byte

    // The version of the host.
    "version": "1.3.4",

    // A storage proof window is the number of blocks that the host has to
    // get a storage proof onto the blockchain. The window size is the
    // minimum size of window that the host will accept in a file contract.
    "windowsize": 144 // blocks
  },

  // A set of scores as determined by the renter. Generally, the host's final
  // final score is all of the values multiplied together. Modified renters may
  // have additional criteria that they use to judge a host, or may ignore
  // certin criteia. In general, these fields should only be used as a loose
  // guide for the score of a host, as every renter sees the world differently
  // and uses different metrics to evaluate hosts.
  "scorebreakdown": {
    // The multiplier that gets applied to the host based on how long it has
    // been a host. Older hosts typically have a lower penalty.
    "ageadjustment": 0.1234,

    // The multiplier that gets applied to the host based on how much
    // proof-of-burn the host has performed. More burn causes a linear increase
    // in score.
    "burnadjustment": 23.456,

    // The multiplier that gets applied to a host based on how much collateral
    // the host is offering. More collateral is typically better, though above
    // a point it can be detrimental.
    "collateraladjustment": 23.456,

    // conversionrate is the likelihood that the host will be selected 
    // by renters forming contracts.
    "conversionrate": 9.12345,

    // The multipler that gets applied to a host based on previous interactions
    // with the host. A high ratio of successful interactions will improve this
    // hosts score, and a high ratio of failed interactions will hurt this
    // hosts score. This adjustment helps account for hosts that are on
    // unstable connections, don't keep their wallets unlocked, ran out of
    // funds, etc.
    "interactionadjustment": 0.1234,

    // The multiplier that gets applied to a host based on the host's price.
    // Lower prices are almost always better. Below a certain, very low price,
    // there is no advantage.
    "pricesmultiplier": 0.1234,

    // The overall score for the host. Scores are entriely relative, and are
    // consistent only within the current hostdb. Between different machines,
    // different configurations, and different versions the absolute scores for
    // a given host can be off by many orders of magnitude. When displaying to a
    // human, some form of normalization with respect to the other hosts (for
    // example, divide all scores by the median score of the hosts) is
    // recommended.
    "score": 123456,

    // The multiplier that gets applied to a host based on how much storage is
    // remaining for the host. More storage remaining is better, to a point.
    "storageremainingadjustment": 0.1234,

    // The multiplier that gets applied to a host based on the uptime percentage
    // of the host. The penalty increases extremely quickly as uptime drops
    // below 90%.
    "uptimeadjustment": 0.1234,

    // The multiplier that gets applied to a host based on the version of Sia
    // that they are running. Versions get penalties if there are known bugs,
    // scaling limitations, performance limitations, etc. Generally, the most
    // recent version is always the one with the highest score.
    "versionadjustment": 0.1234
  }
}
```
#### /hostdb/listmode [POST] 

lets you enable and disable `blacklist` mode and `whitelist` mode. In
`blacklist` mode, any hosts you identify as being on the `blacklist` will not be
used to form contracts. In `whitelist` mode, only the hosts identified as being
on the `whitelist` will be used to form contracts. To enable the `blacklist`,
submit a list of host pubkeys that you wish to black list and set the
`mode` parameter to `blacklist`. To enable the `whitelist` mode, submit the list
of host pubkeys that you wish to white list and set the `mode` parameter to
`whitelist`. To disable either list, submit an empty list of hosts with the
`mode` parameter set to `disable`.

###### Query String Parameters 1
```
// Set to blacklist or whitelist to enable either, set to disable to disable either mode
mode    // string

// This is a list of the host pubkeys that will either be black listed or white
// listed. Pubkeys should be comma separated. To disable either mode, leave hosts
// blank.
hosts   // string of comma separated pubkeys
// Example Pubkey: ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef
```

###### Response
standard success or error response. See
[#standard-responses](#standard-responses).

Examples
--------

#### HostDB Get

###### Request
```
/hostdb
```

###### Expected Response Code
```
200 OK
```

###### Example JSON Response
```javascript
{
    "initialscancomplete": false
}
```

#### Active hosts

###### Request
```
/hostdb/active?numhosts=2
```

###### Expected Response Code
```
200 OK
```

###### Example JSON Response
```javascript
{
  "hosts": [
    {
      "acceptingcontracts": true,
      "collateral": "20000000000",
      "contractprice": "1000000000000000000000000",
      "downloadbandwidthprice": "35000000000000",
      "firstseen": 160000,
      "historicdowntime": 0,
      "historicfailedinteractions": 0,
      "historicsuccessfulinteractions": 5,
      "historicuptime": 41634520900246576,
      "lasthistoricupdate": 174900,
      "maxcollateral": "1000000000000000000000000000",
      "maxdownloadbatchsize": 17825792,
      "maxduration": 25920,
      "maxrevisebatchsize": 17825792,
      "netaddress": "123.456.789.0:9982",
      "publickey": {
        "algorithm": "ed25519",
        "key": "RW50cm9weSBpc24ndCB3aGF0IGl0IHVzZWQgdG8gYmU="
      },
      "publickeystring": "ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
      "recentfailedinteractions": 0,
      "recentsuccessfulinteractions": 0,
      "remainingstorage": 35000000000,
      "revisionnumber": 12733798,
      "scanhistory": [
        {
          "success": true,
          "timestamp": "2018-09-23T08:00:00.000000000+04:00"
        },
        {
          "success": true,
          "timestamp": "2018-09-23T06:00:00.000000000+04:00"
        },
        {
          "success": true,
          "timestamp": "2018-09-23T04:00:00.000000000+04:00"
        }
      ],
      "sectorsize": 4194304,
      "storageprice": "14000000000",
      "totalstorage": 35000000000,
      "unlockhash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab",
      "uploadbandwidthprice": "3000000000000",
      "version": "1.3.4",
      "windowsize": 144
    },
    {
      "acceptingcontracts": true,
      "collateral": "20000000000",
      "contractprice": "1000000000000000000000000",
      "downloadbandwidthprice": "35000000000000",
      "firstseen": 160000,
      "historicdowntime": 0,
      "historicfailedinteractions": 0,
      "historicsuccessfulinteractions": 5,
      "historicuptime": 41634520900246576,
      "lasthistoricupdate": 174900,
      "maxcollateral": "1000000000000000000000000000",
      "maxdownloadbatchsize": 17825792,
      "maxduration": 25920,
      "maxrevisebatchsize": 17825792,
      "netaddress": "123.456.789.0:9982",
      "publickey": {
        "algorithm": "ed25519",
        "key": "RW50cm9weSBpc24ndCB3aGF0IGl0IHVzZWQgdG8gYmU="
      },
      "publickeystring": "ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
      "recentfailedinteractions": 0,
      "recentsuccessfulinteractions": 0,
      "remainingstorage": 35000000000,
      "revisionnumber": 12733798,
      "scanhistory": [
        {
          "success": true,
          "timestamp": "2018-09-23T08:00:00.000000000+04:00"
        },
        {
          "success": true,
          "timestamp": "2018-09-23T06:00:00.000000000+04:00"
        },
        {
          "success": true,
          "timestamp": "2018-09-23T04:00:00.000000000+04:00"
        }
      ],
      "sectorsize": 4194304,
      "storageprice": "14000000000",
      "totalstorage": 35000000000,
      "unlockhash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab",
      "uploadbandwidthprice": "3000000000000",
      "version": "1.3.4",
      "windowsize": 144
    }
  ]
}
```

#### All hosts

###### Request
```
/hostdb/all
```

###### Expected Response Code
```
200 OK
```

###### Example JSON Response
```javascript
{
  "hosts": [
    {
      "acceptingcontracts": true,
      "collateral": "20000000000",
      "contractprice": "1000000000000000000000000",
      "downloadbandwidthprice": "35000000000000",
      "firstseen": 160000,
      "historicdowntime": 0,
      "historicfailedinteractions": 0,
      "historicsuccessfulinteractions": 5,
      "historicuptime": 41634520900246576,
      "lasthistoricupdate": 174900,
      "maxcollateral": "1000000000000000000000000000",
      "maxdownloadbatchsize": 17825792,
      "maxduration": 25920,
      "maxrevisebatchsize": 17825792,
      "netaddress": "123.456.789.0:9982",
      "publickey": {
        "algorithm": "ed25519",
        "key": "RW50cm9weSBpc24ndCB3aGF0IGl0IHVzZWQgdG8gYmU="
      },
      "publickeystring": "ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
      "recentfailedinteractions": 0,
      "recentsuccessfulinteractions": 0,
      "remainingstorage": 35000000000,
      "revisionnumber": 12733798,
      "scanhistory": [
        {
          "success": true,
          "timestamp": "2018-09-23T08:00:00.000000000+04:00"
        },
        {
          "success": true,
          "timestamp": "2018-09-23T06:00:00.000000000+04:00"
        },
        {
          "success": true,
          "timestamp": "2018-09-23T04:00:00.000000000+04:00"
        }
      ],
      "sectorsize": 4194304,
      "storageprice": "14000000000",
      "totalstorage": 35000000000,
      "unlockhash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab",
      "uploadbandwidthprice": "3000000000000",
      "version": "1.3.4",
      "windowsize": 144
    },
    {
      "acceptingcontracts": true,
      "collateral": "20000000000",
      "contractprice": "1000000000000000000000000",
      "downloadbandwidthprice": "35000000000000",
      "firstseen": 160000,
      "historicdowntime": 0,
      "historicfailedinteractions": 0,
      "historicsuccessfulinteractions": 5,
      "historicuptime": 41634520900246576,
      "lasthistoricupdate": 174900,
      "maxcollateral": "1000000000000000000000000000",
      "maxdownloadbatchsize": 17825792,
      "maxduration": 25920,
      "maxrevisebatchsize": 17825792,
      "netaddress": "123.456.789.0:9982",
      "publickey": {
        "algorithm": "ed25519",
        "key": "RW50cm9weSBpc24ndCB3aGF0IGl0IHVzZWQgdG8gYmU="
      },
      "publickeystring": "ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
      "recentfailedinteractions": 0,
      "recentsuccessfulinteractions": 0,
      "remainingstorage": 35000000000,
      "revisionnumber": 12733798,
      "scanhistory": [
        {
          "success": true,
          "timestamp": "2018-09-23T08:00:00.000000000+04:00"
        },
        {
          "success": true,
          "timestamp": "2018-09-23T06:00:00.000000000+04:00"
        },
        {
          "success": true,
          "timestamp": "2018-09-23T04:00:00.000000000+04:00"
        }
      ],
      "sectorsize": 4194304,
      "storageprice": "14000000000",
      "totalstorage": 35000000000,
      "unlockhash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab",
      "uploadbandwidthprice": "3000000000000",
      "version": "1.3.4",
      "windowsize": 144
    },
    {
      "acceptingcontracts": true,
      "collateral": "20000000000",
      "contractprice": "1000000000000000000000000",
      "downloadbandwidthprice": "35000000000000",
      "firstseen": 160000,
      "historicdowntime": 0,
      "historicfailedinteractions": 0,
      "historicsuccessfulinteractions": 5,
      "historicuptime": 41634520900246576,
      "lasthistoricupdate": 174900,
      "maxcollateral": "1000000000000000000000000000",
      "maxdownloadbatchsize": 17825792,
      "maxduration": 25920,
      "maxrevisebatchsize": 17825792,
      "netaddress": "123.456.789.0:9982",
      "publickey": {
        "algorithm": "ed25519",
        "key": "RW50cm9weSBpc24ndCB3aGF0IGl0IHVzZWQgdG8gYmU="
      },
      "publickeystring": "ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
      "recentfailedinteractions": 0,
      "recentsuccessfulinteractions": 0,
      "remainingstorage": 35000000000,
      "revisionnumber": 12733798,
      "scanhistory": [
        {
          "success": true,
          "timestamp": "2018-09-23T08:00:00.000000000+04:00"
        },
        {
          "success": true,
          "timestamp": "2018-09-23T06:00:00.000000000+04:00"
        },
        {
          "success": true,
          "timestamp": "2018-09-23T04:00:00.000000000+04:00"
        }
      ],
      "sectorsize": 4194304,
      "storageprice": "14000000000",
      "totalstorage": 35000000000,
      "unlockhash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab",
      "uploadbandwidthprice": "3000000000000",
      "version": "1.3.4",
      "windowsize": 144
    }
  ]
}
```

#### Hosts

###### Request
```
/hostdb/hosts/ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef
```

###### Expected Response Code
```
200 OK
```

###### Example JSON Response
```javascript
{
  "entry": {
    "acceptingcontracts": true,
    "collateral": "20000000000",
    "contractprice": "1000000000000000000000000",
    "downloadbandwidthprice": "35000000000000",
    "firstseen": 160000,
    "historicdowntime": 0,
    "historicfailedinteractions": 0,
    "historicsuccessfulinteractions": 5,
    "historicuptime": 41634520900246576,
    "lasthistoricupdate": 174900,
    "maxcollateral": "1000000000000000000000000000", 
    "maxdownloadbatchsize": 17825792,
    "maxduration": 25920,
    "maxrevisebatchsize": 17825792,
    "netaddress": "123.456.789.0:9982",
    "publickey": {
      "algorithm": "ed25519",
      "key": "RW50cm9weSBpc24ndCB3aGF0IGl0IHVzZWQgdG8gYmU="
    },
    "publickeystring": "ed25519:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
    "recentfailedinteractions": 0,
    "recentsuccessfulinteractions": 0,
    "remainingstorage": 35000000000,
    "revisionnumber": 12733798,
    "scanhistory": [
      {
        "success": true,
        "timestamp": "2018-09-23T08:00:00.000000000+04:00"
      },
      {
        "success": true,
        "timestamp": "2018-09-23T06:00:00.000000000+04:00"
      },
      {
        "success": true,
        "timestamp": "2018-09-23T04:00:00.000000000+04:00"
      }
    ],
    "sectorsize": 4194304,
    "storageprice": "14000000000",
    "totalstorage": 35000000000,
    "unlockhash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab",
    "uploadbandwidthprice": "3000000000000", 
    "version": "1.3.4",
    "windowsize": 144
  },
  "scorebreakdown": {
    "ageadjustment": 0.1234,
    "burnadjustment": 23.456,
    "collateraladjustment": 23.456,
    "conversionrate": 9.12345,
    "interactionadjustment": 0.1234,
    "pricesmultiplier": 0.1234,
    "score": 123456,
    "storageremainingadjustment": 0.1234,
    "uptimeadjustment": 0.1234,
    "versionadjustment": 0.1234
  }
}
```
