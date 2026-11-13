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
# Runs critest against containerd configured for Kata Containers.
#
# Usage: critest-kata.sh <report_dir>
#
# Required environment:
#   CGROUP_DRIVER           "cgroupfs" or "systemd"
#   CONTAINERD_CONFIG_FILE  config from write-containerd-kata-config.sh
#
set -eu -o pipefail

report_dir=${1:?report_dir argument is required}

: "${CGROUP_DRIVER:?CGROUP_DRIVER must be set to cgroupfs or systemd}"
: "${CONTAINERD_CONFIG_FILE:?CONTAINERD_CONFIG_FILE must point to the kata containerd config}"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" > /dev/null 2>&1; pwd -P)"

# Tests that fail under Kata. Pods run in a guest VM, so host namespaces and
# host-side policy don't apply. The causes noted below are unverified.
# TODO: Refine from CI results.
skip_tests=(
	'HostIpc is true'
	'HostNetwork is true'
	'HostPID'
	'UserNamespaces.*NamespaceMode_POD'
	'portforward'
	# host AppArmor/seccomp not enforced in the guest
	'should enforce a apparmor_profile blocking writes'
	'should support seccomp localhost profile on the container'
	'should support seccomp default on the container'
	'runtime should support an seccomp profile that blocks setting hostname with SYS_ADMIN'
	# host mounts go through virtio-fs
	"mount with 'rshared' should support propagation"
	'should support non-recursive readonly mounts'
	# container_oom_events_total not reported
	'runtime should support listing pod sandbox metrics'
	# OOM status is not read: "no metrics received" from the agent (cgroupfs driver only)
	'should terminate with exitCode 137 and reason OOMKilled'
)
skip_arg=$(IFS='|'; echo "${skip_tests[*]}")

if [[ -n "${SKIP_TEST:-}" ]]; then
	SKIP_TEST="${SKIP_TEST}|${skip_arg}"
else
	SKIP_TEST="${skip_arg}"
fi
export SKIP_TEST

if [[ -z "${GINKGO_NODES:-}" ]]; then
	# each pod boots a VM, keep parallelism low
	GINKGO_NODES=2
fi
export GINKGO_NODES

exec "${script_dir}/../critest.sh" "${report_dir}"
