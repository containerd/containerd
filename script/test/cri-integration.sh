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

source $(dirname "${BASH_SOURCE[0]}")/utils.sh
cd ${ROOT}

# FOCUS focuses the test to run.
FOCUS=${FOCUS:-""}
# REPORT_DIR is the the directory to store test logs.
REPORT_DIR=${REPORT_DIR:-"/tmp/test-integration"}
# RUNTIME is the runtime handler to use in the test.
RUNTIME=${RUNTIME:-""}

CRI_ROOT="${CONTAINERD_ROOT}/io.containerd.grpc.v1.cri"

mkdir -p ${REPORT_DIR}
test_setup ${REPORT_DIR}

# Run integration test.
${sudo} bin/cri-integration.test --test.run="${FOCUS}" --test.v \
  --cri-endpoint=${CONTAINERD_SOCK} \
  --cri-root=${CRI_ROOT} \
  --runtime-handler=${RUNTIME} \
  --containerd-bin=${CONTAINERD_BIN} \
  --image-list="${TEST_IMAGE_LIST:-}"

test_exit_code=$?

test_teardown

exit ${test_exit_code}
