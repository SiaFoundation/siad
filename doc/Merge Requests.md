# Merge Requests
Merge Requests are used to ensure all code being added to the Sia repo adheres
to the Sia Engineering Guidelines (TODO: add link).

## Template
Every merge request on the Sia repo will be created with the default Merge
Request Template.
```
# MERGE REQUEST
## Overview

## Example for Visual Changes
<!--
For changes to siac or other user facing features please provide proof that the format is as expected.  
Screen shots and/or asciinema recordings are very helpful.
-->

## Checklist
Review and complete the checklist to ensure that the MR is complete before assigned to an approver.
 - [ ] All new methods or updated methods have clear docstrings
 - [ ] Testing added or updated for new methods
 - [ ] Any new packages are added to Makefile and .gitlab-ci.yml
 - [ ] API documentation updated for API updates
 - [ ] Module README.md updated for changes to workflow
 - [ ] Issue added to Sia-UI repo for new supporting features
 - [ ] Changelog File Created

## Issues Closed
<!--
Use the `Closes` keyword to automatically close the issue on merge.  
Example: Closes #XXXX  
-->

```
The `Overview` section is used to provide reviewers with a clear understanding
of the purpose of the merge request. Developers should always make sure that the
overview is filled out, as Sia is an open source project and we have people
outside the core team, such as community members, that look at the merge
requests and need to know the purpose behind them.

The `Example for Visual Changes` section should be used whenever there are UI
changes to `siac`. Other examples of when this section should be used would be
proof that a regression test failed prior to a change.

The `Checklist` should be used by the developer to make sure that they have
submitted a complete merge request. Developers should refrain from deleting
items that they believe do not apply as an approver might disagree. Updating the
markdown to strike through the items that do not apply with the `~~text~~`
(~~text~~) syntax is preferred. 

The `Issues Closed` section is used to ensure the Sia repo is being kept tidy
and issues are being closed. Use the built in helper text such as `Closes #1324`
to close issue 1324 when the merge request is approved.

## Approvals
The default approval rules for merge requests are:
 - 1 approval from a Dev III
 - 1 approval from a Peer Reviewer

To ensure all new code is reviewed, any new commits will remove previous
approvals.

### Exceptions
To help ensure that Dev III's are not being cluttered with inconsequential merge
request approvals there are a few exceptions to this approval rule.
 - Spelling / Grammar fixes
 - Renaming / Alphabetizing
 - Code reorganization (size dependent)

In these examples, there are no actual changes to the code and the diff of the
merge should be functionally net zero. If a merge request meets these
thresholds, the approval requirements can be updated to 2 approvals from Peer
Reviewers. 

## Comments / Discussions
When reviewing an MR, approvers use comments and discussions to work through the
approval. Comments are non-blocking while discussions are blocking. 

Comments should be used by the developer and/or reviewers to provide additional
context or information. An example would be an reviewer leaving a comment of
`LGTM but would like to leave the approval to XXX as this is their content
area`. This comment informs others that they have given an initial review and no
further discussion is needed.

Discussions should be used when follow up or further discussion is needed.
Discussions create a thread so responses are grouped. Discussions can be added
at specific lines of code or left on the merge request generally. All
discussions have to be resolved prior to a merge request being merged.

### Acronyms
To help developers and other reviewers, all discussions should have a acronym
prefix that informs the priority of the issue.

**REQ**: The `REQ` prefix indicates that a discussion is required to be resolved
in the current merge request, not in a follow up, in order for a merge request
to be approved.

**nit**: The `nit` prefix indicates that a discussion is a personal preference
or a small recommendation of a reviewer. The developer can decide how they want
to handle this discussion and choose to incorporate the feedback or not. The use
of `nit` is to be more consistent with the Bitcoin review standards.

**F/U**: The `F/U` prefix indicates that a discussion is intended to be
addressed in a follow up and shouldn't be addressed in the current merge
request. `F/U` is used to avoid merge request bloat and keep the merge request
diff contained.

## Follow Ups
For comments that are resolved into follow up issues, the expectation is that
they are addressed immediately. Gitlab allows users to create a branch and MR
directly from an issue.

If the follow up is not a minor change then it should be updated to a new issue
and fully filled out and properly labelled and assigned.

## Branch Management
By default Gitlab should select branches to be deleted upon merge. Developer
should help ensure that this is enforced to keep the repository clean.

For multi-part features, or for breaking up large merge requests into smaller
merge requests, the preferred approach is for subsequent merge requests to be
new branches off the preceding merge requests. The subsequent merge requests can
then target the preceding branch to keep the diff local to the new changes. The
preceding merge request should also be listed as a dependency to ensure the
merge requests are merged in order.

## Merge Request Commits
When working locally developers can choose to either rebase or merge in order to
keep up to date with the master branch. Any commit squashing and clean up should
happen locally prior to submitting a merge request. Once a merge request is
submitted, developers should merge with master to address conflicts and be
pushing new commits for all changes. It is discouraged to continue to squash
commits and rebase once the merge request is being reviewed to help reviewers
keep track of changes.

## WIP Merge Requests 
Gitlab allows the use of the `WIP:` prefix in the title of the merge request.
This `WIP:` prefix is blocking and will not allow the merge request to be merged
until it is removed. Developers should use this prefix to inform reviewers that
the merge request is still under development and should not be reviewed. The
`WIP:` prefix is also automatically added when a merge request is created from
an issue as in the case of follow ups from previous merge requests. This allows
the developer to make the initial changes and have the follow up merge request
staged until the initial merge request is merged.

To keep the list of open merge requests down, it is advised to use the `WIP:`
prefix sparingly. The majority of development should be done locally and most
merge requests should be created ready for review.
