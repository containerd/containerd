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

source $(dirname "${BASH_SOURCE[0]}")/utils.sh
cd ${ROOT}

umask 0022

# BUILD_DIR is the directory to generate release tar.
# TARBALL is the name of the release tar.
BUILD_DIR=${BUILD_DIR:-"_output"}
# Convert to absolute path if it's relative.
if [[ ${BUILD_DIR} != /* ]]; then
  BUILD_DIR=${ROOT}/${BUILD_DIR}
fi
TARBALL=${TARBALL:-"cri-containerd.tar.gz"}
# INCLUDE_CNI indicates whether to install CNI. By default don't
# include CNI in release tarball.
INCLUDE_CNI=${INCLUDE_CNI:-false}
# CUSTOM_CONTAINERD indicates whether to install customized containerd
# for CI test.
CUSTOM_CONTAINERD=${CUSTOM_CONTAINERD:-false}

destdir=${BUILD_DIR}/release-stage

if [[ -z "${VERSION}" ]]; then
  echo "VERSION is not set"
  exit 1
fi

# Remove release-stage directory to avoid including old files.
rm -rf ${destdir}

# Install dependencies into release stage.
# Install hcsshim
HCSSHIM_DIR=${destdir} ./hack/install/windows/install-hcsshim.sh

if ${INCLUDE_CNI}; then
  # Install cni
  NOSUDO=true WINCNI_BIN_DIR=${destdir}/cni ./hack/install/windows/install-cni.sh
fi

# Build containerd from source
NOSUDO=true CONTAINERD_DIR=${destdir} ./hack/install/install-containerd.sh
# Containerd makefile always installs into a "bin" directory.
mv "${destdir}"/bin/* "${destdir}"
rm -rf "${destdir}/bin"

if ${CUSTOM_CONTAINERD}; then
  make install -e BINDIR=${destdir}
fi

# Create release tar
tarball=${BUILD_DIR}/${TARBALL}
tar -zcvf ${tarball} -C ${destdir} . --owner=0 --group=0
checksum=$(sha256 ${tarball})
echo "sha256sum: ${checksum} ${tarball}"
echo ${checksum} > ${tarball}.sha256
