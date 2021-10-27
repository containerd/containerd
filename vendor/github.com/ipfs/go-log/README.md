# go-log

[![](https://img.shields.io/badge/made%20by-Protocol%20Labs-blue.svg?style=flat-square)](http://ipn.io)
[![](https://img.shields.io/badge/project-IPFS-blue.svg?style=flat-square)](http://ipfs.io/)
[![](https://img.shields.io/badge/freenode-%23ipfs-blue.svg?style=flat-square)](http://webchat.freenode.net/?channels=%23ipfs)
[![standard-readme compliant](https://img.shields.io/badge/standard--readme-OK-green.svg?style=flat-square)](https://github.com/RichardLitt/standard-readme)
[![GoDoc](https://godoc.org/github.com/ipfs/go-log?status.svg)](https://godoc.org/github.com/ipfs/go-log)
[![CircleCI](https://img.shields.io/circleci/build/github/ipfs/go-log?style=flat-square)](https://circleci.com/gh/ipfs/go-log)

<!---[![Coverage Status](https://coveralls.io/repos/github/ipfs/go-log/badge.svg?branch=master)](https://coveralls.io/github/ipfs/go-log?branch=master)--->


> The logging library used by go-ipfs

It currently uses a modified version of [go-logging](https://github.com/whyrusleeping/go-logging) to implement the standard printf-style log output.

## Install

```sh
go get github.com/ipfs/go-log
```

## Usage

Once the package is imported under the name `logging`, an instance of `EventLogger` can be created like so:

```go
var log = logging.Logger("subsystem name")
```

It can then be used to emit log messages, either plain printf-style messages at six standard levels or structured messages using `Start`, `StartFromParentState`, `Finish` and `FinishWithErr` methods.

## Example

```go
func (s *Session) GetBlock(ctx context.Context, c *cid.Cid) (blk blocks.Block, err error) {

    // Starts Span called "Session.GetBlock", associates with `ctx`
    ctx = log.Start(ctx, "Session.GetBlock")

    // defer so `blk` and `err` can be evaluated after call
    defer func() {
        // tag span associated with `ctx`
        log.SetTags(ctx, map[string]interface{}{
            "cid": c,
            "block", blk,
        })
        // if err is non-nil tag the span with an error
        log.FinishWithErr(ctx, err)
    }()

    if shouldStartSomething() {
        // log message on span associated with `ctx`
        log.LogKV(ctx, "startSomething", true)
    }
  ...
}
```
## Tracing

`go-log` wraps the [opentracing-go](https://github.com/opentracing/opentracing-go) methods - `StartSpan`, `Finish`, `LogKV`, and `SetTag`.

`go-log` implements its own tracer - `loggabletracer` - based on the [basictracer-go](https://github.com/opentracing/basictracer-go) implementation. If there is an active [`WriterGroup`](https://github.com/ipfs/go-log/blob/master/writer/option.go) the `loggabletracer` will [record](https://github.com/ipfs/go-log/blob/master/tracer/recorder.go) span data to the `WriterGroup`. An example of this can be seen in the [`log tail`](https://github.com/ipfs/go-ipfs/blob/master/core/commands/log.go) command of `go-ipfs`. 

Third party tracers may be used by calling `opentracing.SetGlobalTracer()` with your desired tracing implementation. An example of this can be seen using the [`go-jaeger-plugin`](https://github.com/ipfs/go-jaeger-plugin) and the `go-ipfs` [tracer plugin](https://github.com/ipfs/go-ipfs/blob/master/plugin/tracer.go)

## Contribute

Feel free to join in. All welcome. Open an [issue](https://github.com/ipfs/go-log/issues)!

This repository falls under the IPFS [Code of Conduct](https://github.com/ipfs/community/blob/master/code-of-conduct.md).

### Want to hack on IPFS?

[![](https://cdn.rawgit.com/jbenet/contribute-ipfs-gif/master/img/contribute.gif)](https://github.com/ipfs/community/blob/master/contributing.md)

## License

MIT
