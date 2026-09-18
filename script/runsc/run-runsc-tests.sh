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
# Runs the integration-runsc CI test suite locally against an environment
# provisioned by setup-runsc-test-env.sh.
#
# Usage: sudo CGROUP_DRIVER=systemd script/runsc/run-runsc-tests.sh
#
# Optional: set FOCUS to run a subset of cri-integration tests, e.g.
#        sudo FOCUS=TestContainerPrivileged ... run-runsc-tests.sh
#
set -eux -o pipefail

if [[ "$(id -u)" != "0" ]]; then
	echo "must be run as root (use sudo)" >&2
	exit 1
fi

: "${CGROUP_DRIVER:=systemd}"
: "${FOCUS:=}"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" > /dev/null 2>&1; pwd -P)"
containerd_dir="$(cd -- "${script_dir}/../.." > /dev/null 2>&1; pwd -P)"

export GOPATH="${GOPATH:-/go}"
export PATH="/usr/local/go/bin:${GOPATH}/bin:/usr/local/bin:/usr/local/sbin:${PATH}"

report_dir="${containerd_dir}/report-runsc-${CGROUP_DRIVER}"
mkdir -p "${report_dir}"

cd "${containerd_dir}"

echo "==> CRI integration tests (CGROUP_DRIVER=${CGROUP_DRIVER})"

cfg=$(mktemp /tmp/containerd-config-runsc-XXXXXX.toml)
trap 'rm -f "${cfg}"' EXIT

# Use systrap platform to avoid KVM-specific failures on hosts without KVM.
"${script_dir}/write-runsc-shim-config.sh" "${CGROUP_DRIVER}" systrap
"${script_dir}/write-containerd-runsc-config.sh" "${cfg}" "${CGROUP_DRIVER}"

FOCUS="${FOCUS}" \
CONTAINERD_CONFIG_FILE="${cfg}" \
RUNTIME=runsc \
RUNTIME_TYPE=io.containerd.runsc.v1 \
	make cri-integration

echo "==> critest (CGROUP_DRIVER=${CGROUP_DRIVER})"
CGROUP_DRIVER="${CGROUP_DRIVER}" \
	"${script_dir}/critest-runsc.sh" "${report_dir}"

echo ""
echo "All tests passed. Reports in: ${report_dir}"
