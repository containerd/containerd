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
# Runs critest against a containerd instance configured for runsc (gVisor).
# Skips tests for features that gVisor does not support.
#
# Usage: critest-runsc.sh <report_dir>
#
# Required environment variable:
#   CGROUP_DRIVER  — must be either "cgroupfs" or "systemd"
#
# The containerd and runsc shim configs must already be written by the caller.
# Pass the containerd config through CONTAINERD_CONFIG_FILE.
#
set -eu -o pipefail

report_dir=${1:?report_dir argument is required}

: "${CGROUP_DRIVER:?CGROUP_DRIVER must be set to cgroupfs or systemd}"
: "${CONTAINERD_CONFIG_FILE:?CONTAINERD_CONFIG_FILE must point to the runsc containerd config}"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" > /dev/null 2>&1; pwd -P)"

# Fail fast if a runsc sandbox remains before critest. The root directory itself
# may remain because runsc keeps its shared null network namespace there.
_runsc_root=/run/containerd-runsc-test
if [[ -e "${_runsc_root}" ]]; then
	if ! _runsc_ids="$(runsc --root="${_runsc_root}" list --quiet)"; then
		echo "ERROR: unable to inspect runsc root ${_runsc_root}" >&2
		exit 1
	fi
	if [[ -n "${_runsc_ids}" ]]; then
		echo "ERROR: stale runsc sandboxes found in ${_runsc_root}:" >&2
		echo "${_runsc_ids}" >&2
		exit 1
	fi
fi

# Skip tests for features gVisor does not support.
# See script/vm/test-cri.sh for the full rationale.
skip_tests=(
	'HostIpc is true'
	'HostNetwork is true'
	'HostPID'
	'SELinux'
	'should support safe sysctls'
	'should support unsafe sysctls'
	'mount propagation'
	'rshared'
	'non-recursive readonly'
	'adding capability'
	'MaskedPaths'
	'ReadonlyPaths'
	'SeccompProfilePath'
	'UserNamespaces'
	'portforward'
	'listing pod sandbox metrics'
	'listing container stats|listing stats for'
	'should enforce a apparmor_profile blocking writes'
	'should allow privilege escalation when false'
	'runtime should support Privileged is true'
)
# Skip OOM tests for runsc entirely - it intentionally triggers container OOM
# kills which causes host memory pressure on CI runners (both cgroupfs and systemd).
skip_tests+=(
   'should terminate with exitCode 137'
   'OOMKilled'
)
skip_arg=$(IFS='|'; echo "${skip_tests[*]}")

if [[ -n "${SKIP_TEST:-}" ]]; then
	SKIP_TEST="${SKIP_TEST}|${skip_arg}"
else
	SKIP_TEST="${skip_arg}"
fi
export SKIP_TEST

if [[ -z "${GINKGO_NODES:-}" ]]; then
	# Cap default parallelism to avoid host resource exhaustion on high-core runners.
	_procs=$(($(nproc) + 2))
	GINKGO_NODES=$(( _procs > 8 ? 8 : _procs ))
fi
export GINKGO_NODES

exec "${script_dir}/../critest.sh" "${report_dir}"
