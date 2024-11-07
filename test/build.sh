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

basedir="$(dirname "${BASH_SOURCE[0]}")"
source "${basedir}/build-utils.sh"

ROOT="$( cd "${basedir}" && pwd )"/..
cd "${ROOT}"

# Make sure output directory is clean.
make clean

# Build CRI+CNI release
make BUILDTAGS="seccomp no_btrfs no_devmapper no_zfs" cri-cni-release

# DEPLOY_DIR is the directory in the gcs bucket to store the tarball.
DEPLOY_DIR=${DEPLOY_DIR:-""}

BUILDDIR=$(mktemp -d)
cleanup() {
  if [[ ${BUILDDIR} == /tmp/* ]]; then
    echo "[-] REMOVING ${BUILDDIR}"
    rm -rf "${BUILDDIR}"
  fi
}
trap cleanup EXIT

set -x
latest=$(readlink ./releases/cri-cni-containerd.tar.gz)
tarball=$(echo "${latest}" | sed -e 's/cri-containerd-cni/containerd-cni/g' | sed -e 's/-linux-/.linux-/g')
cp "releases/${latest}" "${BUILDDIR}/${tarball}"
cp "releases/${latest}.sha256sum" "${BUILDDIR}/${tarball}.sha256"

# Push test tarball to Google cloud storage.
VERSION=$(git describe --match 'v[0-9]*' --dirty='.m' --always)

if [ -z "${DEPLOY_DIR}" ]; then
  DEPLOY_DIR="containerd"
else
  DEPLOY_DIR="containerd/${DEPLOY_DIR}"
fi

PUSH_VERSION=true DEPLOY_DIR=${DEPLOY_DIR} TARBALL=${tarball} VERSION=${VERSION#v} BUILD_DIR=${BUILDDIR} "${ROOT}/test/push.sh"
