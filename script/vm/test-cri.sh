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
# Runs critest against containerd (running as a systemd unit) inside a
# virtual machine provisioned with provision.sh.
#
set -eux -o pipefail

if [[ "$(id -u)" != "0" ]]; then
	echo "must be executed as the root user" >&2
	exit 1
fi

script_dir="$(cd -- "$(dirname -- "$0")" > /dev/null 2>&1; pwd -P)"
containerd_dir="$(cd -- "${script_dir}/../.." > /dev/null 2>&1; pwd -P)"

: "${CGROUP_DRIVER:=}"
: "${REPORT_DIR:=}"

export GOPATH="${GOPATH:-/go}"
export PATH="/usr/local/go/bin:${GOPATH}/bin:/usr/local/bin:/usr/local/sbin:${PATH}"

systemctl disable --now containerd || true
rm -rf /var/lib/containerd /run/containerd

cleanup() {
	journalctl -u containerd > /tmp/containerd.log
	cat /tmp/containerd.log
	systemctl stop containerd
}

selinux=$(getenforce)
if [[ $selinux == Enforcing ]]; then
	setenforce 0
fi
systemctl enable --now "${containerd_dir}/containerd.service"
if [[ $selinux == Enforcing ]]; then
	setenforce 1
fi
trap cleanup EXIT
ctr version

skip_tests=(
	'HostIpc is true'
)
if [[ $CGROUP_DRIVER == "systemd" ]]; then
	skip_tests+=("should terminate with exitCode 137 and reason OOMKilled")
fi
skip_test_args=$(
	IFS='|'
	echo "${skip_tests[*]}"
)
critest_args=(--parallel=$(($(nproc) + 2)) --ginkgo.skip="${skip_test_args}")
if [[ -n $REPORT_DIR ]]; then
	mkdir -p "${REPORT_DIR}"
	critest_args+=(--report-dir="${REPORT_DIR}")
fi
critest "${critest_args[@]}"
