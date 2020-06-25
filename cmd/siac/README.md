Siac Usage
==========

`siac` is the command line interface to Sia, for use by power users and those on
headless servers. It comes as a part of the command line package, and can be run
as `./siac` from the same folder, or just by calling `siac` if you move the
binary into your path.

Most of the following commands have online help. For example, executing `siac
wallet send help` will list the arguments for that command, while `siac host
help` will list the commands that can be called pertaining to hosting. `siac
help` will list all of the top level command groups that can be used.

You can change the address of where siad is pointing using the `-a` flag. For
example, `siac -a :9000 status` will display the status of the siad instance
launched on the local machine with `siad -a :9000`.

Common tasks
------------
* `siac consensus` view block height

Wallet:
* `siac wallet init [-p]` initialize a wallet
* `siac wallet unlock` unlock a wallet
* `siac wallet balance` retrieve wallet balance
* `siac wallet address` get a wallet address
* `siac wallet send [amount] [dest]` sends siacoin to an address

Renter:
* `siac renter ls` list all renter files and subdirectories
* `siac renter upload [filepath] [nickname]` upload a file
* `siac renter download [nickname] [filepath]` download a file
* `siac renter workers` show worker status
* `siac renter workers ea` show worker account status
* `siac renter workers pt` show worker price table status
* `siac renter workers rj` show worker read jobs status
* `siac renter workers hsj` show worker has sector jobs status


Full Descriptions
-----------------

### Consensus tasks

* `siac consensus` prints the current block ID, current block height, and
  current target.

### Daemon tasks

* `siac stop` sends the stop signal to siad to safely terminate. This has the
  same affect as C^c on the terminal.

* `siac update` checks the server for updates.

* `siac version` displays the version string of siac.

### FeeManager tasks

* `siac feemanager` prints info about the feemanager such as pending fees and
  the next fee payout height.

* `siac feemanager cancel <feeUID>` cancels a pending fee. If a transaction has
  already been created the fee cannot be cancelled.

### Gateway tasks

* `siac gateway` prints info about the gateway, including its address and how
  many peers it's connected to.

* `siac gateway connect [address:port]` manually connects to a peer and adds it
  to the gateway's node list.

* `siac gateway disconnect [address:port]` manually disconnects from a peer, but
  leaves it in the gateway's node list.

* `siac gateway list` prints a list of all currently connected peers.

### Host tasks

* `siac host -v` outputs some of your hosting settings.

Example:
```bash
user@hostname:~$ siac host -v
Host settings:
Storage:      2.0000 TB (1.524 GB used)
Price:        0.000 SC per GB per month
Collateral:   0
Max Filesize: 10000000000
Max Duration: 8640
Contracts:    32
```

* `siac host announce` makes an host announcement. You may optionally supply
  a specific address to be announced; this allows you to announce a domain name.
Announcing a second time after changing settings is not necessary, as the
announcement only contains enough information to reach your host.

* `siac host config [setting] [value]` is used to configure hosting.

In version `1.4.3.0`, sia hosting is configured as follows:

| Setting                    | Value                                           |
| ---------------------------|-------------------------------------------------|
| acceptingcontracts         | Yes or No                                       |
| collateral                 | in SC / TB / Month, 10-1000                     |
| collateralbudget           | in SC                                           |
| ephemeralaccountexpiry     | in seconds                                      |
| maxcollateral              | in SC, max per contract                         |
| maxduration                | in weeks, at least 12                           |
| maxephemeralaccountbalance | in SC                                           |
| maxephemeralaccountrisk    | in SC                                           |
| mincontractprice           | minimum price in SC per contract                |
| mindownloadbandwidthprice  | in SC / TB                                      |
| minstorageprice            | in SC / TB                                      |
| minuploadbandwidthprice    | in SC / TB                                      |

You can call this many times to configure you host before announcing.
Alternatively, you can manually adjust these parameters inside the
`host/config.json` file.

### HostDB tasks

* `siac hostdb -v` prints a list of all the know active hosts on the network.

### Miner tasks

* `siac miner start` starts running the CPU miner on one thread. This is
  virtually useless outside of debugging.

* `siac miner status` returns information about the miner. It is only valid for
  when siad is running.

* `siac miner stop` halts the CPU miner.

### Renter tasks

* `siac renter allowance` views the current allowance, which controls how much
  money is spent on file contracts.

* `siac renter delete [nickname]` removes a file from your list of stored files.
  This does not remove it from the network, but only from your saved list.

* `siac renter download [nickname] [destination]` downloads a file from the sia
  network onto your computer. `nickname` is the name used to refer to your file
in the sia network, and `destination` is the path to where the file will be. If
a file already exists there, it will be overwritten.

* `siac renter ls` displays a list of uploaded files and subdirectories
  currently on the sia network by nickname, and their filesizes.

* `siac renter queue` shows the download queue. This is only relevant if you
  have multiple downloads happening simultaneously.

* `siac renter rename [nickname] [newname]` changes the nickname of a file.

* `siac renter setallowance` sets the amount of money that can be spent over
  a given period. If no flags are set you will be walked through the interactive
