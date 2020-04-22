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

export PATH="/c/Program Files/Containerd:$PATH"
FOCUS="${FOCUS:-"Conformance"}"
SKIP="${SKIP:-""}"
REPORT_DIR="${REPORT_DIR:-"/c/_artifacts"}"

make install.deps
make install -e BINDIR="/c/Program Files/Containerd"

mkdir -p "${REPORT_DIR}"
containerd -log-level debug &> "${REPORT_DIR}/containerd.log" &
pid=$!
ctr version

set +o errexit
critest --runtime-endpoint=npipe:////./pipe/containerd-containerd --ginkgo.focus="${FOCUS}" --ginkgo.skip="${SKIP}" --report-dir="${REPORT_DIR}" --report-prefix="windows"
TEST_RC=$?
set -o errexit
kill -9 $pid
echo -n "${TEST_RC}" > "${REPORT_DIR}/exitcode"
