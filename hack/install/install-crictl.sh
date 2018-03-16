#!/bin/bash

# Copyright 2018 The containerd Authors.
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
CRICTL_DIR=${DESTDIR}/usr/local/bin
CRICTL_CONFIG_DIR=${DESTDIR}/etc

# Create a temporary GOPATH for crictl installation.
TMPGOPATH=$(mktemp -d /tmp/cri-install-crictl.XXXX)
GOPATH=${TMPGOPATH}

#Install crictl
checkout_repo ${CRITOOL_PKG} ${CRITOOL_VERSION} ${CRITOOL_REPO}
cd ${GOPATH}/src/${CRITOOL_PKG}
make crictl
${SUDO} make install-crictl -e BINDIR=${CRICTL_DIR} GOPATH=${GOPATH}
${SUDO} mkdir -p ${CRICTL_CONFIG_DIR}
${SUDO} bash -c 'cat >'${CRICTL_CONFIG_DIR}'/crictl.yaml <<EOF
runtime-endpoint: /run/containerd/containerd.sock
EOF'

# Clean the tmp GOPATH dir.
rm -rf ${TMPGOPATH}
