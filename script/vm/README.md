# VM scripts

The scripts in this directory run the containerd integration tests inside a
virtual machine (Fedora or an EL distribution, with SELinux Enforcing by
default). They are used by the `integration-vm` job in
`.github/workflows/ci.yml`, but do not depend on [Lima](https://lima-vm.io)
and can be used with other VM environments too.

## Local usage with Lima

```bash
# Boot a plain VM. template:almalinux-8, -9, and -10 are tested too.
# --plain keeps the guest pristine (no guest agent, no mounts, no port forwards).
limactl start --plain --name=default --cpus=2 --memory=4 --disk=60 template:fedora-44

# Copy the source tree into the guest.
limactl cp -r . default:containerd
lima bash -c 'sudo mkdir -p /go/src/github.com/containerd && sudo mv ~/containerd /go/src/github.com/containerd/containerd'

export LIMA_WORKDIR=/go/src/github.com/containerd/containerd

# Provision the guest (packages, Go, runc, CNI plugins, cri-tools, containerd, ...).
lima sudo script/vm/provision.sh

# Run the test suites.
lima sudo script/vm/test-integration.sh
lima sudo CGROUP_DRIVER=systemd script/vm/test-cri-integration.sh
lima sudo CGROUP_DRIVER=systemd script/vm/test-cri.sh

# Clean up.
limactl delete -f default
```

## Environment variables

- `provision.sh`: `GO_VERSION`, `RUNC_FLAVOR` (`runc` or `crun`), `SELINUX`
  (`Enforcing`, `Permissive`, or `Disabled`), `INSTALL_PACKAGES`
- `test-integration.sh`: `RUNC_FLAVOR`
- `test-cri-integration.sh`: `CGROUP_DRIVER` (empty or `systemd`), `RUNC_FLAVOR`
- `test-cri.sh`: `CGROUP_DRIVER` (empty or `systemd`), `REPORT_DIR`
