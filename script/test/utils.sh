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

# USE_HYPERV configures containerd to spawn Hyper-V isolated containers
# when running on Windows.
USE_HYPERV=${USE_HYPERV:-0}

IS_WINDOWS=0
if [ -v "OS" ] && [ "${OS}" == "Windows_NT" ]; then
  IS_WINDOWS=1
fi

# RESTART_WAIT_PERIOD is the period to wait before restarting containerd.
RESTART_WAIT_PERIOD=${RESTART_WAIT_PERIOD:-10}

if [ $IS_WINDOWS -eq 0 ]; then
  CONTAINERD_CONFIG_DIR=${CONTAINERD_CONFIG_DIR:-"/tmp"}
else
  CONTAINERD_CONFIG_DIR=${CONTAINERD_CONFIG_DIR:-"c:/Windows/Temp"}
fi

# Use a configuration file for containerd.
CONTAINERD_CONFIG_FILE=${CONTAINERD_CONFIG_FILE:-""}
# The runtime to use (ignored when CONTAINERD_CONFIG_FILE is set)
CONTAINERD_RUNTIME=${CONTAINERD_RUNTIME:-""}
if [ -z "${CONTAINERD_CONFIG_FILE}" ]; then
  config_file="${CONTAINERD_CONFIG_DIR}/containerd-config-cri.toml"
  truncate --size 0 "${config_file}"
  # TODO(fuweid): if the config.Imports supports patch update, it will be easy
  # to write the integration test case with different configuration, like:
  #
  # 1. write configuration into importable containerd config path.
  # 2. restart containerd
  # 3. verify the behaviour
  # 4. delete the configuration
  # 5. restart containerd
  cat >>${config_file} <<EOF
version=2

[plugins."io.containerd.grpc.v1.cri"]
  drain_exec_sync_io_timeout = "10s"

# Userns requires idmap mount support for overlayfs (added in 5.19)
# Let's opt-in for a recursive chown, so we can always test this even in old distros.
# Note that if idmap mounts support is present, we will use that, so it is harmless to keep this
# here.
[plugins."io.containerd.snapshotter.v1.overlayfs"]
    slow_chown = true
EOF

  if command -v sestatus >/dev/null 2>&1; then
    cat >>${config_file} <<EOF
  enable_selinux = true
EOF
  fi
  if [ -n "${CONTAINERD_RUNTIME}" ]; then
    cat >>${config_file} <<EOF
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
runtime_type = "${CONTAINERD_RUNTIME}"
EOF
  fi
  if [ $IS_WINDOWS -eq 0 ]; then
    NRI_CONFIG_DIR="${CONTAINERD_CONFIG_DIR}/nri"
    cat >>${config_file} <<EOF
[plugins."io.containerd.nri.v1.nri"]
  disable = false
  config_file = "${NRI_CONFIG_DIR}/nri.conf"
  socket_path = "/var/run/nri-test.sock"
  plugin_path = "/no/pre-launched/nri/plugins"
EOF
    mkdir -p "${NRI_CONFIG_DIR}"
    cat >"${NRI_CONFIG_DIR}/nri.conf" <<EOF
disableConnections: false
EOF
  fi
  CONTAINERD_CONFIG_FILE="${config_file}"
fi

if [ $IS_WINDOWS -eq 0 ]; then
  FAILPOINT_CONTAINERD_RUNTIME="runc-fp.v1"
  FAILPOINT_CNI_CONF_DIR=${FAILPOINT_CNI_CONF_DIR:-"/tmp/failpoint-cni-net.d"}
  mkdir -p "${FAILPOINT_CNI_CONF_DIR}"

  # Add runtime with failpoint
  cat << EOF | tee -a "${CONTAINERD_CONFIG_FILE}"
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc-fp]
  cni_conf_dir = "${FAILPOINT_CNI_CONF_DIR}"
  cni_max_conf_num = 1
  pod_annotations = ["io.containerd.runtime.v2.shim.failpoint.*"]
  runtime_type = "${FAILPOINT_CONTAINERD_RUNTIME}"
