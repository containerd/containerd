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
HCSSHIM_DIR="${HCSSHIM_DIR:-"C:\\Program Files\\Containerd"}"
HCSSHIM_PKG=github.com/Microsoft/hcsshim

# Create a temporary GOPATH for hcsshim installation.
GOPATH="$(mktemp -d /tmp/cri-install-hcsshim.XXXX)"

# Install hcsshim
from-vendor HCSSHIM "${HCSSHIM_PKG}"
checkout_repo "${HCSSHIM_PKG}" "${HCSSHIM_VERSION}" "${HCSSHIM_REPO}"
cd "${GOPATH}/src/${HCSSHIM_PKG}"
go build "${HCSSHIM_PKG}/cmd/containerd-shim-runhcs-v1"
install -D -m 755 containerd-shim-runhcs-v1.exe "${HCSSHIM_DIR}"/containerd-shim-runhcs-v1.exe

# Clean the tmp GOPATH dir. Use sudo because runc build generates
# some privileged files.
rm -rf ${GOPATH}
