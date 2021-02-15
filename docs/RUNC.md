containerd is built with OCI support and with support for advanced features provided by [runc](https://github.com/opencontainers/runc).

We depend on a specific `runc` version when dealing with advanced features.  You should have a specific runc build for development.  The current supported runc commit is described in [`go.mod`](../go.mod). Please refer to the line that starts with `github.com/opencontainers/runc`.

For more information on how to clone and build runc see the runc Building [documentation](https://github.com/opencontainers/runc#building).

Note: before building you may need to install additional support, which will vary by platform. For example, you may need to install `libseccomp` e.g. `libseccomp-dev` for Ubuntu.

## building

From within your `opencontainers/runc` repository run:

```bash
make && sudo make install
```

Starting with runc 1.0.0-rc93, the "selinux" and "apparmor" buildtags have been
removed, and runc builds have SELinux, AppArmor, and seccomp support enabled
by default. Note that "seccomp" can be disabled by passing an empty `BUILDTAGS`
make variable, but is highly recommended to keep enabled.

By default, runc is compiled with kernel-memory limiting support enabled. This
functionality is deprecated in kernel 5.4 and up, and is known to be broken on
RHEL7 and CentOS 7 3.10 kernels. For these kernels, we recommend disabling kmem
support using the `nokmem` build-tag. When doing so, be sure to set the `seccomp`
build-tag to enable seccomp support, for example:

```sh
make BUILDTAGS='nokmem seccomp' && make install
```

For details about the `nokmem` build-tag, refer to [opencontainers/runc#2594](https://github.com/opencontainers/runc/pull/2594).
For further details on building runc, refer to the [build instructions in the runc README](https://github.com/opencontainers/runc#building).

After an official runc release we will start pinning containerd support to a specific version but various development and testing features may require a newer runc version than the latest release.  If you encounter any runtime errors, please make sure your runc is in sync with the commit/tag provided in this document.
