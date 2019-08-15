# Transaction Pool
The transaction pool is responsible for tracking the set of unconfirmed
transactions on the network and for broadcasting new transactions out to the
network.

## Subsystems
The transaction pool has the following subsystems.
 - [Core](#core)
 - [New Peer Share](#new-peer-share)

### Core
**Key Files**
 - [accept.go](./accept.go)
 - [database.go](./database.go)
 - [persist.go](./persist.go)
 - [standard.go](./standard.go)
 - [subscribe.go](./subscribe.go)
 - [transactionpool.go](./transactionpool.go)
 - [update.go](./update.go)

The core subsystem contains all of the code that existed in the transaction pool
before we started breaking packages down into subsystems. The core subsystem
should be broken down and separated out into new subsystems.

### New Peer Share
**Key Files**
 - [newpeershare.go](./newpeershare.go)
 - [newpeershareconsts.go](./newpeershareconsts.go)

The new peer share subsystem is responsible for tracking which peers siad is
connected to and sharing the transactions in the transaction pool with that
peer.

The subsystem will not start sending transactions to a new peer until it knows
that it has the most recent blocks on the network. This is to avoid sending the
peer outdated transactions if the transaction pool is still catching up to the
most recent block.

The subsystem will self-ratelimit when sending new transactions to peers. It
will send each transaction set at full speed, but then sleep for some time
between the sending of each set to ensure that the overall data rate for that
peer is reasonable.

##### State Complexities
The new peer share subsystem depends on the `synced` value of the transaction
pool, which needs to be updated every time a new `ConsensusChange` is received
by `ProcessConsensusChange` in [update.go](./update.go).
