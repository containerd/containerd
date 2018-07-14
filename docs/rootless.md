# Rootless mode (Experimental)

Requirements:
- [runc (May 30, 2018)](https://github.com/opencontainers/runc/commit/ecd55a4135e0a26de884ce436442914f945b1e76) or later
- Some distros such as Debian (excluding Ubuntu) and Arch Linux require `echo 1 > /proc/sys/kernel/unprivileged_userns_clone`
- `newuidmap` and `newgidmap` need to be installed on the host. These commands are provided by the `uidmap` package on most distros.
- `/etc/subuid` and `/etc/subgid` should contain >= 65536 sub-IDs. e.g. `penguin:231072:65536`.
- To run in a Docker container with non-root `USER`, `docker run --privileged` is still required. See also Jessie's blog: https://blog.jessfraz.com/post/building-container-images-securely-on-kubernetes/

Daemon-side remarks:

* The data dir will be set to `/home/$USER/.local/share/containerd` by default.
* The address will be set to `/run/user/$UID/containerd/containerd.sock` by default.
* CRI plugin is not supported yet.
* `overlayfs` snapshotter is not supported except on [Ubuntu-flavored kernel](http://kernel.ubuntu.com/git/ubuntu/ubuntu-artful.git/commit/fs/overlayfs?h=Ubuntu-4.13.0-25.29&id=0a414bdc3d01f3b61ed86cfe3ce8b63a9240eba7). `native` snapshotter should work on non-Ubuntu kernel.

Go client library remarks:
* `oci.WithRootless()` removes Cgroups configuration. However, you can still set Cgroups configurations after calling `oci.WithRootless()`, if the permission bits are preconfigured on cgroup filesystems.

Network namespace remarks:
* To set up NAT across the host and the containers, you need to use either a TAP with a usermode network stack (slirp) or a SUID helper. See [RootlessKit](https://github.com/rootless-containers/rootlesskit).

## Usage

### Terminal 1:

```
$ unshare -U -m
unshared$ echo $$ > /tmp/pid
```

Unsharing mountns (and userns) is required for mounting filesystems without real root privileges.

### Terminal 2:

```
$ id -u
1001
$ grep $(whoami) /etc/subuid
penguin:231072:65536
$ grep $(whoami) /etc/subgid
penguin:231072:65536
$ newuidmap $(cat /tmp/pid) 0 1001 1 1 231072 65536
$ newgidmap $(cat /tmp/pid) 0 1001 1 1 231072 65536
```

### Terminal 1:

```
unshared# containerd
```

### Terminal 2:

```
$ nsenter -U -m -t $(cat /tmp/pid)
unshared# ctr -a /run/user/1001/containerd/containerd.sock images pull docker.io/library/debian:latest
unshared# ctr -a /run/user/1001/containerd/containerd.sock run -t --rm --rootless --net-host docker.io/library/debian:latest foo
foo#
```

## Usage ([RootlessKit](https://github.com/rootless-containers/rootlesskit))

RootlessKit can be used for executing `unshare` and `newuidmap/newgidmap` at once.
RootlessKit also supports unsharing the network namespace with usermode NAT such as [VPNKit](https://github.com/moby/vpnkit)
and [libvdeplug_slirp](https://github.com/moby/vpnkit).


### Terminal 1:

The following example is tested with RootlessKit [`20b0fc24b305b031a61ef1a1ca456aadafaf5e77`](https://github.com/rootless-containers/rootlesskit/tree/20b0fc24b305b031a61ef1a1ca456aadafaf5e77).

```
$ rootlesskit --state-dir=/tmp/foo --net=vpnkit --copy-up=/etc containerd
```

### Terminal 2:

```
$ nsenter -U -m -n -t $(cat /tmp/foo/child_pid)
unshared# ctr -a /run/user/1001/containerd/containerd.sock images pull docker.io/library/debian:latest
unshared# ctr -a /run/user/1001/containerd/containerd.sock run -t --rm --rootless --net-host docker.io/library/debian:latest foo
foo#
```
