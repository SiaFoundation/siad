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

Siac Command Output Testing
===========================

New type of testing siac command line commands is now available from go tests.

Siac is using [Cobra](https://github.com/spf13/cobra) golang library to
generate command line commands (and subcommands) interface. In
`cmd/siac/main.go` file root siac Cobra command with all subcommands is created
using `initCmds()`, siac/siad node instance specific flags of siac commands are
initialized using `initClient(...)`.

## Test Group Structure

Pseudo code example of a test group:

```
func TestGroup() {
    // Create test inputs
    create test node
    init Cobra command with subcommands and flags
    create regex pattern constants

    // Create subtests
    define subtests

    // Execute subtests
    run subtests
}
```

## Test Inputs

The most of the siac tests require running instance of `siad` to execute the
tests against. A new instance of `siad` can be created using `newTestNode`.
Note that some of the `siac` tests don't require running an instance of `siad`.
This is the case when we're testing unknown `siac` subcommand or an unknown
command/subcommand flag for example, because these error cases are handled by
Cobra library itself.

Before testing siac Cobra command(s), siac Cobra command with its subcommands
and flags must be built and initialized. This is done by
`getRootCmdForSiacCmdsTests()` helper function.

## Subtests

Subtests are defined using `siacCmdSubTest` struct:

```
type siacCmdSubTest struct {
	name               string
	test               siacCmdTestFn
	cmd                *cobra.Command
	cmdStrs            []string
	expectedOutPattern string
}
```

### name

`name` is the name of a subtest to appear in report.

### test

`test` is a subtest helper function that executes subtest.

### cmd

`cmd` is an initialized root Cobra command with all subcommands and flags.

### cmdStrs

`cmdStrs` is a list of string values you would normally enter to the command
line, but without leading `siac` and each space between command, subcommand(s),
flag(s) or parameter(s) starting a new string in a list.

Examples:

|CLI command|cmdStrs|
|---|---|
|./siac|cmdStrs: []string{},|
|./siac -h|cmdStrs: []string{"-h"},|
|./siac --address localhost:5555|cmdStrs: []string{"--address", "localhost:5555"},|
|./siac renter --address localhost:5555|cmdStrs: []string{"renter", "--address", "localhost:5555"},|

### expectedOutPattern

`expectedOutPattern` is expected regex pattern string to test actual output
against. It can be a multiline string to test complete output from beginning
(starting with `^`) till end (ending with `$`) or just a smaller pattern
testing multiple lines, a single line or just a part of a line in the complete
output.

Note that each siac command handler has to be prepared for these tests, for
more information see [below](#preparation-of-command-handler-for-cobra-Output-tests).

## Errors

In case of failure in the executed subtest, error log output from
`testGenericSiacCmd()` in `cmd/siac/helpers_test.go` will include the following 5 items:

* Regex pattern didn't match between row x, and row y
* Regex pattern part that didn't match
* ----- Expected output pattern: -----
* ----- Actual Cobra output: -----
* ----- Actual Sia output: -----

Error log example with 5 above items (part `...` of the message is cut):

```
=== RUN   TestRootSiacCmd
=== RUN   TestRootSiacCmd/TestRootCmdWithShortAddressFlagIPv6
--- FAIL: TestRootSiacCmd (2.18s)
    maincmd_test.go:28: siad API address: [::]:35103
    --- FAIL: TestRootSiacCmd/TestRootCmdWithShortAddressFlagIPv6 (0.02s)
        helpers_test.go:141: Regex pattern didn't match between row 5, and row 5
        helpers_test.go:142: Regex pattern part that didn't match:
            Wallet XXX:
        helpers_test.go:150: ----- Expected output pattern: -----
        helpers_test.go:151: ^Consensus:
              Synced: (No|Yes)
              Height: [\d]+
            
            Wallet XXX:
            (  Status: Locked|  Status:          unlocked
              Siacoin Balance: [\d]+(\.[\d]*|) (SC|KS|MS))
            ...
            $
        helpers_test.go:153: ----- Actual Cobra output: -----
        helpers_test.go:154: 
        helpers_test.go:156: ----- Actual Sia output: -----
        helpers_test.go:157: Consensus:
              Synced: Yes
              Height: 14
            
            Wallet:
              Status:          unlocked
              Siacoin Balance: 3.3 MS
            ...
        helpers_test.go:159: 
FAIL
coverage: 5.3% of statements
FAIL	gitlab.com/NebulousLabs/Sia/cmd/siac	2.242s
FAIL
```

Expected output regex pattern can have multiple lines and because spotting
errors in complex regex pattern matching can be difficult `testGenericSiacCmd`
tests in a for loop at first only the first line of the regex pattern, then
first 2 lines of the regex pattern, adding one more line each iteration. If
there is a regex pattern match error, it prints the line number of the regex
that didn't match. E.g. there is a 20 line of expected regex pattern, it passed
to test first 11 lines of regex but fails to match when first 12 lines are
matched against, it prints that it failed to match line 12 of regex pattern and
prints the content of 12th line.

Then it prints the complete expected regex pattern and actual Cobra output and
actual siac output. There are two actual outputs, because unknown subcommands,
unknown flags and command/subcommand help requests are handled by Cobra
library, while the rest is the output written to stdout by siac command
handlers.

## Examples

First examples of siac Cobra command tests are tests located in
`cmd/siac/maincmd_test.go` file in `TestRootSiacCmd` test group, helpers for
these tests are located in `cmd/siac/helpers_test.go` file.

Simplified example code:

```
func TestRootSiacCmd(t *testing.T) {
    ...
    n, err := newTestNode(groupDir)
    ...

    root := getRootCmdForSiacCmdsTests(t, groupDir)
    ...
    regexPatternConstantX := "..."
    ...
    subTests := []siacCmdSubTest{
        {
            name:               "TestRootCmdWithShortAddressFlagIPv6",
            test:               testGenericSiacCmd,
            cmd:                root,
            cmdStrs:            []string{"-a", IPv6addr},
            expectedOutPattern: regexPatternConstantX,
        },
        ...
    }

    err = runSiacCmdSubTests(t, subTests)
    ...
}
```
