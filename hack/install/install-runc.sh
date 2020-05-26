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
RUNC_DIR=${DESTDIR}
RUNC_PKG=github.com/opencontainers/runc

# Create a temporary GOPATH for runc installation.
GOPATH=$(mktemp -d /tmp/cri-install-runc.XXXX)

# Install runc
from-vendor RUNC github.com/opencontainers/runc
checkout_repo ${RUNC_PKG} ${RUNC_VERSION} ${RUNC_REPO}
cd ${GOPATH}/src/${RUNC_PKG}
make BUILDTAGS="$BUILDTAGS" VERSION=${RUNC_VERSION}
${SUDO} make install -e DESTDIR=${RUNC_DIR}

# Clean the tmp GOPATH dir. Use sudo because runc build generates
# some privileged files.
${SUDO} rm -rf ${GOPATH}
