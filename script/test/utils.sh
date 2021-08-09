#!/bin/bash

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

IS_WINDOWS=0
if [ -v "OS" ] && [ "${OS}" == "Windows_NT" ]; then
  IS_WINDOWS=1
fi

# RESTART_WAIT_PERIOD is the period to wait before restarting containerd.
RESTART_WAIT_PERIOD=${RESTART_WAIT_PERIOD:-10}
# CONTAINERD_FLAGS contains all containerd flags.
CONTAINERD_FLAGS="--log-level=debug "

# Use a configuration file for containerd.
CONTAINERD_CONFIG_FILE=${CONTAINERD_CONFIG_FILE:-""}
# The runtime to use (ignored when CONTAINERD_CONFIG_FILE is set)
CONTAINERD_RUNTIME=${CONTAINERD_RUNTIME:-""}
if [ -z "${CONTAINERD_CONFIG_FILE}" ]; then
  config_file="/tmp/containerd-config-cri.toml"
  truncate --size 0 "${config_file}"
  if command -v sestatus >/dev/null 2>&1; then
    cat >>${config_file} <<EOF
version=2
[plugins."io.containerd.grpc.v1.cri"]
  enable_selinux = true
EOF
  fi
  if [ -n "${CONTAINERD_RUNTIME}" ]; then
    cat >>${config_file} <<EOF
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
runtime_type = "${CONTAINERD_RUNTIME}"
EOF
  fi
  CONTAINERD_CONFIG_FILE="${config_file}"
fi

# CONTAINERD_TEST_SUFFIX is the suffix appended to the root/state directory used
# by test containerd.
CONTAINERD_TEST_SUFFIX=${CONTAINERD_TEST_SUFFIX:-"-test"}
# The containerd root directory.
CONTAINERD_ROOT=${CONTAINERD_ROOT:-"/var/lib/containerd${CONTAINERD_TEST_SUFFIX}"}
# The containerd state directory.
CONTAINERD_STATE=${CONTAINERD_STATE:-"/run/containerd${CONTAINERD_TEST_SUFFIX}"}
# The containerd socket address.
if [ $IS_WINDOWS -eq 0 ]; then
  CONTAINERD_SOCK=${CONTAINERD_SOCK:-unix://${CONTAINERD_STATE}/containerd.sock}
  TRIMMED_CONTAINERD_SOCK="${CONTAINERD_SOCK#unix://}"
else
  CONTAINERD_SOCK=${CONTAINERD_SOCK:-npipe://./pipe/${CONTAINERD_STATE}/containerd}
  TRIMMED_CONTAINERD_SOCK="${CONTAINERD_SOCK#npipe:}"
fi

# The containerd binary name.
EXE_SUFFIX=""
if [ $IS_WINDOWS -eq 1 ]; then
  EXE_SUFFIX=".exe"
fi
CONTAINERD_BIN=${CONTAINERD_BIN:-"containerd"}${EXE_SUFFIX}
if [ -f "${CONTAINERD_CONFIG_FILE}" ]; then
  CONTAINERD_FLAGS+="--config ${CONTAINERD_CONFIG_FILE} "
fi

CONTAINERD_FLAGS+="--address ${TRIMMED_CONTAINERD_SOCK} \
  --state ${CONTAINERD_STATE} \
  --root ${CONTAINERD_ROOT}"

pid=

# NOTE: We don't have the sudo command on Windows.
sudo=""
if [ "$(id -u)" -ne 0 ] && command -v sudo &> /dev/null; then
  sudo="sudo PATH=${PATH}"
fi

# test_setup starts containerd.
test_setup() {
  local report_dir=$1
  # Start containerd
  if [ ! -x "bin/containerd" ]; then
    echo "containerd is not built"
    exit 1
  fi
  set -m
  # Create containerd in a different process group
  # so that we can easily clean them up.
  if [ $IS_WINDOWS -eq 0 ]; then
    keepalive "${sudo} bin/containerd ${CONTAINERD_FLAGS}" \
      "${RESTART_WAIT_PERIOD}" &> "${report_dir}/containerd.log" &
    pid=$!
  else
    # NOTE(claudiub): For Windows HostProcess containers, containerd needs to be privileged enough to
    # start them. For this, we can register containerd as a service, so the LocalSystem will run it
    # for us. Additionally, we don't need to worry about keeping it alive, Windows will do it for us.
    nssm install containerd-test "$(pwd)/bin/containerd.exe" ${CONTAINERD_FLAGS} \
      --log-file "${report_dir}/containerd.log"

    # it might still result in SERVICE_START_PENDING, but we can ignore it.
    nssm start containerd-test || true
    pid="1"  # for teardown
  fi
  set +m

  # Wait for containerd to be running by using the containerd client ctr to check the version
  # of the containerd server. Wait an increasing amount of time after each of five attempts
  local -r crictl_path=$(which crictl)
  if [ -z "${crictl_path}" ]; then
    echo "crictl is not in PATH"
    exit 1
  fi
  readiness_check "${sudo} bin/ctr --address ${TRIMMED_CONTAINERD_SOCK} version"
  readiness_check "${sudo} ${crictl_path} --runtime-endpoint=${CONTAINERD_SOCK} info"
}

# test_teardown kills containerd.
test_teardown() {
  if [ -n "${pid}" ]; then
    if [ $IS_WINDOWS -eq 1 ]; then
      nssm stop containerd-test
      nssm remove containerd-test confirm
    else
      ${sudo} pkill -g $(ps -o pgid= -p "${pid}")
    fi
  fi
}

# keepalive runs a command and keeps it alive.
# keepalive process is eventually killed in test_teardown.
keepalive() {
  local command=$1
  echo "${command}"
  local wait_period=$2
  while true; do
    ${command}
    sleep "${wait_period}"
  done
}

# readiness_check checks readiness of a daemon with specified command.
readiness_check() {
  local command=$1
  local MAX_ATTEMPTS=5
  local attempt_num=1
  until ${command} &> /dev/null || (( attempt_num == MAX_ATTEMPTS ))
  do
      echo "$attempt_num attempt \"$command\"! Trying again in $attempt_num seconds..."
      sleep $(( attempt_num++ ))
  done
}
