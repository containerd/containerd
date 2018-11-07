# Running containerd as a non-root user

A non-root user can execute containerd by using [`user_namespaces(7)`](http://man7.org/linux/man-pages/man7/user_namespaces.7.html).

For example [RootlessKit](https://github.com/rootless-containers/rootlesskit) can be used for setting up a user namespace (along with mount namespace and optionally network namespace). Please refer to RootlessKit documentation for further information.

## Daemon

```console
$ rootlesskit --net=slirp4netns --copy-up=/etc \
  --state-dir=/run/user/1001/rootlesskit-containerd \
  containerd -c config.toml
```

* `--net=slirp4netns --copy-up=/etc` is only required when you want to unshare network namespaces
* Depending on the containerd plugin configuration, you may also need to add more `--copy-up` options, e.g. `--copy-up=/run`, which mounts a writable tmpfs on `/run`, with symbolic links to the files under the `/run` on the parent namespace.
* `--state-dir` is set to a random directory under `/tmp` if unset. RootlessKit writes the PID to a file named `child_pid` under this directory.
* You need to provide `config.toml` with your own path configuration. e.g.
```toml
root = "/home/penguin/.local/share/containerd"
state = "/run/user/1001/containerd"

[grpc]
  address = "/run/user/1001/containerd/containerd.sock"

[plugins]
  [plugins.linux]
    runtime_root = "/run/user/1001/containerd/runc"
```

## Client

A client program such as `ctr` also needs to be executed inside the daemon namespaces.
```console
$ nsenter -U --preserve-credentials -m -n -t $(cat /run/user/1001/rootlesskit-containerd/child_pid)
$ export CONTAINERD_SNAPSHOTTER=native
$ ctr -a /run/user/1001/containerd/containerd.sock pull docker.io/library/ubuntu:latest
$ ctr -a /run/user/1001/containerd/containerd.sock run -t --rm --fifo-dir /tmp/foo-fifo --cgroup "" docker.io/library/ubuntu:latest foo
```

* `overlayfs` snapshotter does not work inside user namespaces, except on Ubuntu kernel
