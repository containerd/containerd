#!/usr/bin/env bash

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

#
# Builds and installs cni plugins to /opt/cni/bin,
# and create basic cni config in /etc/cni/net.d.
# The commit defined in go.mod
#
set -eu -o pipefail

CNI_COMMIT=${1:-$(go list -f "{{.Version}}" -m github.com/containernetworking/plugins)}
CNI_DIR=${DESTDIR:=''}/opt/cni
CNI_CONFIG_DIR=${DESTDIR}/etc/cni/net.d

# e2e and Cirrus will fail with "sudo: command not found"
SUDO=''
if (( $EUID != 0 )); then
    SUDO='sudo'
fi

TMPROOT=$(mktemp -d)
git clone https://github.com/containernetworking/plugins.git "${TMPROOT}"/plugins
pushd "${TMPROOT}"/plugins
git checkout "$CNI_COMMIT"
./build_linux.sh
$SUDO mkdir -p $CNI_DIR
$SUDO cp -r ./bin $CNI_DIR
$SUDO mkdir -p $CNI_CONFIG_DIR
$SUDO cat << EOF | $SUDO tee $CNI_CONFIG_DIR/10-containerd-net.conflist
{
  "cniVersion": "1.0.0",
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
        "ranges": [
          [{
            "subnet": "10.88.0.0/16"
          }],
          [{
            "subnet": "2001:4860:4860::/64"
          }]
        ],
        "routes": [
          { "dst": "0.0.0.0/0" },
          { "dst": "::/0" }
        ]
      }
    },
    {
      "type": "portmap",
      "capabilities": {"portMappings": true}
    }
  ]
}
EOF

popd
rm -fR "${TMPROOT}"
