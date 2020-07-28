Default Path
============

This document describes the behaviour of the default path for skyfiles.

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
not support either. 

### Single file
Single files do not support `defaultPath`, so both setting and disabling that 
are invalid operations and result in errors. The reason for that is that we 
always want to return the content of the only file.
 
- `disableDefaultPath` other than missing or `false` results in an Error
- `defaultPath` other than `""` results in an Error

### Single file directory

`disableDefaultPath` works, i.e. its boolean value is set in the metadata. If that value is `true` the `defaultPath` field is set to `""`. This takes precedence 
over any value of `defaultPath`. 

If `defaultPath` is set to a path that doesn't match the only file in `subfiles`
that results in Error.

In all other cases `defaultPath` is set to the path of the only file in 
`subfiles`.

### Multi file directory

`disableDefaultPath` works, i.e. its boolean value is set in the metadata. If that value is `true` the `defaultPath` field is set to `""`. This takes precedence 
over any value of `defaultPath`.

If `defaultPath` is set to a path that doesn't match any file in `subfiles`
that results in Error.

If `defaultPath` is set to a directory path (i.e. not a file) that is considered
to not match any file and results in an Error.  

#### Non-empty `defaultPath`

If `defaultPath` is set to a valid file from `subfiles` that value is accepted 
and set in the metadata. Note that this is always the case, regardless of other
restrictions on valid default paths, such as default path files needing to be
HTML files.

#### Empty `defaultPath`

If `defaultPath` is empty and there is an `index.html` file in the root 
directory of the skyfile than the `defaultPath` is set to that `index.html`.

## Behaviour on download

### Single file

Since single file uploads ignore `defaultPath` and `disableDefaultPath` they
will download their content directly.

In case a `defaultPath` is somehow set on a single file (e.g. via a modified 
portal that didn't do the normal checks) it will be ignored. This is not a 
dedicated execution path, it's just a side effect of the way `ForPath` is
implemented.

### Single file directory

#### Legacy case

The legacy case is the status quo from before the `defaultPath` was implemented.
These files have `defaultPath` equivalent to `""` and their `disableDefaultPath`
equivalent to `false`. In this case we automatically set the `defaultPath` to
the only file in `subfiles`.

#### `defaultPath` is defined and not empty

The entire metadata of the skyfile is returned.

If the `defaultPath` is somehow set to a directory and not a file this case is
identical to the Multi file directory with `defaultPath` pointing to a directory
case.

#### `defaultPath` is empty and `disableDefaultPath` is `true`

The entire metadata of the skyfile is returned.

The content of the skyfile is returned in a zipped form.

### Multi file directory

#### `defaultPath` is not empty

##### `defaultPath` exactly matches a file

If the file which `defaultPath` matches doesn't end with `.html` or `.htm` we
return an Error.

We return a subset of the metadata which contains `Filename` which is set to the 
`defaultPath` value and `Subfiles` which only contains the `defaultPath` file.

We return the content of the `defaultPath` file.

##### `defaultPath` doesn't exactly match a file

This is only possible if the skyfile was uploaded via a modified portal.

We return the entire metadata of the skyfile.

TODO: We have a problem here. We expect `ForPath` to return a single file but it
will return a subset of the skyfile in the same way accessing a skyfile with a 
path will do it. We need to change the code in order to accommodate/protect 
against this case. 

#### `defaultPath` is empty

If `disableDefaultPath` is `false` and the skyfile contains an `index.html` file
at its root directory then the `defaultPath` is set to `/index.html` and we 
continue with the case where the `defaultPath` matches a specific file.

Otherwise, we return the entire metadata.

The entire content of the skyfile is returned as a zip.










