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

ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"/..
cd ${ROOT}

# BUILD_DIR is the directory to generate release tar.
# TARBALL is the name of the release tar.
BUILD_DIR=${BUILD_DIR:-"_output"}
TARBALL=${TARBALL:-"cri-containerd.tar.gz"}
# INCLUDE_CNI indicates whether to install CNI. By default don't
# include CNI in release tarball.
INCLUDE_CNI=${INCLUDE_CNI:-false}

destdir=${BUILD_DIR}/release-stage

# Install dependencies into release stage.
NOSUDO=true INSTALL_CNI=${INCLUDE_CNI} DESTDIR=${destdir} ./hack/install-deps.sh

# Install cri-containerd into release stage.
make install -e DESTDIR=${destdir}

# Copy crictl tool into release stage
cp ${GOPATH}/bin/crictl ${destdir}/usr/local/bin/
# Install systemd units into release stage.
mkdir -p ${destdir}/etc/systemd/system
cp ${ROOT}/contrib/systemd-units/* ${destdir}/etc/systemd/system/

# Create release tar
tar -zcvf ${BUILD_DIR}/${TARBALL} -C ${destdir} .
