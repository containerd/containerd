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
CNI_DIR=${DESTDIR}/opt/cni
CNI_CONFIG_DIR=${DESTDIR}/etc/cni/net.d
CNI_PKG=github.com/containernetworking/plugins

# Create a temporary GOPATH for cni installation.
TMPGOPATH=$(mktemp -d /tmp/cri-install-cni.XXXX)
GOPATH=${TMPGOPATH}

# Install cni
from-vendor CNI github.com/containernetworking/plugins
checkout_repo ${CNI_PKG} ${CNI_VERSION} ${CNI_REPO}
cd ${GOPATH}/src/${CNI_PKG}
FASTBUILD=true ./build.sh
${SUDO} mkdir -p ${CNI_DIR}
${SUDO} cp -r ./bin ${CNI_DIR}
${SUDO} mkdir -p ${CNI_CONFIG_DIR}
${SUDO} bash -c 'cat >'${CNI_CONFIG_DIR}'/10-containerd-net.conflist <<EOF
{
  "cniVersion": "0.3.1",
  "name": "containerd-net",
  "plugins": [
    {
      "type": "bridge",
      "bridge": "cni0",
      "isGateway": true,
      "ipMasq": true,
      "promiscMode": true,
      "ipam": {
        "type": "host-local",
        "subnet": "10.88.0.0/16",
        "routes": [
          { "dst": "0.0.0.0/0" }
        ]
      }
    },
    {
      "type": "portmap",
      "capabilities": {"portMappings": true}
    }
  ]
}
EOF'

# Clean the tmp GOPATH dir.
rm -rf ${TMPGOPATH}
