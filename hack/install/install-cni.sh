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
CNI_DIR=${DESTDIR}/opt/cni
CNI_PKG=github.com/containernetworking/plugins

# Create a temporary GOPATH for cni installation.
GOPATH=$(mktemp -d /tmp/cri-install-cni.XXXX)

# Install cni
from-vendor CNI github.com/containernetworking/plugins
checkout_repo ${CNI_PKG} ${CNI_VERSION} ${CNI_REPO}
cd ${GOPATH}/src/${CNI_PKG}

if [[ "$OSTYPE" == "linux-gnu"* ]] || [[ "$OSTYPE" == "darwin"* ]]; then
   ./build_linux.sh
elif [[ "$OSTYPE" == "win32" ]]; then
   ./build_windows.sh
else
   exit 1
fi

${SUDO} mkdir -p ${CNI_DIR}
${SUDO} cp -r ./bin ${CNI_DIR}

# Clean the tmp GOPATH dir.
rm -rf ${GOPATH}
