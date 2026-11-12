# gVisor tests

Run the gVisor CI tests locally in a privileged container.
You need Linux with cgroup v2 and a rootful Docker daemon with Buildx and BuildKit.

The runsc runtime uses `privileged_without_host_devices = true` to avoid
automatically passing host devices, including `/dev/nvidia*`, to privileged test
containers. Privileged capabilities remain enabled. The suite does not configure
nvproxy for GPU access.

From the repository root, run:

```sh
contrib/gvisor/test.sh
```

The script builds the image, starts a container with systemd, and runs CRI
integration tests, then critest. It stops if either test suite fails.
Shim grouping is enabled, so containers in a pod share one shim.
The script prints a test summary at the end.
The container and its anonymous volumes are removed on exit. Test output and
critest reports, including `containerd.log`, are kept in a temporary host
directory whose path is printed at exit.

The cgroup driver is `systemd` by default. To use `cgroupfs`:

```sh
CGROUP_DRIVER=cgroupfs contrib/gvisor/test.sh
```

Use `FOCUS` to select CRI integration tests and `FOCUS_TEST` to select critest tests.

The script uses the same install, config, and test scripts as the
[`integration-runsc` CI job](../../.github/workflows/ci.yml).
The Go version is read from [CI](../../.github/actions/install-go/action.yml)
and passed to the Dockerfile as `GO_VERSION`. Docker layers cache installed
dependencies, and a BuildKit cache mount preserves Go build results.

For a host-based setup, follow the installation, configuration, and test steps
in the same CI job.