EOF

  cat << EOF | tee "${FAILPOINT_CNI_CONF_DIR}/10-containerd-net.conflist"
{
  "cniVersion": "1.0.0",
  "name": "containerd-net-failpoint",
  "plugins": [
    {
      "type": "cni-bridge-fp",
      "bridge": "cni-fp",
      "isGateway": true,
      "ipMasq": true,
      "promiscMode": true,
      "ipam": {
        "type": "host-local",
        "ranges": [
          [{
            "subnet": "10.88.0.0/16"
          }],
          [{
            "subnet": "2001:4860:4860::/64"
          }]
        ],
        "routes": [
          { "dst": "0.0.0.0/0" },
          { "dst": "::/0" }
        ]
      },
      "capabilities": {
        "io.kubernetes.cri.pod-annotations": true
      }
    },
    {
      "type": "portmap",
      "capabilities": {"portMappings": true}
    }
  ]
}
EOF
fi

if [ ${IS_WINDOWS} -eq 1 -a ${USE_HYPERV} -eq 1 ];then
  cat >> ${CONTAINERD_CONFIG_FILE} << EOF
version = 2
[plugins]
    [plugins."io.containerd.grpc.v1.cri".containerd]
      default_runtime_name = "runhcs-wcow-hyperv"
      [plugins."io.containerd.grpc.v1.cri".containerd.runtimes]

       [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runhcs-wcow-hyperv]
        runtime_type = "io.containerd.runhcs.v1"
        [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runhcs-wcow-hyperv.options]
          Debug = true
          DebugType = 2
          SandboxPlatform = "windows/amd64"
          SandboxIsolation = 1
EOF
fi

