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

# FOCUS focuses the test to run.
FOCUS=${FOCUS:-}
# SKIP skips the test to skip.
SKIP=${SKIP:-""}
# REPORT_DIR is the the directory to store test logs.
REPORT_DIR=${REPORT_DIR:-"/tmp/test-cri"}
# RUNTIME is the runtime handler to use in the test.
RUNTIME=${RUNTIME:-""}

# Check GOPATH
if [[ -z "${GOPATH}" ]]; then
  echo "GOPATH is not set"
  exit 1
fi

# For multiple GOPATHs, keep the first one only
GOPATH=${GOPATH%%:*}

CRITEST=${GOPATH}/bin/critest

GINKGO_PKG=github.com/onsi/ginkgo/ginkgo

# Install ginkgo
if [ ! -x "$(command -v ginkgo)" ]; then
  go get -u ${GINKGO_PKG}
fi

# Install critest
if [ ! -x "$(command -v ${CRITEST})" ]; then
  go get -d ${CRITOOL_PKG}/...
  cd ${GOPATH}/src/${CRITOOL_PKG}
  git fetch --all
  git checkout ${CRITOOL_VERSION}
  make critest
  make install-critest -e BINDIR="${GOPATH}/bin"
fi
which ${CRITEST}

mkdir -p ${REPORT_DIR}
test_setup ${REPORT_DIR}

# Run cri validation test
sudo env PATH=${PATH} GOPATH=${GOPATH} ${CRITEST} --runtime-endpoint=${CONTAINERD_SOCK} --ginkgo.focus="${FOCUS}" --ginkgo.skip="${SKIP}" --parallel=8 --runtime-handler=${RUNTIME}
test_exit_code=$?

test_teardown

exit ${test_exit_code}
