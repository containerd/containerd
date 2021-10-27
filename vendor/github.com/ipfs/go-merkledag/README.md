go-merkledag
==================

[![](https://img.shields.io/badge/made%20by-Protocol%20Labs-blue.svg?style=flat-square)](http://ipn.io)
[![](https://img.shields.io/badge/project-IPFS-blue.svg?style=flat-square)](http://ipfs.io/)
[![](https://img.shields.io/badge/freenode-%23ipfs-blue.svg?style=flat-square)](http://webchat.freenode.net/?channels=%23ipfs)
[![Coverage Status](https://codecov.io/gh/ipfs/go-merkledag/branch/master/graph/badge.svg)](https://codecov.io/gh/ipfs/go-merkledag/branch/master)
[![Travis CI](https://travis-ci.org/ipfs/go-merkledag.svg?branch=master)](https://travis-ci.org/ipfs/go-merkledag)

> go-merkledag implements the 'DAGService' interface and adds two ipld node types, Protobuf and Raw 

## Lead Maintainer

[Steven Allen](https://github.com/Stebalien)

## Table of Contents

- [TODO](#todo)
- [Contribute](#contribute)
- [License](#license)

## TODO

- Pull out dag-pb stuff into go-ipld-pb
- Pull 'raw nodes' out into go-ipld-raw (maybe main one instead)
- Move most other logic to go-ipld
- Make dagservice constructor take a 'blockstore' to avoid the blockservice offline nonsense
- deprecate this package

## Contribute

PRs are welcome!

Small note: If editing the Readme, please conform to the [standard-readme](https://github.com/RichardLitt/standard-readme) specification.

## License

MIT © Juan Batiz-Benet
