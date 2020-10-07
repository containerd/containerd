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

set -o nounset
set -o pipefail

source $(dirname "${BASH_SOURCE[0]}")/test-utils.sh

DEFAULT_SKIP="\[Flaky\]|\[Slow\]|\[Serial\]"
DEFAULT_SKIP+="|querying\s\/stats\/summary"

# FOCUS focuses the test to run.
export FOCUS=${FOCUS:-""}
# SKIP skips the test to skip.
export SKIP=${SKIP:-${DEFAULT_SKIP}}
# REPORT_DIR is the the directory to store test logs.
REPORT_DIR=${REPORT_DIR:-"/tmp/test-e2e-node"}
# UPLOAD_LOG indicates whether to upload test log to gcs.
UPLOAD_LOG=${UPLOAD_LOG:-false}
# TIMEOUT is the timeout of the test.
TIMEOUT=${TIMEOUT:-"40m"}
# FAIL_SWAP_ON makes kubelet fail when swap is on.
# Many dev environments run with swap on, so we don't fail by default.
FAIL_SWAP_ON=${FAIL_SWAP_ON:-"false"}

# Check GOPATH
if [[ -z "${GOPATH}" ]]; then
  echo "GOPATH is not set"
  exit 1
fi

ORIGINAL_RULES=`mktemp`
sudo iptables-save > ${ORIGINAL_RULES}

# Update ip firewall
# We need to add rules to accept all TCP/UDP/ICMP packets.
if sudo iptables -L INPUT | grep "Chain INPUT (policy DROP)" > /dev/null; then
  sudo iptables -A INPUT -w -p TCP -j ACCEPT
  sudo iptables -A INPUT -w -p UDP -j ACCEPT
  sudo iptables -A INPUT -w -p ICMP -j ACCEPT
fi
if sudo iptables -L FORWARD | grep "Chain FORWARD (policy DROP)" > /dev/null; then
  sudo iptables -A FORWARD -w -p TCP -j ACCEPT
  sudo iptables -A FORWARD -w -p UDP -j ACCEPT
  sudo iptables -A FORWARD -w -p ICMP -j ACCEPT
fi

# For multiple GOPATHs, keep the first one only
GOPATH=${GOPATH%%:*}

# Get kubernetes
KUBERNETES_REPO="https://github.com/kubernetes/kubernetes"
KUBERNETES_PATH="${GOPATH}/src/k8s.io/kubernetes"
if [ ! -d "${KUBERNETES_PATH}" ]; then
  mkdir -p ${KUBERNETES_PATH}
  cd ${KUBERNETES_PATH}
  git clone https://${KUBERNETES_REPO} .
fi
cd ${KUBERNETES_PATH}
git fetch --all
git checkout ${KUBERNETES_VERSION}

mkdir -p ${REPORT_DIR}
test_setup ${REPORT_DIR}

timeout "${TIMEOUT}" make test-e2e-node \
  RUNTIME=remote \
  CONTAINER_RUNTIME_ENDPOINT=unix://${CONTAINERD_SOCK} \
  ARTIFACTS=${REPORT_DIR} \
  TEST_ARGS='--kubelet-flags=--cgroups-per-qos=true \
  --kubelet-flags=--cgroup-root=/ \
  --kubelet-flags=--fail-swap-on='${FAIL_SWAP_ON}' \
  --prepull-images=false'
test_exit_code=$?

test_teardown

sudo iptables-restore < ${ORIGINAL_RULES}
rm ${ORIGINAL_RULES}

# UPLOAD_LOG_PATH is bucket to upload test logs.
UPLOAD_LOG_PATH=cri-containerd_test-e2e-node
if ${UPLOAD_LOG}; then
  if [ -z "${VERSION}" ]; then
    echo "VERSION is not set"
    exit 1
  fi
  upload_logs_to_gcs "${UPLOAD_LOG_PATH}" "${VERSION}-$(date +%Y%m%d-%H%M%S)" "${REPORT_DIR}"
fi

exit ${test_exit_code}
