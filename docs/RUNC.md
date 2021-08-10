# Runc version requirements for containerd

containerd is built with OCI support and with support for advanced features
provided by the [runc container runtime](https://github.com/opencontainers/runc).

Development (`-dev`) and pre-releases of containerd may depend features in `runc`
that have not yet been released, and may require a specific runc build. The version
of runc that is tested against in our CI can be found in the [`script/setup/runc-version`](../script/setup/runc-version)
file, which may point to a git-commit (for pre releases) or tag in the runc
repository.

For regular (non-pre-)releases of containerd releases, we attempt to use released
(tagged) versions of runc. We recommend using a version of runc that's equal to
or higher than the version of runc described in [`script/setup/runc-version`](../script/setup/runc-version).

If you encounter any runtime errors, make sure your runc is in sync with the
commit or tag provided in that file.

If you do not have the correct version of `runc` installed, you can refer to the
["building" section in the runc documentation](https://github.com/opencontainers/runc#building)
to learn how to build `runc` from source.

runc builds have [SELinux](https://en.wikipedia.org/wiki/Security-Enhanced_Linux),
[AppArmor](https://en.wikipedia.org/wiki/AppArmor), and [seccomp](https://en.wikipedia.org/wiki/seccomp)
support enabled by default.

Note that "seccomp" can be disabled by passing an empty `BUILDTAGS` make
variable, but is highly recommended to keep enabled.

Use the output of the `runc --version` output to verify if your version of runc
has seccomp enabled. For example:

```sh
$ runc --version
runc version 1.0.1
commit: v1.0.1-0-g4144b638
spec: 1.0.2-dev
go: go1.16.6
libseccomp: 2.4.4
```
