# Changelog
The Changelog for the `Sia` repository is managed by this directory in order to
avoid merge conflicts on each Changelog update and to simplify generating
Changelog.

## Changelog Files
<<<<<<< HEAD

In `Sia` repository there is the following directory/file structure
(example with 2 versions, v.1.4.5 being the latest version)

      chengelog
          changelog-head.md
          changelog-tail.md
          README.md
          v1.4.4
              bugs-fixed
                  bug1-filename.md
                  bug2-filename.md
                  bug3-filename.md
                  ...
              key-updates
                  update1-filename.md
                  update2-filename.md
                  ...
              other
                  other1-filename.md
                  other2-filename.md
                  ...
          v1.4.5
              bugs-fixed
                  bug4-filename.md
                  bug5-filename.md
                  bug6-filename.md
                  ...
              key-updates
                  update3-filename.md
                  update4-filename.md
                  ...
              other
                  other3-filename.md
                  other4-filename.md
                  ...**

To add an item to changelog, create an `.md` file at the proper location.

**Notable changes**

- New features and notable product updates should be logged
  under `key-updates` directory.
- Bug fixes from the previous releases should be logged
  under `bugs-fixed` directory.
- Other notable changes that users and developers should know about
  should be logged under `other` directory.

These issues then appear in corresponding section
**Key Updates**, **Bugs Fixed**, resp. **Other** in the generated changelog.

**Format**
- Do not use spaces or apostrophes in the filename
- Inside the .md file use markdown description of the issue
  that will appear in `CHANGELOG.md`
- Optionally we can use numeric prefix if you want to position the issue

## Sort/position the changelog issues

Changelog versions are sorted in reverse version order, i.e. latest version is first.

Changelog items (under `key-updates`, `bugs-fixed`, `other`)
are sorted in ascending alphabetic order by filenames.

Optionally we can use numeric filename prefix to sort items in desired order.

## changelog-head.md and changelog-tail.md

- `changelog-head.md` is the beginning, `changelog-tail.md` is the end of the generated `CHANGELOG.md`.
- Changelog generator will generate changelog issues between these two parts.
- They can be edited and committed to git

## Updating changelog-tail.md with old versions

- Changelog generator can have multiple versions in `changelog` directory.
- However you might want to remove finished versions
  from `changelog` directory to `changelog-tail.md`
- To do this:
  - generate the changelog
  - open generated `CHANGELOG.md`
  - copy the finished version generated `CHANGELOG.md` to `changelog-tail.md`
  - delete the finished version from `changelog` folder
  - commit to git

## Changelog generation

The script is located at `release-scripts/generate-changelog.sh` in `Sia` repo.

The script:

=======
Instead of creating new entries directly in `CHANGELOG.MD`, a new file is
created in this directory that documents the change.

Below is an example of how the files are structured by type and version. In this
example there are 2 versions, v.1.4.5 being the latest version.

    /changelog
        /v1.4.4
            /bugs-fixed
                bug1-filename.md
                bug2-filename.md
                bug3-filename.md
                ...
            /key-updates
                update1-filename.md
                update2-filename.md
                ...
            /other
                other1-filename.md
                other2-filename.md
                ...
        /v1.4.5
            /bugs-fixed
                bug4-filename.md
                bug5-filename.md
                bug6-filename.md
                ...
            /key-updates
                update3-filename.md
                update4-filename.md
                ...
            /other
                other3-filename.md
                other4-filename.md
        changelog-head.md
        changelog-tail.md
        README.md

To add a new changelog item, create an `.md` file at the proper location.

### File Format
When naming changelog files, the following format should be user.
```
Format:
<MR number>-description-string.md

Example:
4230-check-contract-gfr.md
```
It is important to not use spaces or apostrophes in the filename. In the body of
the file, use markdown to write a detailed description of the issue that will
appear in `CHANGELOG.md`.
```
Example Body

- Fixed a bug which caused a call to `build.Critical` in the case that a
  contract in the renew set was marked `!GoodForRenew` while the contractor lock
  was not held

```
To ensure consistent spacing please have a newline at the end of the file.

## Change Types
### Key Updates
Key update are new features and notable product updates. Any key updates should
be added to the version's `key-updates` directory. For new features that require
multiple MRs to complete, only one changelog entry is need and should be
submitted with the first MR.

### Bug Fixes
Any bug fixes from the previous releases should be logged under `bugs-fixed`
directory. If bugs are created and fixed in the same release cycle, no changelog
entry is needed.

### Other
Any other notable changes that users and developers should know about should be
logged under `other` directory. Examples of these would be improves to the build
process, new README files, changes to the CI etc.

## Changelog Generation
### Ordering
Changelog versions are sorted in descending version order.

Changelog items are sorted in ascending alphabetic order by filenames under
their corresponding section **Key Updates**, **Bugs Fixed**, and **Other** in
the generated changelog. Since the filenames are prefixed with the merge request
number, this means the changes in the changelog will roughly follow the order of
development from oldest to newest.

### Creation
To create the updated `CHANGELOG.md` file, use the `generate-changelog.sh`
script in the `/release-scripts` repo.

The script creates the changelog by executing the following steps:
>>>>>>> master
- copies `changelog-head.md` to `CHANGELOG.md`
- generates section header for each found version, latest version first
- generates **Key Updates**, **Bugs Fixed** and **Other** sections for each
  version
- renders all items in filename alphabetic order under it's specific section in
  `CHANGELOG.md`
- and finally appends `changelog-tail.md`

Once generated, the new `CHANGELOG.md` should be pushed as a new merge request
to be merged with master.

### Editing
The Changelog generator can have multiple versions in the `changelog` directory.
Editing any version that currently has a directory in the `/changelog` directory
should follow the above listed process and new changelog files should be created
for any changes.

For any versions that have been moved into the `changelog-tail.md` file, the
`changelog-tail.md` file can be edited directly. Version that have been
officially released and tagged can have their `/changelog` directory removed and
all changes added directly to `changelog-tail.md`.