allowance setting. To update only certain fields, pass in those values with the
corresponding field flag, for example '--amount 500SC'.

* `siac renter upload [filename] [nickname]` uploads a file to the sia network.
  `filename` is the path to the file you want to upload, and nickname is what
you will use to refer to that file in the network. For example, it is common to
have the nickname be the same as the filename.

* `siac renter workers` shows a detailed overview of all workers. It shows
  information about their accounts, contract and download and upload status.

* `siac renter workers ea` shows a detailed overview of the workers' ephemeral
  account statuses, such as balance information, whether its on cooldown or not
  and potentially the most recent error.

* `siac renter workers pt` shows a detailed overview of the workers's price table
  statuses, such as when it was updated, when it expires, whether its on cooldown
  or not and potentially the most recent error.

* `siac renter workers rj` shows information about the read jobs queue. How many
  jobs are in the queue and their average completion time. In case there was an
  error it will also display the most recent error and when it occurred.

* `siac renter workers hsj` shows information about the has sector jobs queue.
  How many jobs are in the queue and their average completion time. In case
  there was an error it will also display the most recent error and when it
  occurred.

### Skykey tasks
TODO - Fill in

### Skynet tasks

* `siac skynet blacklist [skylink]` will add or remove a skylink from the
  Renter's Skynet Blacklist

* `siac skynet convert [source siaPath] [destination siaPath]` converts
  a siafile to a skyfile and then generates its skylink. A new skylink will be
created in the user's skyfile directory. The skyfile and the original siafile
are both necessary to pin the file and keep the skylink active. The skyfile will
consume an additional 40 MiB of storage.

* `siac skynet download [skylink] [destination]` downloads a file from Skynet
  using a skylink.

* `siac skynet ls` lists all skyfiles and subdirectories that the user has
  pinned along with the corresponding skylinks. By default, only files in
var/skynet/ will be displayed. Files that are not tracking skylinks are not
counted.

* `siac skynet pin [skylink] [destination siapath]` pins the file associated
  with this skylink by re-uploading an exact copy. This ensures that the file
will still be available on skynet as long as you continue maintaining the file
in your renter.

* `siac skynet unpin [siapath]` unpins one or more skyfiles or directories,
  deleting them from your list of stored files or directories.

* `siac skynet upload [source filepath] [destination siapath]` uploads a file or
  directory to Skynet. A skylink will be produced for each file. The link can be
shared and used to retrieve the file. The file(s) that get uploaded will be
pinned to this Sia node, meaning that this node will pay for storage and repairs
until the file(s) are manually deleted. If the `silent` flag is provided, `siac`
will not output progress bars during upload.

### Utils tasks
TODO - Fill in

### Wallet tasks

* `siac wallet address` returns a never seen before address for sending siacoins
  to.

* `siac wallet addseed` prompts the user for his encryption password, as well as
  a new secret seed. The wallet will then incorporate this seed into itself.
This can be used for wallet recovery and merging.

* `siac wallet balance` prints information about your wallet.

Example:
```bash
user@hostname:~$ siac wallet balance
Wallet status:
Encrypted, Unlocked
Confirmed Balance:   61516458.00 SC
Unconfirmed Balance: 64516461.00 SC
Exact:               61516457999999999999999999999999 H
```

* `siac wallet init [-p]` encrypts and initializes the wallet. If the `-p` flag
  is provided, an encryption password is requested from the user. Otherwise the
initial seed is used as the encryption password. The wallet must be initialized
and unlocked before any actions can be performed on the wallet.

Examples:
```bash
user@hostname:~$ siac -a :9920 wallet init
Seed is:
 cider sailor incur sober feast unhappy mundane sadness hinder aglow imitate amaze duties arrow gigantic uttered inflamed girth myriad jittery hexagon nail lush reef sushi pastry southern inkling acquire

Wallet encrypted with password: cider sailor incur sober feast unhappy mundane sadness hinder aglow imitate amaze duties arrow gigantic uttered inflamed girth myriad jittery hexagon nail lush reef sushi pastry southern inkling acquire
```

```bash
user@hostname:~$ siac -a :9920 wallet init -p
Wallet password:
Seed is:
 potato haunted fuming lordship library vane fever powder zippers fabrics dexterity hoisting emails pebbles each vampire rockets irony summon sailor lemon vipers foxes oneself glide cylinder vehicle mews acoustic

Wallet encrypted with given password
```

* `siac wallet lock` locks a wallet. After calling, the wallet must be unlocked
  using the encryption password in order to use it further

* `siac wallet seeds` returns the list of secret seeds in use by the wallet.
  These can be used to regenerate the wallet

* `siac wallet send [amount] [dest]` Sends `amount` siacoins to `dest`. `amount`
  is in the form XXXXUU where an X is a number and U is a unit, for example MS,
S, mS, ps, etc. If no unit is given hastings is assumed. `dest` must be a valid
siacoin address.

* `siac wallet unlock` prompts the user for the encryption password to the
  wallet, supplied by the `init` command. The wallet must be initialized and
unlocked before any actions can take place.

