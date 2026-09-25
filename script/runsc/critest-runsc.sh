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
# The runsc shim config (/etc/containerd/runsc/config.toml) must already be
# written by the caller (e.g. write-runsc-shim-config.sh) before invoking
# this script.
#
set -eu -o pipefail

report_dir=${1:?report_dir argument is required}
mkdir -p "${report_dir}"

: "${CGROUP_DRIVER:?CGROUP_DRIVER must be set to cgroupfs or systemd}"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" > /dev/null 2>&1; pwd -P)"

BDIR="$(mktemp -d -p "$PWD")"
containerd_pid=

function cleanup() {
	# Shut down containerd gracefully first.  Containerd's own shutdown path
	# sends exit/delete requests to its runtime-v2 shims before it terminates,
	# so a clean SIGTERM is the most reliable way to reap shims.
	#
	# Note: runtime-v2 shims are launched with Setpgid:true (see
	# core/runtime/v2/command_unix.go), so each shim has a PGID different from
	# containerd's — a process-group kill of containerd's PGID cannot reach them.
	if [[ -n "${containerd_pid}" ]]; then
		kill -TERM "${containerd_pid}" 2>/dev/null || true
		# Wait for containerd to exit and reap it.  'wait' returns immediately
		# when the process is already a zombie (unlike kill -0, which succeeds
		# for zombies — causing a poll loop to always run to completion before
		# the redundant SIGKILL).  A background sleep provides the timeout: if
		# containerd has not exited within 10 s, the sleep exits first and we
		# fall through to SIGKILL; if containerd exits first, we kill the sleep.
		{ sleep 10; kill -KILL "${containerd_pid}" 2>/dev/null || true; } &
		_timeout_pid=$!
		wait "${containerd_pid}" 2>/dev/null || true
		kill "${_timeout_pid}" 2>/dev/null || true
		wait "${_timeout_pid}" 2>/dev/null || true
	fi
	# Last-resort: reap any surviving runsc shims and their child processes.
	# Match only containerd-shim-runsc-v1 processes whose command line contains
	# the BDIR socket address (so we target only *this* test's shims and not
	# shims from concurrent test runs or the containerd process itself, whose
	# command line also contains BDIR but shares the shell's PGID).
	# Each shim is started with Setpgid:true so it has its own PGID; killing
	# that group also reaps any runsc worker subprocesses.
	while IFS= read -r _pid; do
		_pgid=$(ps -o pgid= -p "${_pid}" 2>/dev/null | tr -d ' ') || continue
		if [[ -n "${_pgid}" && "${_pgid}" != "0" ]]; then
			kill -KILL -- "-${_pgid}" 2>/dev/null || true
		fi
	done < <(pgrep -f "containerd-shim-runsc-v1.*${BDIR}" 2>/dev/null || true)
	echo "::group::containerd logs"
	cat "${report_dir}/containerd.log" || true
	echo "::endgroup::"
	rm -rf "${BDIR}"
}
trap cleanup EXIT

mkdir -p "${BDIR}"/{root,state}

# Write the containerd config for runsc using the shared helper.
"${script_dir}/write-containerd-runsc-config.sh" "${BDIR}/config.toml" "${CGROUP_DRIVER}"

/usr/local/bin/containerd \
	-a "${BDIR}/c.sock" \
	--config "${BDIR}/config.toml" \
	--root "${BDIR}/root" \
	--state "${BDIR}/state" \
	--log-level debug &> "${report_dir}/containerd.log" &
containerd_pid=$!

# Wait for containerd to be ready, failing fast if the process has already died.
for i in $(seq 1 10); do
	if ! kill -0 "${containerd_pid}" 2>/dev/null; then
		echo "containerd exited unexpectedly" >&2
		exit 1
	fi
	crictl --runtime-endpoint "${BDIR}/c.sock" info 2>/dev/null && break
	if [[ "${i}" -eq 10 ]]; then
		echo "containerd did not become ready after 10 seconds" >&2
		exit 1
	fi
	sleep 1
done

# Skip tests for features gVisor does not support.
# See script/vm/test-cri.sh for the full rationale.
skip_tests=(
	'HostIpc is true'
	'HostNetwork is true'
	'HostPID'
	'SELinux'
	'should support safe sysctls'
	'should support unsafe sysctls'
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

if [[ -n "${GINKGO_NODES:-}" ]]; then
	ginkgo_procs="${GINKGO_NODES}"
else
	# Cap default parallelism to avoid host resource exhaustion on high-core runners.
	_procs=$(($(nproc) + 2))
	ginkgo_procs=$(( _procs > 8 ? 8 : _procs ))
fi

critest \
	--report-dir "${report_dir}" \
	--runtime-endpoint "unix:///${BDIR}/c.sock" \
	--parallel="${ginkgo_procs}" \
	--ginkgo.skip="${skip_arg}"
