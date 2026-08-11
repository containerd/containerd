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

set -eu -o pipefail

DIR="$(dirname "${0}")"
cd "${DIR}"
if ! command -v crictl >/dev/null 2>&1; then
	echo >&2 "ERROR: crictl binary not found"
	exit 1
fi

crictl() {
	command crictl --timeout 60s "$@"
}
TESTDIR=$(mktemp -d -p "${PWD}")
SUCCESS=0
CRIU_PATH=$(command -v criu || true)
CRIU_DIR=""
if [ -n "${CRIU_PATH}" ]; then
	CRIU_DIR=$(dirname "${CRIU_PATH}")
fi

function get_cleaned_path() {
	if [ -z "${CRIU_DIR}" ]; then
		echo "$PATH"
		return
	fi
	local real_criu_dir
	real_criu_dir=$(realpath "${CRIU_DIR}")
	local shadow_dir="${TESTDIR}/shadow_path"
	mkdir -p "${shadow_dir}"
	for file in "${real_criu_dir}"/*; do
		if [ -x "$file" ] && [ "$(basename "$file")" != "criu" ]; then
			ln -s "$file" "${shadow_dir}/" || true
		fi
	done
	local cleaned=""
	local IFS=':'
	for dir in $PATH; do
		if [ -d "$dir" ]; then
			local real_dir
			real_dir=$(realpath "$dir")
			if [ "${real_dir}" == "${real_criu_dir}" ]; then
				cleaned="${cleaned:+$cleaned:}${shadow_dir}"
			else
				cleaned="${cleaned:+$cleaned:}$dir"
			fi
		fi
	done
	echo "${cleaned}"
}

function cleanup() {
	crictl ps -a || true
	stop_containerd
	if [ "${SUCCESS}" == "1" ]; then
		echo PASS
	else
		echo "--> containerd logs"
		if [ -f "${TESTDIR}/containerd.log" ]; then
			sed 's/^/----> \t/' <"${TESTDIR}/containerd.log"
		fi
		echo FAIL
	fi
	find "${TESTDIR}" -name shm -type d -exec umount {} + >/dev/null 2>&1 || true
	find "${TESTDIR}" -name rootfs -type d -exec umount {} + >/dev/null 2>&1 || true
	rm -rf "${TESTDIR}" || true
}
trap cleanup EXIT
export CONTAINERD_ADDRESS="$TESTDIR/c.sock"
export CONTAINER_RUNTIME_ENDPOINT="unix://${CONTAINERD_ADDRESS}"
TEST_IMAGE=ghcr.io/containerd/alpine
TESTDATA=testdata
function start_containerd() {
	local enable_criu="$1"
	local enable_experimental_restore="$2"
	local use_path="$3"
	echo "--> Starting containerd with enable_criu=${enable_criu} enable_experimental_restore_via_create=${enable_experimental_restore}"
	cat >"${TESTDIR}/config.toml" <<EOF
version = 3
[plugins."io.containerd.cri.v1.runtime"]
EOF
	if [ "${enable_criu}" != "omit" ]; then
		cat >>"${TESTDIR}/config.toml" <<EOF
  enable_criu = ${enable_criu}
EOF
	fi
	if [ "${enable_experimental_restore}" != "omit" ]; then
		cat >>"${TESTDIR}/config.toml" <<EOF
  enable_experimental_restore_via_create = ${enable_experimental_restore}
EOF
	fi
	cat >>"${TESTDIR}/config.toml" <<EOF
[plugins."io.containerd.cri.v1.runtime".containerd]
  default_runtime_name = "test-runtime"
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.test-runtime]
runtime_type = "${TEST_RUNTIME:-io.containerd.runc.v2}"
EOF
	mkdir -p "${TESTDIR}"/{root,state}
	echo "=== STARTING CONTAINERD (enable_criu=${enable_criu}, enable_experimental_restore_via_create=${enable_experimental_restore}) ===" >> "${TESTDIR}/containerd.log"
	if [ -n "${use_path}" ]; then
		env PATH="${use_path}" ../../bin/containerd \
			--address "${TESTDIR}/c.sock" \
			--config "${TESTDIR}/config.toml" \
			--root "${TESTDIR}/root" \
			--state "${TESTDIR}/state" \
			--log-level trace >>"${TESTDIR}/containerd.log" 2>&1 &
	else
		../../bin/containerd \
			--address "${TESTDIR}/c.sock" \
			--config "${TESTDIR}/config.toml" \
			--root "${TESTDIR}/root" \
			--state "${TESTDIR}/state" \
			--log-level trace >>"${TESTDIR}/containerd.log" 2>&1 &
	fi
	echo $! > "${TESTDIR}/containerd.pid"
	retry_counter=0
	max_retries=10
	while true; do
		((retry_counter += 1))
		if crictl info >/dev/null 2>&1; then
			break
		fi
		sleep 1
		if [ "${retry_counter}" -gt "${max_retries}" ]; then
			echo "--> Failed to start containerd"
			exit 1
		fi
	done
}
function stop_containerd() {
	echo "--> Stopping containerd..."
	if [ -f "${TESTDIR}/containerd.pid" ]; then
		local pid
		pid=$(cat "${TESTDIR}/containerd.pid")
		echo "--> Killing containerd PID ${pid}..."
		kill -15 "${pid}" || true
		sleep 2
		if kill -0 "${pid}" 2>/dev/null; then
			echo "--> containerd PID ${pid} still running, force killing..."
			kill -9 "${pid}" || true
		fi
		rm -f "${TESTDIR}/containerd.pid"
	else
		echo "--> No containerd.pid found, using pkill..."
		pkill -x containerd || true
		sleep 2
	fi
}
function setup_container() {
	crictl pull "${TEST_IMAGE}" >/dev/null
	POD_JSON=$(mktemp)
	jq ".log_directory=\"${TESTDIR}\"" "$TESTDATA"/sandbox_config.json >"$POD_JSON"
	pod_id=$(crictl runp "$POD_JSON")
	ctr_id=$(crictl create "$pod_id" "$TESTDATA"/container_sleep.json "$POD_JSON")
	crictl start "$ctr_id" >/dev/null
	rm -f "$POD_JSON"
	echo "${pod_id}:${ctr_id}"
}
function cleanup_container() {
	local pod_id="$1"
	local ctr_id="$2"
	crictl rm -f "${ctr_id}" >/dev/null 2>&1 || true
	crictl rmp -f "${pod_id}" >/dev/null 2>&1 || true
	crictl rmi "${TEST_IMAGE}" >/dev/null 2>&1 || true
}

# --- Test Executions ---
rm -f "${TESTDIR}/containerd.log"

# ==============================================================================
# Group 1 (Configuration: defaults - enable_criu omitted, enable_experimental_restore_via_create omitted)
# ==============================================================================
start_containerd "omit" "omit" ""

# Test 1: Checkpoint succeeds by default (enable_criu defaults to true).
ids=$(setup_container)
pod_id=$(echo "$ids" | cut -d: -f1)
ctr_id=$(echo "$ids" | cut -d: -f2)
rm -f "$TESTDIR"/default_checkpoint.tar
crictl checkpoint --export="$TESTDIR"/default_checkpoint.tar "${ctr_id}"
echo "PASS: Test 1: Checkpoint succeeds with default configuration"
cleanup_container "$pod_id" "$ctr_id"

# Test 2: Restore fails fast by default (enable_experimental_restore_via_create defaults to false).
POD_JSON=$(mktemp)
jq ".log_directory=\"${TESTDIR}\"" "$TESTDATA"/sandbox_config.json >"$POD_JSON"
pod_id=$(crictl runp "$POD_JSON")
touch "$TESTDIR/dummy-checkpoint.tar"
RESTORE_JSON=$(mktemp)
jq ".image.image=\"$TESTDIR/dummy-checkpoint.tar\"" "$TESTDATA"/container_sleep.json >"$RESTORE_JSON"
set +e
output=$(crictl create "$pod_id" "$RESTORE_JSON" "$POD_JSON" 2>&1)
exit_code=$?
set -e
rm -f "$RESTORE_JSON" "$POD_JSON"
if [ $exit_code -eq 0 ] || [[ ! "$output" =~ "checkpoint restore via CreateContainer is disabled by configuration" ]]; then
	echo "ERROR: Test 2 failed (restore did not fail fast with expected message). Output: $output"
	exit 1
fi
echo "PASS: Test 2: Restore fails fast when enable_experimental_restore_via_create defaults to false"
crictl rmp -f "$pod_id" >/dev/null 2>&1 || true

stop_containerd

# ==============================================================================
# Group 2 (Configuration: enable_criu = false)
# ==============================================================================
start_containerd "false" "omit" ""

# Test 3: Checkpoint fails fast when enable_criu = false, and the source container remains running.
ids=$(setup_container)
pod_id=$(echo "$ids" | cut -d: -f1)
ctr_id=$(echo "$ids" | cut -d: -f2)
set +e
output=$(crictl checkpoint --export="$TESTDIR"/cp.tar "${ctr_id}" 2>&1)
exit_code=$?
set -e
if [ $exit_code -eq 0 ] || [[ ! "$output" =~ "criu support is disabled by configuration" ]]; then
	echo "ERROR: Test 3 failed (checkpoint did not fail fast). Output: $output"
	exit 1
fi
state=$(crictl inspect "${ctr_id}" | jq -r '.status.state')
if [ "$state" != "CONTAINER_RUNNING" ]; then
	echo "ERROR: Test 3 failed (source container state is not RUNNING). State: $state"
	exit 1
fi
echo "PASS: Test 3: Checkpoint fails fast and source container remains running when enable_criu = false"
cleanup_container "$pod_id" "$ctr_id"

# Test 4: Restore fails fast when enable_criu = false.
POD_JSON=$(mktemp)
jq ".log_directory=\"${TESTDIR}\"" "$TESTDATA"/sandbox_config.json >"$POD_JSON"
pod_id=$(crictl runp "$POD_JSON")
touch "$TESTDIR/dummy-checkpoint.tar"
RESTORE_JSON=$(mktemp)
jq ".image.image=\"$TESTDIR/dummy-checkpoint.tar\"" "$TESTDATA"/container_sleep.json >"$RESTORE_JSON"
set +e
output=$(crictl create "$pod_id" "$RESTORE_JSON" "$POD_JSON" 2>&1)
exit_code=$?
set -e
rm -f "$RESTORE_JSON" "$POD_JSON"
if [ $exit_code -eq 0 ] || [[ ! "$output" =~ "disabled by configuration" ]]; then
	echo "ERROR: Test 4 failed (restore did not fail fast). Output: $output"
	exit 1
fi
echo "PASS: Test 4: Restore fails fast when enable_criu = false"
crictl rmp -f "$pod_id" >/dev/null 2>&1 || true

stop_containerd

# ==============================================================================
# Group 3 (Configuration: enable_criu = true, enable_experimental_restore_via_create = true, cleaned PATH without CRIU)
# ==============================================================================
if [ -n "${CRIU_DIR}" ]; then
	CLEANED_PATH=$(get_cleaned_path)

	start_containerd "true" "true" "${CLEANED_PATH}"

	# Test 5: CRIU missing from PATH, enable_criu = true.
	# Verifies that when CRIU is enabled but missing, it fails with the binary missing error.
	ids=$(setup_container)
	pod_id=$(echo "$ids" | cut -d: -f1)
	ctr_id=$(echo "$ids" | cut -d: -f2)
	set +e
	output=$(crictl checkpoint --export="$TESTDIR"/cp.tar "${ctr_id}" 2>&1)
	exit_code=$?
	set -e
	if [ $exit_code -eq 0 ] || [[ ! "$output" =~ "criu binary not found" ]]; then
		echo "ERROR: Test 5 failed. Output: $output"
		exit 1
	fi
	echo "PASS: Test 5: Fails with binary not found as expected when enable_criu = true and CRIU is missing"
	cleanup_container "$pod_id" "$ctr_id"

	stop_containerd
fi

SUCCESS=1
