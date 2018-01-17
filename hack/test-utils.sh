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

source ${ROOT}/hack/versions
# CRI_CONTAINERD_FLAGS are the extra flags to use when start cri-containerd.
CRI_CONTAINERD_FLAGS=${CRI_CONTAINERD_FLAGS:-""}
# RESTART_WAIT_PERIOD is the period to wait before restarting cri-containerd/containerd.
RESTART_WAIT_PERIOD=${RESTART_WAIT_PERIOD:-10}
# STANDALONE_CRI_CONTAINERD indicates whether to run standalone cri-containerd.
STANDALONE_CRI_CONTAINERD=${STANDALONE_CRI_CONTAINERD:-true}

CRICONTAINERD_SOCK=/var/run/cri-containerd.sock

cri_containerd_pid=
containerd_pid=

# test_setup starts containerd and cri-containerd.
test_setup() {
  local report_dir=$1 
  if [ ! -x ${ROOT}/_output/cri-containerd ]; then
    echo "cri-containerd is not built"
    exit 1
  fi

  # Start containerd
  if [ ! -x "$(command -v containerd)" ]; then
    echo "containerd is not installed, please run hack/install-deps.sh"
    exit 1
  fi
  sudo pkill containerd
  keepalive "sudo containerd" ${RESTART_WAIT_PERIOD} &> ${report_dir}/containerd.log &
  containerd_pid=$!
  # Wait for containerd to be running by using the containerd client ctr to check the version
  # of the containerd server. Wait an increasing amount of time after each of five attempts
  readiness_check "sudo ctr version"

  # Start cri-containerd
  if ${STANDALONE_CRI_CONTAINERD}; then
    keepalive "sudo ${ROOT}/_output/cri-containerd --log-level=debug ${CRI_CONTAINERD_FLAGS}" \
      ${RESTART_WAIT_PERIOD} &> ${report_dir}/cri-containerd.log &
    cri_containerd_pid=$!
  fi
  readiness_check "sudo ${GOPATH}/bin/crictl --runtime-endpoint=${CRICONTAINERD_SOCK} info"
}

# test_teardown kills containerd and cri-containerd.
test_teardown() {
  if [ -n "${containerd_pid}" ]; then
    kill ${containerd_pid}
  fi
  if [ -n "${cri_containerd_pid}" ]; then
    kill ${cri_containerd_pid}
  fi
  sudo pkill containerd
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
