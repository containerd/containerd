# Build containerd from source

This guide is useful if you intend to contribute on containerd. Thanks for your
effort. Every contribution is very appreciated.

## Build the development environment

In first you need to setup your Go development environment. You can follow this
guideline [How to write go code](https://golang.org/doc/code.html) and at the
end you need to have `GOPATH` and `GOROOT` set in your environment.

Current containerd requires Go 1.8.x or above.

At this point you can use `go` to checkout `containerd` in your `GOPATH`:

```sh
go get github.com/containerd/containerd
```

`containerd` uses [Btrfs](https://en.wikipedia.org/wiki/Btrfs) it means that you
need to satisfy this dependencies in your system:

* CentOS/Fedora: `yum install btrfs-progs-devel`
* Debian/Ubuntu: `apt-get install btrfs-tools`

At this point you are ready to build `containerd` yourself.

## In your local environment

`containerd` uses `make` to create a repeatable build flow. It means that you
can run:

```sudo
make
```

This is going to build all the binaries provided by this project in the `./bin`
directory.

You can move them in your global path with:

```sudo
sudo make install
```

## Via Docker Container

You can build `containerd` via Docker container. You can build an image from
this `Dockerfile`:

```
FROM golang

RUN apt-get update && \
    apt-get install btrfs-tools
```

Let's suppose that you built an image called `containerd/build` and you are into
the containerd root directory you can run the following command:

```sh
docker run -it --rm \
    -v ${PWD}:/go/src/github.com/containerd/containerd \
    -e GOPATH=/go \
    -w /go/src/github.com/containerd/containerd containerd/build make
```

At this point you can find your binaries in the `./bin` directory in your host.
You can move the binaries in your `$PATH` with the command:

```sh
sudo make install
```
