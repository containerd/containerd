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
# Writes a containerd config for Kata Containers.
#
# Usage: write-containerd-kata-config.sh <output_path> <cgroup_driver>
#   cgroup_driver: "systemd" or "cgroupfs"
#
# Uses the runtime-rs QEMU config unless $KATA_CONFIG_PATH is set.
# With KATA_DEBUG=true, a copy of it with debug logging enabled is used
# (output goes to the journal, identifier "kata").
set -eu -o pipefail

output=${1:?output path argument is required}
cgroup_driver=${2:?cgroup_driver argument is required: pass "systemd" or "cgroupfs"}
kata_config_path=${KATA_CONFIG_PATH:-/opt/kata/share/defaults/kata-containers/runtime-rs/configuration-qemu-runtime-rs.toml}
# absolute: the shim runs from its bundle directory
if [[ "${kata_config_path}" != /* ]]; then
  kata_config_path="${PWD}/${kata_config_path}"
fi
if [[ "${KATA_DEBUG:-}" == "true" ]]; then
  debug_config="${output}.kata-debug.toml"
  sed 's/^enable_debug = false/enable_debug = true/' "${kata_config_path}" > "${debug_config}"
  kata_config_path="$(cd -- "$(dirname -- "${debug_config}")" && pwd -P)/$(basename -- "${debug_config}")"
fi
# systemd -> true, anything else -> false
case "${cgroup_driver}" in
  true|systemd) systemd_cgroup=true ;;
  *)            systemd_cgroup=false ;;
esac

cat > "${output}" <<EOF
version = 2

[plugins."io.containerd.snapshotter.v1.overlayfs"]
  slow_chown = true

[plugins."io.containerd.grpc.v1.cri"]
  enable_selinux = false

[plugins."io.containerd.nri.v1.nri"]
  disable = false
  socket_path = "/var/run/nri-test.sock"
  plugin_path = "/no/pre-launched/nri/plugins"

[plugins."io.containerd.grpc.v1.cri".containerd]
  default_runtime_name = "kata"

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
  SystemdCgroup = ${systemd_cgroup}

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kata]
  runtime_type = "io.containerd.kata.v2"
  privileged_without_host_devices = true

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kata.options]
  ConfigPath = "${kata_config_path}"
EOF
