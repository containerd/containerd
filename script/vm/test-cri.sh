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
: "${TEST_RUNTIME:=}"
: "${REPORT_DIR:=}"

export GOPATH="${GOPATH:-/go}"
export PATH="/usr/local/go/bin:${GOPATH}/bin:/usr/local/bin:/usr/local/sbin:${PATH}"

# Unmount any bind-mounted network namespace files left behind by a previous
# runsc run (e.g. /run/containerd/runsc/k8s.io/null-netns).  findmnt only
# lists directory mountpoints, so read /proc/mounts directly to catch file
# bind-mounts too.
unmount_containerd_mounts() {
	awk '$2 ~ "^/run/containerd" {print $2}' /proc/mounts 2>/dev/null |
		sort -r |
		xargs -r umount -l 2>/dev/null || true
}

systemctl disable --now containerd || true
unmount_containerd_mounts
rm -rf /var/lib/containerd /run/containerd

cleanup() {
	journalctl -u containerd > /tmp/containerd.log
	cat /tmp/containerd.log
	systemctl stop containerd
	unmount_containerd_mounts
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
if [[ $TEST_RUNTIME == "io.containerd.runsc.v1" ]]; then
	# gVisor does not support: host networking, privileged containers, SELinux,
	# host PID/IPC namespaces, cgroup memory limits (OOMKilled), sysctls,
	# rshared/non-recursive-readonly mount semantics, NET_ADMIN capability
	# (brctl), MaskedPaths, ReadonlyPaths, NoNewPrivs escalation semantics,
	# host seccomp profiles, user namespace idmap mounts, port-forwarding into
	# its own network namespace, or the OOM-events metric.
	skip_tests+=(
		'HostNetwork is true'
		'HostPID'
		'Privileged is true'
		'SELinux'
		'should terminate with exitCode 137 and reason OOMKilled'
		'should support safe sysctls'
		'should support unsafe sysctls'
		'rshared'
		'non-recursive readonly'
		'adding capability'
		'MaskedPaths'
		'ReadonlyPaths'
		'should allow privilege escalation when false'
		'SeccompProfilePath'
		'UserNamespaces'
		'portforward'
		'listing pod sandbox metrics'
	)
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
