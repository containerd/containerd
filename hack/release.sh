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
# OFFICIAL_RELEASE indicates whether to use official containerd release.
OFFICIAL_RELEASE=${OFFICIAL_RELEASE:-false}
# LOCAL_RELEASE indicates that containerd has been built and released
# locally.
LOCAL_RELEASE=${LOCAL_RELEASE:-false}
if [ -z "${GOOS:-}" ]
then
    GOOS=$(go env GOOS)
fi
if [ -z "${GOARCH:-}" ]
then
    GOARCH=$(go env GOARCH)
fi


destdir=${BUILD_DIR}/release-stage

if [[ -z "${VERSION}" ]]; then
  echo "VERSION is not set"
  exit 1
fi

# Remove release-stage directory to avoid including old files.
rm -rf ${destdir}

# download_containerd downloads containerd from official release.
download_containerd() {
  local -r tmppath="$(mktemp -d /tmp/download-containerd.XXXX)"
  local -r tarball="${tmppath}/containerd.tar.gz"
  local -r url="https://github.com/containerd/containerd/releases/download/v${VERSION}/containerd-${VERSION}.linux-amd64.tar.gz"
  wget -O "${tarball}" "${url}"
  tar -C "${destdir}/usr/local" -xzf "${tarball}"
  rm -rf "${tmppath}"
}

# copy_local_containerd copies local containerd release.
copy_local_containerd() {
  local -r tarball="${GOPATH}/src/github.com/containerd/containerd/releases/containerd-${VERSION}.${GOOS}-${GOARCH}.tar.gz"
  if [[ ! -e "${tarball}" ]]; then
    echo "Containerd release is not built"
    exit 1
  fi
  tar -C "${destdir}/usr/local" -xzf "${tarball}"
}

# Install dependencies into release stage.
# Install runc
NOSUDO=true DESTDIR=${destdir} ./hack/install/install-runc.sh

if ${INCLUDE_CNI}; then
  # Install cni
  NOSUDO=true DESTDIR=${destdir} ./hack/install/install-cni.sh
fi

# Install critools
NOSUDO=true DESTDIR=${destdir} ./hack/install/install-critools.sh

# Install containerd
if $OFFICIAL_RELEASE; then
  download_containerd
elif $LOCAL_RELEASE; then
  copy_local_containerd
else
  # Build containerd from source
  NOSUDO=true DESTDIR=${destdir} ./hack/install/install-containerd.sh
fi

if ${CUSTOM_CONTAINERD}; then
  make install -e DESTDIR=${destdir}
fi

# Install systemd units into release stage.
mkdir -p ${destdir}/etc/systemd/system
cp ${ROOT}/contrib/systemd-units/* ${destdir}/etc/systemd/system/
# Install cluster directory into release stage.
mkdir -p ${destdir}/opt/containerd
cp -r ${ROOT}/cluster ${destdir}/opt/containerd
# Write a version file into the release tarball.
cat > ${destdir}/opt/containerd/cluster/version <<EOF
CONTAINERD_VERSION: $(yaml-quote ${VERSION})
EOF

# Create release tar
tarball=${BUILD_DIR}/${TARBALL}
tar -zcvf ${tarball} -C ${destdir} . --owner=0 --group=0
checksum=$(sha256 ${tarball})
echo "sha256sum: ${checksum} ${tarball}"
echo ${checksum} > ${tarball}.sha256
