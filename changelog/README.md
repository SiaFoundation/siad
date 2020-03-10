# Changelog Generator

## Why?

To avoid merge conflicts on each Changelog update.  
To simplify generating Changelog.

## How?

- Execute Changelog Generator from `Sia/release-scripts/generate-changelog.sh`
- Generator works in `Sia/changelog` directory
- It starts with copy of `Sia/changelog/changelog-head.md` into `CHANGELOG.md` file 
- It takes all version folders named `Sia/changelog/vX.Y.Z` (eg. `v1.4.4`) in reverse order (latest release first)
  - it renders version header (e.g. `v1.4.4.`) into `CHANGELOG.md`
  - it collects all "Key Updates" `*.md` files from `Sia/changelog/vX.Y.Z/key-updates` directory in alphabetic order
  - it collects all "Bug Fixes" `*.md` files from `Sia/changelog/vX.Y.Z/key-updates` directory in alphabetic order
  - it collects all "Others" `*.md` files from `Sia/changelog/vX.Y.Z/others` directory in alphabetic order
- Finally it appends `Sia/changelog/changelog-tail.md` to `CHANGELOG.md`

## Dos and don't dos

- for changelog item filenames do not use apostroph (`'`) or spaces
