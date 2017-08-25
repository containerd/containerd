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
set -o nounset
set -o pipefail

source $(dirname "${BASH_SOURCE[0]}")/test-utils.sh

DEFAULT_SKIP="\[Flaky\]|\[Slow\]|\[Serial\]"
DEFAULT_SKIP+="|scheduling\sa\sGuaranteed\sPod"
DEFAULT_SKIP+="|scheduling\sa\sBurstable\sPod"
DEFAULT_SKIP+="|scheduling\sa\sBestEffort\sPod"
DEFAULT_SKIP+="|querying\s\/stats\/summary"
DEFAULT_SKIP+="|set\sto\sthe\smanifest\sdigest"
DEFAULT_SKIP+="|AppArmor"
DEFAULT_SKIP+="|Top\slevel\sQoS\scontainers"
DEFAULT_SKIP+="|pull\sfrom\sprivate\sregistry\swith\ssecret"

# FOCUS focuses the test to run.
export FOCUS=${FOCUS:-""}
# SKIP skips the test to skip.
export SKIP=${SKIP:-${DEFAULT_SKIP}}
REPORT_DIR=${REPORT_DIR:-"/tmp/test-e2e-node"}

if [[ -z "${GOPATH}" ]]; then
  echo "GOPATH is not set"
  exit 1
fi

# Get kubernetes
KUBERNETES_REPO="https://github.com/kubernetes/kubernetes"
KUBERNETES_PATH="${GOPATH}/src/k8s.io/kubernetes"
if [ ! -d "${KUBERNETES_PATH}" ]; then
  mkdir -p ${KUBERNETES_PATH}
  cd ${KUBERNETES_PATH}
  git clone ${KUBERNETES_REPO} .
fi
cd ${KUBERNETES_PATH}
git fetch --all
git checkout ${KUBERNETES_VERSION}

mkdir -p ${REPORT_DIR}
start_cri_containerd ${REPORT_DIR}

make test-e2e-node RUNTIME=remote CONTAINER_RUNTIME_ENDPOINT=unix:///var/run/cri-containerd.sock ARTIFACTS=${REPORT_DIR}

kill_cri_containerd
