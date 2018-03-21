#!/bin/bash

# Copyright 2017 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

source $(dirname "${BASH_SOURCE[0]}")/utils.sh

# RESTART_WAIT_PERIOD is the period to wait before restarting containerd.
RESTART_WAIT_PERIOD=${RESTART_WAIT_PERIOD:-10}
CONTAINERD_CONFIG="--log-level=debug "

# Use a configuration file for containerd.
CONTAINERD_CONFIG_FILE=${CONTAINERD_CONFIG_FILE:-""}
if [ -f "${CONTAINERD_CONFIG_FILE}" ]; then
	CONTAINERD_CONFIG+="--config $CONTAINERD_CONFIG_FILE"
fi

CONTAINERD_SOCK=/run/containerd/containerd.sock

containerd_pid=

# test_setup starts containerd.
test_setup() {
  local report_dir=$1
  # Start containerd
  if [ ! -x ${ROOT}/_output/containerd ]; then
    echo "containerd is not built"
    exit 1
  fi
  sudo pkill -x containerd
  keepalive "sudo ${ROOT}/_output/containerd ${CONTAINERD_CONFIG}" \
    ${RESTART_WAIT_PERIOD} &> ${report_dir}/containerd.log &
  containerd_pid=$!
  # Wait for containerd to be running by using the containerd client ctr to check the version
  # of the containerd server. Wait an increasing amount of time after each of five attempts
  readiness_check "sudo ctr version"
  readiness_check "sudo ${GOPATH}/bin/crictl --runtime-endpoint=${CONTAINERD_SOCK} info"
}

# test_teardown kills containerd.
test_teardown() {
  if [ -n "${containerd_pid}" ]; then
    kill ${containerd_pid}
  fi
  sudo pkill -x containerd
}

# keepalive runs a command and keeps it alive.
# keepalive process is eventually killed in test_teardown.
keepalive() {
  local command=$1
  echo ${command}
  local wait_period=$2
  while true; do
    ${command}
    sleep ${wait_period}
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
