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

source $(dirname "${BASH_SOURCE[0]}")/../utils.sh
# WINCNI_BIN_DIR is the cni plugin directory
WINCNI_BIN_DIR="${WINCNI_BIN_DIR:-"C:\\Program Files\\containerd\\cni\\bin"}"
WINCNI_PKG=github.com/Microsoft/windows-container-networking
WINCNI_VERSION=aa10a0b31e9f72937063436454def1760b858ee2

# Create a temporary GOPATH for cni installation.
GOPATH="$(mktemp -d /tmp/cri-install-cni.XXXX)"

# Install cni
checkout_repo "${WINCNI_PKG}" "${WINCNI_VERSION}" "${WINCNI_PKG}"
cd "${GOPATH}/src/${WINCNI_PKG}"
make all
install -D -m 755 "out/nat.exe" "${WINCNI_BIN_DIR}/nat.exe"
install -D -m 755 "out/sdnbridge.exe" "${WINCNI_BIN_DIR}/sdnbridge.exe"
install -D -m 755 "out/sdnoverlay.exe" "${WINCNI_BIN_DIR}/sdnoverlay.exe"

# Clean the tmp GOPATH dir.
rm -rf "${GOPATH}"
