# Siac Cobra Command Output Testing

New type of testing siac command line commands is now available from go tests.

Siac is using [Cobra](https://github.com/spf13/cobra) golang library to generate command line commands (and subcommands) interface. In `cmd/siac/main.go` file root siac Cobra command with all subcommands is created using `initCmds()`, siac/siad node instance specific flags of siac commands are initialized using `initClient(...)`.

Each siac Cobra command handler must be prepared for testing. For details see [below](#preparation-of-command-handler-for-cobra-Output-tests).

## Examples of Cobra Output Tests

First examples of siac Cobra command tests are tests located in `cmd/siac/maincmd_test.go` file in `TestRootSiacCmd` test group, helpers for these tests are located in `cmd/siac/helpers_test.go` file.

## Prerequisities

Some of the siac tests do not require running instance of siad, such as testing unknown siac subcommand or unknown command/subcommand flag, because these error cases are handled by Cobra library itself, but most of the siac tests require running instance of siad to execute the tests against. This siad instance can be created using `newTestNode()` in the test group.

Before testing siac Cobra command(s), siac Cobra command with its subcommans and flags must be built and initialized. This is done by `getRootCmdForSiacCmdsTests()` helper function.

## TestRootSiacCmd Subtests

In the example test group `TestRootSiacCmd` subtests are created with a helper struct `siacCmdSubTest`, the struct has 5 fields: 

* name: Name of the subtest
* test: `testGenericSiacCmd` helper function to execute the subtest
* cmd: Built and initialized root siac Cobra command, as described above
* cmdStrs: Command to test, more below
* expectedOutPattern: Regex out pattern to test actual command output against, more below

## Commands to Test

Siac commands to test are defined in `cmdStrs` field. This a list of string values you would normally enter to the command line, but without leading `siac` and each space between command, subcommand(s), flag(s) or parameter(s) starting a new string in a list.

Examples:

|CLI command|cmdStrs|
|---|---|
|./siac|cmdStrs: []string{},|
|./siac -h|cmdStrs: []string{"-h"},|
|./siac --address localhost:5555|cmdStrs: []string{"--address", "localhost:5555"},|
|./siac renter --address localhost:5555|cmdStrs: []string{"renter", "--address", "localhost:5555"},|

Note that each command handler has to be prepared for these tests, for more information see [below](#preparation-of-command-handler-for-cobra-Output-tests).

## Expected Output

Subtests `expectedOutPattern` field contains expected regex pattern string to test actual output against. It can be a multiline string to test complete output from beginning (starting with `^`) till end (ending with `$`) or just a smaller pattern testing multiple lines, a single line or just a part of a line in the complete output.

## Error Logging

There are 5 error log items from `testGenericSiacCmd()` in `cmd/siac/helpers_test.go`:

* Regex pattern didn't match between row x, and row y
* Regex pattern part that didn't match
* ----- Expected output pattern: -----
* ----- Actual Cobra output: -----
* ----- Actual Sia output: -----

For an error log example see [asciinema recording](https://asciinema.org/a/hye45Aakye0eBjDcYJJwOnTgp).

Expected output regex pattern can have multiple lines and because spotting errors in complex regex pattern matching can be difficult `testGenericSiacCmd` tests in a for loop first only the first line of the regex pattern, then first 2 lines of the regex pattern, adding one more line each iteration. If there is a regex pattern match error, it prints the line number of the regex that didn't match. E.g. there is a 20 line of expected regex pattern, it passed to test first 11 lines of regex but fails to match when first 12 lines are matched against, it prints that it failed to match line 12 of regex pattern and prints the content of 12th line.

Then it prints the complete expected regex pattern and actual Cobra output and actual siac output. There are two actual outputs, because unknown subcommands, unknown flags and command/subcommand help requests are handled by Cobra library, while the rest is the output written to stdout by siac command handlers.


## Preparation of Command Handler for Cobra Output Tests

Originally when a siac command handler (e.g. `statuscmd` in `cmd/siac/main.go` for root siac command or `rentercmd` in `cmd/siac/rentercmd.go` for `siac renter` subcommand) encounteres error case, the `die()` function is called from the command handler, error is logged to Stderr and siac exits using `os.Exit()`. Checking of errors (command line output) in tests is not possible.

Thus `die` function have been updated for testing and during testing it doesn't exits, but returns. We then have to update the tested command handler (e.g. `statuscmd` is done already) to `return` from command handler after each `die` call. This allows the tests to continue, and to capture and check expected errors.