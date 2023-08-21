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

set -o errexit
set -o nounset
set -o pipefail

basedir="$(dirname "${BASH_SOURCE[0]}")"
source "${basedir}/utils.sh"

trap test_teardown EXIT

ROOT="$( cd "${basedir}" && pwd )"/../..
cd "${ROOT}"

# FOCUS focuses the test to run.
FOCUS=${FOCUS:-""}
# REPORT_DIR is the directory to store test logs.
if [ $IS_WINDOWS -eq 0 ]; then
  REPORT_DIR=${REPORT_DIR:-"/tmp/test-integration"}
else
  REPORT_DIR=${REPORT_DIR:-"C:/Windows/Temp/test-integration"}
fi
# RUNTIME is the runtime handler to use in the test.
RUNTIME=${RUNTIME:-""}

mkdir -p "${REPORT_DIR}"
test_setup "${REPORT_DIR}"

# Run integration test.
CMD=""
if [ -n "${sudo}" ]; then
  CMD+="${sudo} "
  # sudo strips environment variables, so add DISABLE_CRI_SANDBOXES back if present
  if [ -n  "${DISABLE_CRI_SANDBOXES}" ]; then
    CMD+="DISABLE_CRI_SANDBOXES='${DISABLE_CRI_SANDBOXES}' "
  fi
fi
CMD+="${PWD}/bin/cri-integration.test"

${CMD} --test.run="${FOCUS}" --test.v \
  --cri-endpoint="${CONTAINERD_SOCK}" \
  --runtime-handler="${RUNTIME}" \
  --containerd-bin="${CONTAINERD_BIN}" \
  --image-list="${TEST_IMAGE_LIST:-}" && test_exit_code=$? || test_exit_code=$?

if [[ "$test_exit_code" -ne 0 ]]; then
  if [[ -e "$GITHUB_WORKSPACE" ]]; then
    mkdir -p "$GITHUB_WORKSPACE/report"
    mv "$REPORT_DIR/containerd.log" "$GITHUB_WORKSPACE/report"

    echo ::group::containerd logs
    cat "$GITHUB_WORKSPACE/report/containerd.log"
    echo ::endgroup::
  else
    cat "$REPORT_DIR/containerd.log"
  fi
fi

exit ${test_exit_code}
