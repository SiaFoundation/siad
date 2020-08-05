Default Path
============

This document describes the behaviour of the default path for skyfiles. That 
behaviour is only triggered if the skylink is accessed without a subpath.

First of all, some requirements for the `defaultPath` file:
- it must exist
- it must be an HTML file
- it must be in the root directory of the skyfile

# Skyfile types

## Single file

This is a skyfile which contains a single file at its root directory. This 
file's metadata does not have a `subfiles` entry because that's redundant, the 
metadata only has the `filename` field.

This kind of file does not have `defaultPath` or `disableDefaultPath` metadata
fields.

## Single file directory

This is a skyfile which contains a directory tree that happens to hold a single
file. This skyfile's metadata has a `subfiles` entry because that's needed in
order to describe the directory tree. The name of the only file does not
necessarily match the `filename` field of the metadata.

This kind of file may have `defaultPath` and/or `disableDefaultPath` metadata 
fields.

## Multi file directory

This is a skyfile that contains multiple files distributed among one or more 
directories.

This kind of file may have `defaultPath` and/or `disableDefaultPath` metadata 
fields.

# Behaviours

## Behaviour on upload

`defaultPath` and `disableDefaultPath` are only taken into consideration during
a multipart upload. A regular `POST` upload results in a single file which does
not contain either. 

`defaultPath` and `disableDefaultPath` are mutually exclusive and specifying a 
`defaultPath` which is not `""` together with `disableDefaultPath` which is 
`true` results in an error.

`defaultPath` can only point to HTML files located in the root directory of the
skyfile.

### Single file

Single files do not support `defaultPath`, so both setting and disabling that 
are invalid operations and result in errors. The reason for that is that we 
always want to return the content of the only file.
 
- `disableDefaultPath` other than missing or `false` results in an Error
- `defaultPath` other than `""` results in an Error

### Single file directory

If `defaultPath` is set to a path that doesn't match the only file in `subfiles`
that results in Error.

In all other cases `defaultPath` is set to the path of the only file in 
`subfiles`.

### Multi file directory

If `defaultPath` is set to a path that doesn't match any file in `subfiles` or
the file that it matches is not an HTML file located in the root directory, that
results in Error.

If `defaultPath` is set to a directory path (i.e. not a file) that is considered
to not match any file and results in an Error.  

If `defaultPath` is set to a valid file from `subfiles` that value is accepted 
and set in the metadata. Note that this is always the case, regardless of other
restrictions on valid default paths, such as default path files needing to be
HTML files.

## Behaviour on download

If the value of `disableDefaultPath` is `true` no content is served if the 
skyfile is accessed at its root path. Instead, the content of the entire skyfile 
is downloaded as a zip or another format, if one is specified via the `format` 
parameter.
 
 If the value of `defaultPath` is `""` and `disableDefaultPath` is `false` we do
 a couple of checks:
 - if the skyfile contains a single file we serve the content of that file
 - if the skyfile contains more than one file and has an `index.html` file in
 its root directory, we serve that `index.html` file
 - if neither is true we download the entire file as a zip or another format, if
  one is specified via the `format` parameter

If both `defaultPath` and `disableDefaultPath` are set simultaneously and no 
`format` is specified we return an error.
 
In all the above cases we serve the full metadata of the skyfile. 

### Single file

In case a `defaultPath` is set on a single file we return an error - the file
can only be downloaded by specifying a `format`.

### Single file directory

#### `defaultPath` is not empty

If the `defaultPath` is set to a directory and not a file we return an error 
stating that skyfile has invalid `defaultPath` and the client should use a 
format to download the skyfile.

Serve the content of the file to which the `defaultPath` points.

The entire metadata of the skyfile is returned.

#### `defaultPath` is empty and `disableDefaultPath` is `true`

The content of the skyfile is returned as a zip or another format, if one is 
specified via the `format` parameter.

The entire metadata of the skyfile is returned.

#### `defaultPath` is empty and `disableDefaultPath` is `false`

Serve the content of the only file in the skyfile, regardless of its name, type
or location.

The entire metadata of the skyfile is returned.

### Multi file directory

On success the entire metadata of the skyfile is returned.

#### `defaultPath` is not empty

If the `defaultPath` is set to a directory and not a file we return an error 
stating that skyfile has invalid `defaultPath` and the client should use a 
format to download the skyfile.

##### `defaultPath` exactly matches a file

If the file which `defaultPath` matches doesn't end with `.html` or `.htm` or
the file is not in the root directory of the skyfile, we return an Error.

We serve the content of the file to which the `defaultPath` points.

##### `defaultPath` doesn't exactly match a file

We return an error stating that skyfile has invalid `defaultPath` and
the client should use a format to download the skyfile. 

#### `defaultPath` is empty

If `disableDefaultPath` is `false` and the skyfile contains an `index.html` file
in its root directory then we return the content of that `index.html`.

Otherwise, the entire content of the skyfile is returned as a zip or another 
format, if one is specified via the `format` parameter.
