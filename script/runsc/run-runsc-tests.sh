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

# keep in sync with write-runsc-shim-config.sh
_runsc_root=/run/containerd-runsc-test
# write-runsc-shim-config.sh removes /run/containerd/runsc/config.toml (the
# higher-precedence shim config path) before writing the test config to /etc.
# Save it now so the EXIT trap can restore it.
_runsc_runtime_cfg=/run/containerd/runsc/config.toml
_runsc_runtime_cfg_saved=
if [[ -f "${_runsc_runtime_cfg}" ]]; then
	_runsc_runtime_cfg_saved="$(mktemp /tmp/containerd-runsc-runtime-cfg-XXXXXX.toml)"
	cp "${_runsc_runtime_cfg}" "${_runsc_runtime_cfg_saved}"
fi

# _cleanup_runsc_root kills any surviving shims and runsc workers that hold
# mounts under _runsc_root busy, then removes the directory.  Safe to call
# multiple times; all operations are best-effort (|| true).
_cleanup_runsc_root() {
	if command -v runsc >/dev/null 2>&1; then
		local _ids
		_ids="$(runsc --root="${_runsc_root}" list --format '{{.ID}}' 2>/dev/null || true)"
		local _id
		for _id in ${_ids}; do
			runsc --root="${_runsc_root}" delete --force "${_id}" 2>/dev/null || true
		done
	fi
	# Kill shims before removing the root; they can hold state mounts busy.
	while IFS= read -r _pid; do
		[[ -z "${_pid}" ]] && continue
		kill -KILL "${_pid}" 2>/dev/null || true
	done < <(
		pgrep -f "containerd-shim-runsc-v1.*containerd-test" || true
		pgrep -f "runsc.*--root=${_runsc_root}" || true
	)
	# null-netns is a network-namespace bind-mount that findmnt may not
	# enumerate; unmount and kill holders explicitly before the retry loop.
	umount -l "${_runsc_root}/null-netns" 2>/dev/null || true
	fuser -km "${_runsc_root}/null-netns" 2>/dev/null || true
	local _i
	for _i in {1..20}; do
		if command -v findmnt >/dev/null 2>&1; then
			findmnt -R -n -o TARGET "${_runsc_root}" 2>/dev/null | sort -r |
				while IFS= read -r _mount; do
					[[ -z "${_mount}" ]] && continue
					umount -l "${_mount}" 2>/dev/null || true
				done
		fi
		rm -rf "${_runsc_root:?}" && return
		sleep 1
	done
}

cfg=$(mktemp /tmp/containerd-config-runsc-XXXXXX.toml)
# On exit: restore the pre-existing runtime shim config (if any was saved),
# clean up the runsc root, and remove the temp containerd config.
trap '
	if [[ -n "${_runsc_runtime_cfg_saved}" ]]; then
		mv "${_runsc_runtime_cfg_saved}" "${_runsc_runtime_cfg}" 2>/dev/null || true
	fi
	_cleanup_runsc_root
	rm -f "${cfg}"
' EXIT

"${script_dir}/write-runsc-shim-config.sh" "${CGROUP_DRIVER}" systrap
"${script_dir}/write-containerd-runsc-config.sh" "${cfg}" "${CGROUP_DRIVER}"

FOCUS="${FOCUS}" \
CONTAINERD_CONFIG_FILE="${cfg}" \
RUNTIME=runsc \
RUNTIME_TYPE=io.containerd.runsc.v1 \
	make cri-integration

echo "==> Cleaning up runsc state before critest (CGROUP_DRIVER=${CGROUP_DRIVER})"
_cleanup_runsc_root
test ! -e "${_runsc_root}"

echo "==> critest (CGROUP_DRIVER=${CGROUP_DRIVER})"
CGROUP_DRIVER="${CGROUP_DRIVER}" \
	"${script_dir}/critest-runsc.sh" "${report_dir}"

echo ""
echo "All tests passed. Reports in: ${report_dir}"
