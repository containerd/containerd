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
# Writes /etc/containerd/runsc/config.toml for containerd-shim-runsc-v1.
# The shim reads its own config from this path rather than from the main
# containerd config.toml.
#
# Usage: write-runsc-shim-config.sh <cgroup_driver> [<platform>]
#
#   <cgroup_driver>   pass "systemd" to enable the systemd cgroup driver;
#                     anything else selects cgroupfs.
#   <platform>        optional runsc platform (e.g. "systrap", "kvm").
#                     Omit to let runsc use its compiled-in default.
#                     Use "systrap" on hosts without KVM support (CI runners).
#
# The shim config sets root to a test-specific path (/run/containerd-runsc-test)
# so sandbox state is isolated from the shared default (/run/containerd/runsc).
# Callers that clean up after the test should remove /run/containerd-runsc-test.
#
set -eu -o pipefail

# Keep in sync with cleanup paths in run-runsc-tests.sh and ci.yml.
_runsc_test_root=/run/containerd-runsc-test

SUDO=''
if [[ "$(id -u)" != "0" ]]; then
    SUDO='sudo'
fi

case "${1:-false}" in
  true|systemd) systemd_cgroup='"true"' ;;
  *)            systemd_cgroup='"false"' ;;
esac

platform="${2:-}"

# Remove the higher-precedence runtime path to avoid stale overrides.
${SUDO} rm -f /run/containerd/runsc/config.toml

${SUDO} mkdir -p /etc/containerd/runsc

{
  echo ""
  echo "[runsc_config]"
  echo "  systemd-cgroup = ${systemd_cgroup}"
  echo "  root = \"${_runsc_test_root}\""
  if [[ -n "${platform}" ]]; then
    echo "  platform = \"${platform}\""
  fi
} | ${SUDO} tee /etc/containerd/runsc/config.toml > /dev/null
