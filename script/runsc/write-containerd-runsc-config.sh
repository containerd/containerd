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
# Writes a containerd config.toml for use with runsc (gVisor) into the path
# given as the first argument.  The second argument controls the cgroup driver:
# pass "systemd" to enable the systemd driver, anything else selects cgroupfs.
#
# Usage: write-containerd-runsc-config.sh <output_path> <cgroup_driver>
#
# The runsc shim config (including platform and systemd-cgroup) must already
# exist at /etc/containerd/runsc/config.toml before containerd is started.
# Use write-runsc-shim-config.sh to write it.
set -eu -o pipefail

output=${1:?output path argument is required}
# Normalize to a bare TOML boolean: accept "true"/"systemd" → true, anything else → false.
case "${2:-false}" in
  true|systemd) systemd_cgroup=true ;;
  *)            systemd_cgroup=false ;;
esac

cat > "${output}" <<EOF
version = 2

[plugins."io.containerd.snapshotter.v1.overlayfs"]
  slow_chown = true

[plugins."io.containerd.grpc.v1.cri"]
  enable_selinux = false

[plugins."io.containerd.grpc.v1.cri".containerd]
  default_runtime_name = "runsc"

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
  SystemdCgroup = ${systemd_cgroup}

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
  runtime_type = "io.containerd.runsc.v1"

# Point the runsc shim at its own config file so that all runsc-specific
# settings (platform, systemd-cgroup, etc.) are read from one place.
# This prevents containerd from overriding the shim config via ConfigBody.
# Note: go-toml maps "ConfigPath" (not "config_path") to runtimeoptions.Options.ConfigPath.
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc.options]
  ConfigPath = "/etc/containerd/runsc/config.toml"
EOF
