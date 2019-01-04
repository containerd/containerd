# miscreant.go [![Build Status][build-shield]][build-link] [![GoDoc][godoc-shield]][godoc-link] [![Go Report Card][goreport-shield]][goreport-link] [![MIT licensed][license-shield]][license-link] [![Gitter Chat][gitter-image]][gitter-link]

> The best crypto you've never heard of, brought to you by [Phil Rogaway]

Go implementation of **Miscreant**: Advanced symmetric encryption library
which provides the [AES-SIV] ([RFC 5297]), [AES-PMAC-SIV], and [STREAM]
constructions. These algorithms are easy-to-use (or rather, hard-to-misuse)
and support encryption of individual messages or message streams.

```go
import "github.com/miscreant/miscreant-go"
```

All types are designed to be **thread-compatible**: Methods of an instance shared between
multiple threads (or goroutines) must not be accessed concurrently. Callers are responsible for
implementing their own mutual exclusion.


- [Documentation] (Wiki)
- [godoc][godoc-link]

## About AES-SIV and AES-PMAC-SIV

**AES-SIV** and **AES-PMAC-SIV** provide [nonce-reuse misuse-resistance] (NRMR):
accidentally reusing a nonce with this construction is not a security
catastrophe, unlike more popular AES encryption modes like [AES-GCM] where
nonce reuse leaks both the authentication key and the XOR of both plaintexts,
both of which can potentially be leveraged for *full plaintext recovery attacks*.

With **AES-SIV**, the worst outcome of reusing a nonce is an attacker
can see you've sent the same plaintext twice, as opposed to almost all other
AES modes where it can facilitate [chosen ciphertext attacks] and/or
full plaintext recovery.

## Help and Discussion

Have questions? Want to suggest a feature or change?

* [Gitter]: web-based chat about miscreant projects including **miscreant.go**
* [Google Group]: join via web or email ([miscreant-crypto+subscribe@googlegroups.com])

## Security Notice

Though this library is written by cryptographic professionals, it has not
undergone a thorough security audit, and cryptographic professionals are still
humans that make mistakes.

This library makes an effort to use constant time operations throughout its
implementation, however actual constant time behavior has not been verified.

Use this library at your own risk.

## Code of Conduct

We abide by the [Contributor Covenant][cc] and ask that you do as well.

For more information, please see [CODE_OF_CONDUCT.md].

## Contributing

Bug reports and pull requests are welcome on GitHub at:

<https://github.com/miscreant/miscreant-go>

## Copyright

Copyright (c) 2017-2018 [The Miscreant Developers][AUTHORS].
See [LICENSE.txt] for further details.

[build-shield]: https://secure.travis-ci.org/miscreant/miscreant-go.svg?branch=master
[build-link]: https://travis-ci.org/miscreant/miscreant-go
[godoc-shield]: https://godoc.org/github.com/miscreant/miscreant-go?status.svg
[godoc-link]: https://godoc.org/github.com/miscreant/miscreant-go
[goreport-shield]: https://goreportcard.com/badge/github.com/miscreant/miscreant-go
[goreport-link]: https://goreportcard.com/report/github.com/miscreant/miscreant-go
[license-shield]: https://img.shields.io/badge/license-MIT-blue.svg
[license-link]: https://github.com/miscreant/miscreant-go/blob/master/LICENSE.txt
[gitter-image]: https://badges.gitter.im/badge.svg
[gitter-link]: https://gitter.im/miscreant/Lobby
[Phil Rogaway]: https://en.wikipedia.org/wiki/Phillip_Rogaway
[AES-SIV]: https://github.com/miscreant/miscreant/wiki/AES-SIV
[RFC 5297]: https://tools.ietf.org/html/rfc5297
[AES-PMAC-SIV]: https://github.com/miscreant/miscreant/wiki/AES-PMAC-SIV
[STREAM]: https://github.com/miscreant/miscreant/wiki/STREAM
[nonce-reuse misuse-resistance]: https://github.com/miscreant/miscreant/wiki/Nonce-Reuse-Misuse-Resistance
[AES-GCM]: https://en.wikipedia.org/wiki/Galois/Counter_Mode
[chosen ciphertext attacks]: https://en.wikipedia.org/wiki/Chosen-ciphertext_attack
[Documentation]: https://github.com/miscreant/miscreant/wiki/Go-Documentation
[Gitter]: https://gitter.im/miscreant/Lobby
[Google Group]: https://groups.google.com/forum/#!forum/miscreant-crypto
[miscreant-crypto+subscribe@googlegroups.com]: mailto:miscreant-crypto+subscribe@googlegroups.com?subject=subscribe
[cc]: https://contributor-covenant.org
[CODE_OF_CONDUCT.md]: https://github.com/miscreant/miscreant-go/blob/master/CODE_OF_CONDUCT.md
[AUTHORS]: https://github.com/miscreant/miscreant-go/blob/master/AUTHORS.md
[LICENSE.txt]: https://github.com/miscreant/miscreant-go/blob/master/LICENSE.txt
