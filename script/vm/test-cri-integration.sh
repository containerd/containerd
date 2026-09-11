#!/usr/bin/env bash

#   Copyright The containerd Authors.

#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at

#       http://www.apache.org/licenses/LICENSE-2.0

#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.

#
# Runs the CRI integration tests inside a virtual machine
# provisioned with provision.sh.
#
set -eux -o pipefail

if [[ "$(id -u)" != "0" ]]; then
	echo "must be executed as the root user" >&2
	exit 1
fi

script_dir="$(cd -- "$(dirname -- "$0")" > /dev/null 2>&1; pwd -P)"
containerd_dir="$(cd -- "${script_dir}/../.." > /dev/null 2>&1; pwd -P)"

export CGROUP_DRIVER="${CGROUP_DRIVER:-}"
export TEST_RUNTIME="${TEST_RUNTIME:-}"
export GOTEST="${GOTEST:-go test}"
export GITHUB_WORKSPACE="${GITHUB_WORKSPACE:-}"

export GOPATH="${GOPATH:-/go}"
export PATH="/usr/local/go/bin:${GOPATH}/bin:/usr/local/bin:/usr/local/sbin:${PATH}"

# Path to a temporary runsc containerd config; set in the runsc branch below.
runsc_config=

cleanup() {
	# Remove the temporary runsc config file if one was created.
	[[ -n "${runsc_config}" ]] && rm -f "${runsc_config}"
	# runsc bind-mounts network namespace files (e.g. null-netns) under
	# /run/containerd/runsc that are still busy when the shim exits.
	# findmnt --submounts only finds directory mountpoints, not file bind-mounts,
	# so read /proc/mounts directly to catch all mount targets under the path,
	# sort deepest-first, and lazily unmount each one.
	awk '$2 ~ "^/run/containerd" {print $2}' /proc/mounts 2>/dev/null |
		sort -r |
		xargs -r umount -l 2>/dev/null || true
	rm -rf /var/lib/containerd* /run/containerd* /tmp/containerd* /tmp/test* /tmp/failpoint* /tmp/nri*
}

trap cleanup EXIT
cleanup
cd "${containerd_dir}"
# cri-integration.sh executes containerd from ./bin, not from $PATH .
make BUILDTAGS="seccomp selinux no_btrfs no_devmapper no_zfs" binaries bin/cri-integration.test
chcon -v -t container_runtime_exec_t ./bin/{containerd,containerd-shim*}

if [[ "${TEST_RUNTIME}" == "io.containerd.runsc.v1" ]]; then
	# runsc requires its own shim and a named runtime handler.  utils.sh only
	# wires CONTAINERD_RUNTIME into the runc handler stanza — it has no
	# knowledge of TEST_RUNTIME=io.containerd.runsc.v1.  Build the full config
	# ourselves and hand it to cri-integration.sh via CONTAINERD_CONFIG_FILE
	# so that utils.sh skips its own config generation.
	runsc_config=$(mktemp /tmp/containerd-config-runsc-XXXXXX.toml)

	cat >"${runsc_config}" <<EOF
version = 2

[plugins."io.containerd.grpc.v1.cri"]
  drain_exec_sync_io_timeout = "10s"
  # gVisor does not support SELinux labels in the OCI spec; always disable.
  enable_selinux = false

[plugins."io.containerd.snapshotter.v1.overlayfs"]
  slow_chown = true

[plugins."io.containerd.grpc.v1.cri".containerd]
  default_runtime_name = "runsc"

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
  runtime_type = "io.containerd.runsc.v1"
EOF

	# Always write the runsc options table so that ConfigBody is non-empty and
	# getCgroupDriverFromRuntimeHandlerOpts returns the correct driver rather
	# than falling back to systemd auto-detection.
	systemd_cgroup=false
	if [[ "${CGROUP_DRIVER}" == "systemd" ]]; then
		systemd_cgroup=true
	fi
	cat >>"${runsc_config}" <<EOF

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc.options]
  SystemdCgroup = ${systemd_cgroup}
EOF

	# Write the runsc shim config file so the shim actually uses the requested
	# cgroup driver.  containerd-shim-runsc-v1 does not read SystemdCgroup from
	# the containerd options table; it reads its own TOML config from well-known
	# paths, with /etc/containerd/runsc/config.toml as the fallback.
	mkdir -p /etc/containerd/runsc
	cat >/etc/containerd/runsc/config.toml <<EOF

[runsc_config]
  systemd-cgroup = "${systemd_cgroup}"
EOF

	cat >>"${runsc_config}" <<EOF

[plugins."io.containerd.nri.v1.nri"]
  disable = false
  socket_path = "/var/run/nri-test.sock"
  plugin_path = "/no/pre-launched/nri/plugins"
EOF

	CONTAINERD_CONFIG_FILE="${runsc_config}" \
		RUNTIME=runsc \
		./script/test/cri-integration.sh
else
	CONTAINERD_RUNTIME=io.containerd.runc.v2 ./script/test/cri-integration.sh
fi
cleanup
