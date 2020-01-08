# Running containerd as a non-root user

A non-root user can execute containerd by using [`user_namespaces(7)`](http://man7.org/linux/man-pages/man7/user_namespaces.7.html).

For example [RootlessKit](https://github.com/rootless-containers/rootlesskit) can be used for setting up a user namespace (along with mount namespace and optionally network namespace). Please refer to RootlessKit documentation for further information.

See also [Rootless Docker documentation](https://docs.docker.com/engine/security/rootless/).

## Daemon

```console
$ rootlesskit --net=slirp4netns --copy-up=/etc --copy-up=/run \
  --state-dir=/run/user/1001/rootlesskit-containerd \
  sh -c "rm -rf /run/containerd; containerd -c config.toml"
```

* `--net=slirp4netns --copy-up=/etc` is only required when you want to unshare network namespaces
* `--copy-up=/DIR` mounts a writable tmpfs on `/DIR` with symbolic links to the files under the `/DIR` on the parent namespace
  so that the user can add/remove files under `/DIR` in the mount namespace.
  `--copy-up=/etc` and `--copy-up=/run` are needed on typical setup.
  Depending on the containerd plugin configuration, you may also need to add more `--copy-up` options.
* `rm -rf /run/containerd` is required for v2 shim as a workaround for [#2767](https://github.com/containerd/containerd/issues/2767).
  This command removes the "copied-up" symbolic link to `/run/containerd` on the parent namespace (if exists), which cannot be accessed by non-root users.
  The actual `/run/containerd` directory on the host is not affected.
* `--state-dir` is set to a random directory under `/tmp` if unset. RootlessKit writes the PID to a file named `child_pid` under this directory.
* You need to provide `config.toml` with your own path configuration. e.g.
```toml
version = 2
root = "/home/penguin/.local/share/containerd"
state = "/run/user/1001/containerd"

[grpc]
  address = "/run/user/1001/containerd/containerd.sock"
```

## Client

A client program such as `ctr` also needs to be executed inside the daemon namespaces.
```console
$ nsenter -U --preserve-credentials -m -n -t $(cat /run/user/1001/rootlesskit-containerd/child_pid)
$ export CONTAINERD_ADDRESS=/run/user/1001/containerd/containerd.sock
$ export CONTAINERD_SNAPSHOTTER=native
$ ctr images pull docker.io/library/ubuntu:latest
$ ctr run -t --rm --fifo-dir /tmp/foo-fifo --cgroup "" docker.io/library/ubuntu:latest foo
```

* `overlayfs` snapshotter does not work inside user namespaces, except on Ubuntu and Debian kernels.
  However, [`fuse-overlayfs` snapshotter](https://github.com/AkihiroSuda/containerd-fuse-overlayfs) can be used instead if running kernel >= 4.18.
