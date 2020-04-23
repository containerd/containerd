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

# This script is used to build and upload containerd with latest CRI plugin
# from containerd/cri in gcr.io/k8s-testimages/kubekins-e2e.

set -o xtrace
set -o errexit
set -o nounset
set -o pipefail

source $(dirname "${BASH_SOURCE[0]}")/../build-utils.sh
cd "${ROOT}"

# Make sure output directory is clean.
GOOS=windows make clean
# Build and push test tarball.
PUSH_VERSION=true DEPLOY_DIR=${DEPLOY_DIR:-"windows"} GOOS=windows \
  make push TARBALL_PREFIX=cri-containerd-cni INCLUDE_CNI=true CUSTOM_CONTAINERD=true
