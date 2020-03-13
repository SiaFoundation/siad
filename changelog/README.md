# Changelog


To avoid merge conflicts on each Changelog update.  
To simplify generating Changelog.

## Changelog Files

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
  should be loged under `other` directory.

These issues then appear in corresponding section
**Key Updates**, **Bugs Fixed**, resp. **Other** in the generated changelog.

**Format**
- Do not use spaces or apostrophes in the filename
- Inside the .md file use markdown description of the issue
  that will appear in `CHANGELOG.md`
- Use numeric prefix if you want to position the issue

## Sort/position the changelog issues

Changelog versions are sorted in reverse version order, i.e. latest version is first.

Changelog items (under `key-updates`, `bugs-fixed`, `other`)
are sorted in ascending alphabetic order by filenames.

We can use numeric filename prefix to sort items in desired order.

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

- copies `changelog-head.md` to `CHANGELOG.md`
- generates section header for each found version,
  latest version first
- generates **Key Updates**, **Bugs Fixed** and **Other**
  sections for each version
- renders all items in filename alphabetic order
  under it's specific section to `CHANGELOG.md`
- and finally appends `changelog-tail.md`

To generate actual changelog:

- Execute `release-scripts/generate-changelog.sh` script
- Commit generated `CHANGELOG.md` to git and merge it to master.