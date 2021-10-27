# DEPRECATION NOTICE

This package has moved into go-multiaddr as a sub-package,
`github.com/multiformats/go-multiaddr/net`.

# go-multiaddr-net

[![](https://img.shields.io/badge/made%20by-Protocol%20Labs-blue.svg?style=flat-square)](https://protocol.ai)
[![](https://img.shields.io/badge/project-multiformats-blue.svg?style=flat-square)](https://github.com/multiformats/multiformats)
[![](https://img.shields.io/badge/freenode-%23ipfs-blue.svg?style=flat-square)](https://webchat.freenode.net/?channels=%23ipfs)
[![](https://img.shields.io/badge/readme%20style-standard-brightgreen.svg?style=flat-square)](https://github.com/RichardLitt/standard-readme)
[![GoDoc](https://godoc.org/github.com/multiformats/go-multiaddr-net?status.svg)](https://godoc.org/github.com/multiformats/go-multiaddr-net)
[![Travis CI](https://img.shields.io/travis/multiformats/go-multiaddr-net.svg?style=flat-square&branch=master)](https://travis-ci.org/multiformats/go-multiaddr-net)

<!---[![codecov.io](https://img.shields.io/codecov/c/github/multiformats/go-multiaddr-net.svg?style=flat-square&branch=master)](https://codecov.io/github/multiformats/go-multiaddr-net?branch=master)--->

> Multiaddress net tools

This package provides [Multiaddr](https://github.com/multiformats/go-multiaddr) specific versions of common functions in [stdlib](https://github.com/golang/go/tree/master/src)'s `net` package. This means wrappers of standard net symbols like `net.Dial` and `net.Listen`, as well
as conversion to and from `net.Addr`.

## Table of Contents

- [Install](#install)
- [Usage](#usage)
- [Maintainers](#maintainers)
- [Contribute](#contribute)
- [License](#license)

## Install

`go-multiaddr-net` is a standard Go module which can be installed with:

```sh
go get github.com/multiformats/go-multiaddr-net
```

Note that `go-multiaddr-net` is packaged with Gx, so it is recommended to use Gx to install and use it (see Usage section).


## Usage

See the docs:

- `multiaddr/net`: https://godoc.org/github.com/multiformats/go-multiaddr-net
- `multiaddr`: https://godoc.org/github.com/multiformats/go-multiaddr

## Contribute

Contributions welcome. Please check out [the issues](https://github.com/multiformats/go-multiaddr-net/issues).

Check out our [contributing document](https://github.com/multiformats/multiformats/blob/master/contributing.md) for more information on how we work, and about contributing in general. Please be aware that all interactions related to multiformats are subject to the IPFS [Code of Conduct](https://github.com/ipfs/community/blob/master/code-of-conduct.md).

Small note: If editing the README, please conform to the [standard-readme](https://github.com/RichardLitt/standard-readme) specification.

## License

[MIT](LICENSE) © 2014 Juan Batiz-Benet
