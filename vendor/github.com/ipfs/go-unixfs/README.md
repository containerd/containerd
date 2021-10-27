go-unixfs
==================

[![](https://img.shields.io/badge/made%20by-Protocol%20Labs-blue.svg?style=flat-square)](http://ipn.io)
[![](https://img.shields.io/badge/project-IPFS-blue.svg?style=flat-square)](http://ipfs.io/)
[![](https://img.shields.io/badge/freenode-%23ipfs-blue.svg?style=flat-square)](http://webchat.freenode.net/?channels=%23ipfs)
[![Coverage Status](https://codecov.io/gh/ipfs/go-unixfs/branch/master/graph/badge.svg)](https://codecov.io/gh/ipfs/go-unixfs/branch/master)
[![Travis CI](https://travis-ci.org/ipfs/go-unixfs.svg?branch=master)](https://travis-ci.org/ipfs/go-unixfs)

> go-unixfs implements unix-like filesystem utilities on top of an ipld merkledag

## Lead Maintainer

[Steven Allen](https://github.com/Stebalien)

## Table of Contents

- [Directory](#directory)
- [Install](#install)
- [Contribute](#contribute)
- [License](#license)

## Package Directory
This package contains many subpackages, each of which can be very large on its own.

### Top Level
The top level unixfs package defines the unixfs format datastructures, and some helper methods around it.

### importers
The `importer` subpackage is what you'll use when you want to turn a normal file into a unixfs file.

### io
The `io` subpackage provides helpers for reading files and manipulating directories. The `DagReader` takes a
reference to a unixfs file and returns a file handle that can be read from and seeked through. The `Directory`
interface allows you to easily read items in a directory, add items to a directory, and do lookups.

### mod
The `mod` subpackage implements a `DagModifier` type that can be used to write to an existing unixfs file, or
create a new one. The logic for this is significantly more complicated than for the dagreader, so its a separate
type. (TODO: maybe it still belongs in the `io` subpackage though?)

### hamt
The `hamt` subpackage implements a CHAMP hamt that is used in unixfs directory sharding.

### archive
The `archive` subpackage implements a `tar` importer and exporter. The objects created here are not officially unixfs,
but in the future, this may be integrated more directly.

### test
The `test` subpackage provides several utilities to make testing unixfs related things easier.

## Install

```sh
go get github.com/ipfs/go-unixfs
```

## Contribute

PRs are welcome!

Small note: If editing the Readme, please conform to the [standard-readme](https://github.com/RichardLitt/standard-readme) specification.

## License

MIT © Juan Batiz-Benet
