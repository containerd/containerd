# Build containerd from source

This guide is useful if you intend to contribute on containerd. Thanks for your
effort. Every contribution is very appreciated.

This doc includes:
* [Getting started with GitHub Codespaces](#getting-started-with-gitHub-codespaces)
* [Build requirements](#build-requirements)
* [Build the development environment](#build-the-development-environment)
* [Build containerd](#build-containerd)
* [Via docker container](#via-docker-container)
* [Testing](#testing-containerd)

## Getting started with GitHub Codespaces

To get started, create a codespace for this repository by clicking this 👇

[![Open in GitHub Codespaces](https://github.com/codespaces/badge.svg)](https://github.com/codespaces/new?hide_repo_select=true&ref=main&repo=46089560)

A codespace will open in a web-based version of Visual Studio Code. The [dev container](.devcontainer/devcontainer.json) is fully configured with software needed for this project and the containerd built. If you use a codespace, then you can directly skip to the [testing](#testing-containerd) section of this document.

**Note**: Dev containers is an open spec which is supported by [GitHub Codespaces](https://github.com/codespaces) and [other tools](https://containers.dev/supporting).

## Build requirements

To build the `containerd` daemon, and the `ctr` simple test client, the following build system dependencies are required:

* Go 1.22.x or above
* Protoc 3.x compiler and headers (download at the [Google protobuf releases page](https://github.com/protocolbuffers/protobuf/releases))
* Btrfs headers and libraries for your distribution. Note that building the btrfs driver can be disabled via the build tag `no_btrfs`, removing this dependency.

> *Note*: On macOS, you need a third party runtime to run containers on containerd

## Build the development environment

First you need to setup your Go development environment. You can follow this
guideline [How to write go code](https://golang.org/doc/code.html) and at the
end you have `go` command in your `PATH`.

You need `git` to checkout the source code:

```sh
git clone https://github.com/containerd/containerd
```

For proper results, install the `protoc` release into `/usr/local` on your build system. When generating source code from `.proto` files, containerd may rely on some external protocol buffer files. These external dependencies should be added to the `/usr/local/include` directory. To install the appropriate version of `protoc` and download any necessary external protocol buffer files on a Linux host, run the install script located at `script/setup/install-protobuf`.

To enable optional [Btrfs](https://en.wikipedia.org/wiki/Btrfs) snapshotter, you should have the headers from the Linux kernel 4.12 or later.
The dependency on the kernel headers only affects users building containerd from source.
Users on older kernels may opt to not compile the btrfs support (see `BUILDTAGS=no_btrfs` below),
or to provide headers from a newer kernel.

> **Note**
> The dependency on the Linux kernel headers 4.12 was introduced in containerd 1.7.0-beta.4.
>
> containerd 1.6 has different set of dependencies for enabling btrfs.
> containerd 1.6 users should refer to https://github.com/containerd/containerd/blob/release/1.6/BUILDING.md#build-the-development-environment

At this point you are ready to build `containerd` yourself!

## Runc

Runc is the default container runtime used by `containerd` and is required to
run containerd. While it is okay to download a `runc` binary and install that on
the system, sometimes it is necessary to build runc directly when working with
container runtime development. Make sure to follow the guidelines for versioning
in [RUNC.md](/docs/RUNC.md) for the best results.

> *Note*: Runc only supports Linux

## Build containerd

`containerd` uses `make` to create a repeatable build flow. It means that you
can run:

```sh
cd containerd
make
```

This is going to build all the project binaries in the `./bin/` directory.

You can move them in your global path, `/usr/local/bin` with:

```sh
sudo make install
```

The install prefix can be changed by passing the `PREFIX` variable (defaults
to `/usr/local`).

Note: if you set one of these vars, set them to the same values on all make stages
(build as well as install).

If you want to prepend an additional prefix on actual installation (eg. packaging or chroot install),
you can pass it via `DESTDIR` variable:

```sh
sudo make install DESTDIR=/tmp/install-x973234/
```

The above command installs the `containerd` binary to `/tmp/install-x973234/usr/local/bin/containerd`

The current `DESTDIR` convention is supported since containerd v1.6.
Older releases was using `DESTDIR` for a different purpose that is similar to `PREFIX`.


When making any changes to the gRPC API, you can use the installed `protoc`
compiler to regenerate the API generated code packages with:

```sh
make generate
```

> *Note*: Several build tags are currently available:
> * `no_cri`: A build tag disables building Kubernetes [CRI](http://blog.kubernetes.io/2016/12/container-runtime-interface-cri-in-kubernetes.html) support into containerd.
> See [here](https://github.com/containerd/cri-containerd#build-tags) for build tags of CRI plugin.
> * snapshotters (alphabetical order)
>   * `no_aufs`: A build tag disables building the aufs snapshot driver. (Ignored since containerd v2.0, as the aufs snapshot driver is no longer supported)
>   * `no_btrfs`: A build tag disables building the Btrfs snapshot driver.
>   * `no_devmapper`: A build tag disables building the device mapper snapshot driver.
>   * `no_zfs`: A build tag disables building the ZFS snapshot driver.
> * platform
>   * `no_systemd`: disables any systemd specific code
>
> For example, adding `BUILDTAGS=no_btrfs` to your environment before calling the **binaries**
> Makefile target will disable the btrfs driver within the containerd Go build.

Vendoring of external imports uses the [Go Modules](https://golang.org/ref/mod#vendoring). You need
to use `go mod` command to modify the dependencies. After modifition, you should run `go mod tidy`
and `go mod vendor` to ensure the `go.mod`, `go.sum` files and `vendor` directory are up to date.
Changes to these files should become a single commit for a PR which relies on vendored updates.

Please refer to [RUNC.md](/docs/RUNC.md) for the currently supported version of `runc` that is used by containerd.

> *Note*: On macOS, the containerd daemon can be built and run natively. However, as stated above, runc only supports linux.

### Static binaries

You can build static binaries by providing a few variables to `make`:

```sh
make STATIC=1
```

> *Note*:
> - static build is discouraged
> - static containerd binary does not support loading shared object plugins (`*.so`)
> - static build binaries are not position-independent

# Via Docker container

> [!NOTE]
> The following instructions assume you are at the **parent** directory of containerd source directory.

## Build containerd in a container

You can build `containerd` via a Linux-based Docker container using the [Docker official `golang` image](https://hub.docker.com/_/golang/)

From the **parent** directory of `containerd`'s cloned repo you can run the following command:

```sh
docker run -it \
    -v ${PWD}/containerd:/src/containerd \
    -w /src/containerd golang
```

This mounts the `containerd` repository inside the image at `/src/containerd` and, by default, runs a shell at that directory.

Now, you are now ready to follow the [build instructions](#build-containerd):

## Build containerd and runc in a container

To have complete core container runtime, you will need both `containerd` and `runc`. It is possible to build both of these via Docker container.

You can clone `runc` in the same parent directory where you cloned `containerd` and you should clone [the latest stable version of `runc`](https://github.com/opencontainers/runc/releases), e.g. v1.1.13:

```sh
git clone --branch <RELEASE_TAG> https://github.com/opencontainers/runc
```

In our Docker container we will build `runc` build, which includes
[seccomp](https://en.wikipedia.org/wiki/seccomp), [SELinux](https://en.wikipedia.org/wiki/Security-Enhanced_Linux),
and [AppArmor](https://en.wikipedia.org/wiki/AppArmor) support. Seccomp support
in runc requires `libseccomp-dev` as a dependency (AppArmor and SELinux support
do not require external libraries at build time). Refer to [RUNC.md](docs/RUNC.md)
in the docs directory to for details about building runc, and to learn about
supported versions of `runc` as used by containerd.

Since we need [`libseccomp-dev`](https://packages.debian.org/stable/libseccomp-dev) installed as a dependency, we will need a custom Docker image derived from the official `golang` image. You can use the following `Dockerfile` to build your custom image:

```sh
FROM golang

RUN apt-get update && \
    apt-get install -y libseccomp-dev
```

Let's suppose you've built an image named `containerd/build` from the above `Dockerfile`.

You can run the following command:

```sh
docker run -it \
    -v ${PWD}/containerd:/src/containerd \
    -v ${PWD}/runc:/src/runc \
    -w /src/containerd \
    containerd/build
```

This mounts both `runc` and `containerd` repositories in our Docker container.

From within the Docker container, let's build `containerd`:

```sh
make && make install
```

You can check the installed binaries with:

```sh
$ which containerd
/usr/local/bin/containerd

$ containerd --version
containerd github.com/containerd/containerd/v2 v2.0.0-rc.3-195-gf5d5407c2 f5d5407c2ff12865653a9a132d5783196be82763
```

Next, let's build `runc`:

```sh
cd /src/runc
make && make install
```

You can check the installed binaries with:

```sh
$ which runc
/usr/local/sbin/runc

$ runc --version
runc version 1.1.13
commit: v1.1.13-0-g58aa9203
spec: 1.0.2-dev
go: go1.23.0
libseccomp: 2.5.4
```

For further details about building runc, refer to [RUNC.md](docs/RUNC.md) in the
docs directory.

When working with `ctr`, the simple test client we just built, don't forget to start the daemon!

```sh
containerd --config config.toml
```

# Testing containerd

During the automated CI the unit tests and integration tests are run as part of the PR validation. As a developer you can run these tests locally by using any of the following `Makefile` targets:
 - `make test`: run all non-integration tests that do not require `root` privileges
 - `make root-test`: run all non-integration tests which require `root`
 - `make integration`: run all tests, including integration tests and those which require `root`. `TESTFLAGS_PARALLEL` can be used to control parallelism. For example, `TESTFLAGS_PARALLEL=1 make integration` will lead a non-parallel execution. The default value of `TESTFLAGS_PARALLEL` is **8**.
 - `make cri-integration`: [CRI Integration Tests](https://github.com/containerd/containerd/blob/main/docs/cri/testing.md#cri-integration-test) run cri integration tests

To execute a specific test or set of tests you can use the `go test` capabilities
without using the `Makefile` targets. The following examples show how to specify a test
name and also how to use the flag directly against `go test` to run root-requiring tests.

```sh
# run the test <TEST_NAME>:
go test	-v -run "<TEST_NAME>" ./path/to/package
# enable the root-requiring tests:
go test -v -run ./path/to/package -test.root
```

Example output from directly running `go test` to execute the `TestContainerList` test:

```sh
sudo go test -v -run "TestContainerList" ./integration/client -test.root
=== RUN   TestContainerList
--- PASS: TestContainerList (0.00s)
PASS

ok      github.com/containerd/containerd/v2/integration/client  2.584s
```

> *Note*: in order to run `sudo go` you need to
> - either keep user PATH environment variable. ex: `sudo "PATH=$PATH" env go test <args>`
> - or use `go test -exec` ex: `go test -exec sudo -v -run "TestTarWithXattr" ./pkg/archive -test.root`

## Additional tools

### containerd-stress
In addition to `go test`-based testing executed via the `Makefile` targets, the `containerd-stress` tool is available and built with the `all` or `binaries` targets and installed during `make install`.

With this tool you can stress a running containerd daemon for a specified period of time, selecting a concurrency level to generate stress against the daemon. The following command is an example of having five workers running for two hours against a default containerd gRPC socket address:

```sh
containerd-stress -c 5 -d 120m
```

For more information on this tool's options please run `containerd-stress --help`.

### bucketbench
[Bucketbench](https://github.com/estesp/bucketbench) is an external tool which can be used to drive load against a container runtime, specifying a particular set of lifecycle operations to run with a specified amount of concurrency. Bucketbench is more focused on generating performance details than simply inducing load against containerd.

Bucketbench differs from the `containerd-stress` tool in a few ways:
 - Bucketbench has support for testing the Docker engine, the `runc` binary, and containerd 0.2.x (via `ctr`) and 1.0 (via the client library) branches.
 - Bucketbench is driven via configuration file that allows specifying a list of lifecycle operations to execute. This can be used to generate detailed statistics per-command (e.g. start, stop, pause, delete).
 - Bucketbench generates detailed reports and timing data at the end of the configured test run.

More details on how to install and run `bucketbench` are available at the [GitHub project page](https://github.com/estesp/bucketbench).