# CONTAINERD_TEST_SUFFIX is the suffix appended to the root/state directory used
# by test containerd.
CONTAINERD_TEST_SUFFIX=${CONTAINERD_TEST_SUFFIX:-"-test"}
if [ $IS_WINDOWS -eq 0 ]; then
  # The containerd root directory.
  CONTAINERD_ROOT=${CONTAINERD_ROOT:-"/var/lib/containerd${CONTAINERD_TEST_SUFFIX}"}
  # The containerd state directory.
  CONTAINERD_STATE=${CONTAINERD_STATE:-"/run/containerd${CONTAINERD_TEST_SUFFIX}"}
  # The containerd socket address.
  CONTAINERD_SOCK=${CONTAINERD_SOCK:-unix://${CONTAINERD_STATE}/containerd.sock}
  TRIMMED_CONTAINERD_SOCK="${CONTAINERD_SOCK#unix://}"
else
  # $ProgramData holds the Windows path to the ProgramData folder in standard Windows
  # format. The backslash in the path may be interpreted by bash, so we convert the
  # Windows path to POSIX path using cygpath.exe. The end result should be something
  # similar to /c/ProgramData/.
  POSIX_PROGRAM_DATA="$(cygpath.exe $ProgramData)"

  CONTAINERD_ROOT=${CONTAINERD_ROOT:-"$POSIX_PROGRAM_DATA/containerd/root${CONTAINERD_TEST_SUFFIX}"}
  CONTAINERD_STATE=${CONTAINERD_STATE:-"$POSIX_PROGRAM_DATA/containerd/state${CONTAINERD_TEST_SUFFIX}"}

  # Remove drive letter
  PIPE_STATE="${CONTAINERD_STATE#*:/}"
  # Remove leading slash
  PIPE_STATE="${PIPE_STATE#/}"
  # Replace empty space with dash
  PIPE_STATE="${PIPE_STATE// /"-"}"
  CONTAINERD_SOCK=${CONTAINERD_SOCK:-npipe://./pipe/${PIPE_STATE}/containerd}
  TRIMMED_CONTAINERD_SOCK="${CONTAINERD_SOCK#npipe:}"
fi

# The containerd binary name.
EXE_SUFFIX=""
if [ $IS_WINDOWS -eq 1 ]; then
  EXE_SUFFIX=".exe"
fi
CONTAINERD_BIN=${CONTAINERD_BIN:-"containerd"}${EXE_SUFFIX}

pid=

# NOTE: We don't have the sudo command on Windows.
sudo=""
if [ "$(id -u)" -ne 0 ] && command -v sudo &> /dev/null; then
  sudo="sudo PATH=${PATH}"
fi


# The run_containerd function is a wrapper that will run the appropriate
# containerd command based on the OS we're running the tests on. This wrapper
# is needed if we plan to run the containerd command as part of a retry cycle
# as is the case on Linux, where we use the keepalive function. Using a wrapper
# allows us to avoid the need for eval, while allowing us to quote the paths
# to the state and root folders. This allows us to use paths that have spaces
# in them without erring out.
run_containerd() {
  # not used on linux
  if [ $# -gt 0 ]; then
    local report_dir=$1
  fi
  CMD=""
  if [ -n "${sudo}" ]; then
    CMD+="${sudo} "
  fi
  CMD+="${PWD}/bin/containerd"

  if [ $IS_WINDOWS -eq 0 ]; then
    $CMD --log-level=debug \
      --config "${CONTAINERD_CONFIG_FILE}" \
      --address "${TRIMMED_CONTAINERD_SOCK}" \
      --state "${CONTAINERD_STATE}" \
      --root "${CONTAINERD_ROOT}"
  else
    # Note(gsamfira): On Windows, we register a containerd-test service which will run under
    # LocalSystem. This user is part of the local Administrators group and should have all
    # required permissions to successfully start containers.
    # The --register-service parameter will do this for us.
    $CMD --log-level=debug \
      --config "${CONTAINERD_CONFIG_FILE}" \
      --address "${TRIMMED_CONTAINERD_SOCK}" \
      --state "${CONTAINERD_STATE}" \
      --root "${CONTAINERD_ROOT}" \
      --log-file "${report_dir}/containerd.log" \
      --service-name containerd-test \
      --register-service
  fi
}

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
    keepalive run_containerd \
      "${RESTART_WAIT_PERIOD}" &> "${report_dir}/containerd.log" &
    pid=$!
  else
    if [ ! -d "${CONTAINERD_ROOT}" ]; then
      # Create the containerd ROOT dir and set full access to be inherited for "CREATOR OWNER"
      # on all subfolders and files.
      mkdir -p "${CONTAINERD_ROOT}"
      cmd.exe /c 'icacls.exe "'$(cygpath -w "${CONTAINERD_ROOT}")'" /grant "CREATOR OWNER":(OI)(CI)(IO)F /T'
    fi

    run_containerd "$report_dir"

    # Set failure flag on the test service. This will restart the service
    # in case of failure.
    sc.exe failure containerd-test reset=0 actions=restart/1000
    sc.exe failureflag containerd-test 1

    # it might still result in SERVICE_START_PENDING, but we can ignore it.
    sc.exe start containerd-test || true
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
  readiness_check run_ctr
  readiness_check run_crictl
  # Show the config about cri plugin in log when it's ready
  run_crictl
}

# test_teardown kills containerd.
test_teardown() {
  if [ -n "${pid}" ]; then
    if [ $IS_WINDOWS -eq 1 ]; then
      # Mark service for deletion. It will be deleted as soon as the service stops.
      sc.exe delete containerd-test
      # Stop the service
      sc.exe stop containerd-test || true
    else
      pgid=$(ps -o pgid= -p "${pid}" || true)
      if [ ! -z "${pgid}" ]; then
        ${sudo} pkill -g ${pgid}
      else
        echo "pid(${pid}) not found, skipping pkill"
      fi
    fi
  fi
}

run_ctr() {
  ${sudo} ${PWD}/bin/ctr --address "${TRIMMED_CONTAINERD_SOCK}" version
}

run_crictl() {
  ${sudo} ${crictl_path} --runtime-endpoint="${CONTAINERD_SOCK}" info
}

# keepalive runs a command and keeps it alive.
# keepalive process is eventually killed in test_teardown.
keepalive() {
  # The command may return non-zero and we want to continue this script.
  # e.g. containerd receives SIGKILL
  set +e
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
  until ${command} &>/dev/null || (( attempt_num == MAX_ATTEMPTS ))
  do
      echo "$attempt_num attempt \"$command\"! Trying again in $attempt_num seconds..."
      sleep $(( attempt_num++ ))
  done
  set -x
  cat "${report_dir}/containerd.log"
  cat "${config_file}"
  set +x
}
