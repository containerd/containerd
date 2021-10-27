go-blockservice
==================

[![](https://img.shields.io/badge/made%20by-Protocol%20Labs-blue.svg?style=flat-square)](http://ipn.io)
[![](https://img.shields.io/badge/project-IPFS-blue.svg?style=flat-square)](http://ipfs.io/)
[![](https://img.shields.io/badge/freenode-%23ipfs-blue.svg?style=flat-square)](http://webchat.freenode.net/?channels=%23ipfs)
[![Coverage Status](https://codecov.io/gh/ipfs/go-block-format/branch/master/graph/badge.svg)](https://codecov.io/gh/ipfs/go-block-format/branch/master)
[![Build Status](https://circleci.com/gh/ipfs/go-blockservice.svg?style=svg)](https://circleci.com/gh/ipfs/go-blockservice)

> go-blockservice provides a seamless interface to both local and remote storage backends.

## Lead Maintainer

[Steven Allen](https://github.com/Stebalien)

## Table of Contents

- [TODO](#todo)
- [Contribute](#contribute)
- [License](#license)

## TODO

The interfaces here really would like to be merged with the blockstore interfaces.
The 'dagservice' constructor currently takes a blockservice, but it would be really nice
if it could just take a blockstore, and have this package implement a blockstore.

## Contribute

PRs are welcome!

Small note: If editing the Readme, please conform to the [standard-readme](https://github.com/RichardLitt/standard-readme) specification.

## License

MIT © Juan Batiz-Benet
