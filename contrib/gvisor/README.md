# gVisor tests

Run the gVisor CI tests locally in a privileged container.
You need Linux with cgroup v2 and a rootful Docker daemon.

From the repository root, run:

```sh
contrib/gvisor/test.sh
```

The script builds the image, starts a container with systemd, and runs CRI
integration tests, then critest. It stops if either test suite fails.
Shim grouping is enabled, so containers in a pod share one shim.
The script prints a test summary at the end.
The container and its volumes are removed on exit.

The cgroup driver is `systemd` by default. To use `cgroupfs`:

```sh
CGROUP_DRIVER=cgroupfs contrib/gvisor/test.sh
```

Use `FOCUS` to select CRI integration tests and `FOCUS_TEST` to select critest tests.

The script uses the same install, config, and test scripts as the
[`integration-runsc` CI job](../../.github/workflows/ci.yml).
Keep the Go version in [Dockerfile](Dockerfile) the same as
[CI](../../.github/actions/install-go/action.yml).
