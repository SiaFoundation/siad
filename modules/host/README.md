# Host
The host takes local disk storage and makes it available to the Sia network. It
will do so by announcing itself, and its settings, to the network. Renters
transact with the host through a number of RPC calls.

In order for data to be uploaded and stored, the renter and host must agree on a
file contract. Once they have negotiated the terms of the file contract, it is
signed and put on chain. Any further action related to the data is reflected in
the file contract by means contract revisions that get signed by both parties.
The host is responsible for managing these contracts, and making sure the data
is safely stored. The host proves that it is storing the data by providing a
segment of the file and a list of hashes from the file's merkletree.

Aside from storage, the host offers another service called ephemeral accounts.
These accounts serve as an alternative payment method to file contracts. Users
can deposit money into them and then later use those funds to transact with the
host. The most common transactions are uploading and downloading data, however
any RPC that requires payment will support receiving payment from an ephemeral
account.

## Submodules

This README will provide brief overviews of the submodules, but for more
detailed descriptions of the inner workings of the submodules the respective
README files should be reviewed.

 - [ContractManager](./contractmanager/README.md)

### ContractManager

The ContractManager is responsible for managing contracts that the host has with
renters, including storing the data, submitting storage proofs, and deleting the
data when a contract is complete.

## Subsystems

The Host has the following subsystems that help carry out its responsibilities.
 - [AccountManager Subsystem](#accountmanager-subsystem)
 - [AccountsPersister Subsystem](#accountspersister-subsystem)

### AccountManager Subsystem

**Key Files**
 - [accountmanager.go](./accountmanager.go)

The AccountManager subsystem manages all of the ephemeral accounts on the host.
	
Ephemeral accounts are a service offered by hosts that allow users to connect a
balance to a pubkey. Users can deposit funds into an ephemeral account with a
host and then later use the funds to transact with the host.

The account owner fully entrusts the money with the host, they have no recourse
at all if the host decides to steal the funds. For this reason, users should
only keep tiny balances in ephemeral accounts and users should refill the
ephemeral accounts frequently, even on the order of multiple times per minute.

To increase performance, the host will allow a user to withdraw from an
ephemeral without requiring the user to wait until the host has persisted the
withdrawal to complete a transaction. This allows the user to perform actions
such as downloading with significantly less latency. This also means that if the
host loses power at that exact moment, the host will forget that the user has
spent money and the user will be able to spend that money again. The host can
configure the amount of money it is willing to risk due to this asynchronous
persist model through the `maxunsaveddelta` setting.

If an ephemeral account has been inactive for a period of 7 days, the host will
prune it from the accounts list. This will effectively expire the account, along
with all the money that was associated to it.

### AccountsPersister Subsystem

**Key Files**
 - [accountspersist.go](./accountspersist.go)

The AccountsPersister subsystem will persist all ephemeral account data to disk.
This data consists of two parts, the account balance and the fingerprints.
Fingerprints are derived from the withdrawal message the user used to perform a
withdrawal. They ensure that the same withdrawal can not be withdrawn twice,
thus preventing a replay attack on the host.

The account balances together with the corresponding publickey are persisted in
a single accounts file. The fingerprints are persisted across two files, the
current and the next fingerprint bucket. The expiry blockheight of the
withdrawal message decide if the fingerprint belongs to either the current or
the next bucket.
