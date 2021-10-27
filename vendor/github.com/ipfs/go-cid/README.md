go-cid
==================

[![](https://img.shields.io/badge/made%20by-Protocol%20Labs-blue.svg?style=flat-square)](http://ipn.io)
[![](https://img.shields.io/badge/project-IPFS-blue.svg?style=flat-square)](http://ipfs.io/)
[![](https://img.shields.io/badge/freenode-%23ipfs-blue.svg?style=flat-square)](http://webchat.freenode.net/?channels=%23ipfs)
[![](https://img.shields.io/badge/readme%20style-standard-brightgreen.svg?style=flat-square)](https://github.com/RichardLitt/standard-readme)
[![GoDoc](https://godoc.org/github.com/ipfs/go-cid?status.svg)](https://godoc.org/github.com/ipfs/go-cid)
[![Coverage Status](https://coveralls.io/repos/github/ipfs/go-cid/badge.svg?branch=master)](https://coveralls.io/github/ipfs/go-cid?branch=master)
[![Travis CI](https://travis-ci.org/ipfs/go-cid.svg?branch=master)](https://travis-ci.org/ipfs/go-cid)

> A package to handle content IDs in Go.

This is an implementation in Go of the [CID spec](https://github.com/ipld/cid).
It is used in `go-ipfs` and related packages to refer to a typed hunk of data.

## Lead Maintainer

[Eric Myhre](https://github.com/warpfork)

## Table of Contents

- [Install](#install)
- [Usage](#usage)
- [API](#api)
- [Contribute](#contribute)
- [License](#license)

## Install

`go-cid` is a standard Go module which can be installed with:

```sh
go get github.com/ipfs/go-cid
```

## Usage

### Running tests

Run tests with `go test` from the directory root

```sh
go test
```

### Examples

#### Parsing string input from users

```go
// Create a cid from a marshaled string
c, err := cid.Decode("bafzbeigai3eoy2ccc7ybwjfz5r3rdxqrinwi4rwytly24tdbh6yk7zslrm")
if err != nil {...}

fmt.Println("Got CID: ", c)
```

#### Creating a CID from scratch

```go
// Create a cid manually by specifying the 'prefix' parameters
pref := cid.Prefix{
	Version: 1,
	Codec: cid.Raw,
	MhType: mh.SHA2_256,
	MhLength: -1, // default length
}

// And then feed it some data
c, err := pref.Sum([]byte("Hello World!"))
if err != nil {...}

fmt.Println("Created CID: ", c)
```

#### Check if two CIDs match

```go
// To test if two cid's are equivalent, be sure to use the 'Equals' method:
if c1.Equals(c2) {
	fmt.Println("These two refer to the same exact data!")
}
```

#### Check if some data matches a given CID

```go
// To check if some data matches a given cid, 
// Get your CIDs prefix, and use that to sum the data in question:
other, err := c.Prefix().Sum(mydata)
if err != nil {...}

if !c.Equals(other) {
	fmt.Println("This data is different.")
}

```

## Contribute

PRs are welcome!

Small note: If editing the Readme, please conform to the [standard-readme](https://github.com/RichardLitt/standard-readme) specification.

## License

MIT © Jeromy Johnson
