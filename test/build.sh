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

source $(dirname "${BASH_SOURCE[0]}")/build-utils.sh
cd "${ROOT}"

# Make sure output directory is clean.
make clean

# Build CRI+CNI release
make BUILDTAGS="seccomp selinux no_aufs no_btrfs no_devmapper no_zfs" cri-cni-release

BUILDDIR=$(mktemp -d)
cleanup() {
  if [[ ${BUILDDIR} == /tmp/* ]]; then
    echo "[-] REMOVING ${BUILDDIR}"
    rm -rf ${BUILDDIR}
  fi
}
trap cleanup EXIT

set -x
latest=$(readlink ./releases/cri-cni-containerd.tar.gz)
cp releases/${latest} ${BUILDDIR}/cri-containerd.tar.gz
cp releases/${latest}.sha256sum ${BUILDDIR}/cri-containerd.tar.gz.sha256

# Push test tarball to Google cloud storage.
VERSION=$(git describe --match 'v[0-9]*' --dirty='.m' --always)
PUSH_VERSION=true VERSION=${VERSION} BUILD_DIR=${BUILDDIR} ${ROOT}/test/push.sh
