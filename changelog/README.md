# Changelog Generator


To avoid merge conflicts on each Changelog update.  
To simplify generating Changelog.

## How to add an item to Changelog?

- In `Sia` repository there is the following directory/file structure
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
                  ...

- To add an item to changelog,
  create `short-bug-description.md` file at the proper location
- Do not use spaces or apostrophes in the filename
- Inside the .md file use markdown description of the issue
  that will appear in `CHANGELOG.md`

## How to sort/position the changelog issues

- Changelog versions are sorted in reverse version order,
  i.e. latest version is first
- Changelog items (under `key-updates`, `bugs-fixed`, `other`)
  are sorted in ascending alphabetic order by filenames.
- We can use e.g. numeric filename prefix to sort them in desired order

## What is changelog-head.md and changelog-tail.md

- `changelog-head.md` is the beginning, `changelog-tail.md` is the end of the generated `CHANGELOG.md`.
- Changelog generator will generate changelog issues between these two parts.
- They can be edited and committed to git

## How to update changelog-tail.md with old versions

- Changelog generator can have multiple versions in `changelog` directory.
- However you might want to remove finished versions
  from `changelog` directory to `changelog-tail.md`
- To do this:
  - generate the changelog
  - open generated `CHANGELOG.md`
  - copy the finished version generated `CHANGELOG.md` to `changelog-tail.md`
  - delete the finished version from `changelog` folder
  - commit to git