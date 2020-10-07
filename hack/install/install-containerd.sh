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
CONTAINERD_DIR=${CONTAINERD_DIR:-"${DESTDIR}/usr/local"}
CONTAINERD_PKG=github.com/containerd/containerd

# CHECKOUT_CONTAINERD indicates whether to checkout containerd repo.
# This is useful for build containerd from existing repo, currently
# used by containerd CI test.
CHECKOUT_CONTAINERD=${CHECKOUT_CONTAINERD:-true}

if ${CHECKOUT_CONTAINERD}; then
  # Create a temporary GOPATH for containerd installation.
  export GOPATH=$(mktemp -d /tmp/cri-install-containerd.XXXX)
  from-vendor CONTAINERD github.com/containerd/containerd
  checkout_repo ${CONTAINERD_PKG} ${CONTAINERD_VERSION} ${CONTAINERD_REPO}
fi

# Install containerd
cd ${GOPATH}/src/${CONTAINERD_PKG}
make BUILDTAGS="${BUILDTAGS}"
# containerd make install requires `go` to work. Explicitly
# set PATH to make sure it can find `go` even with `sudo`.
# The single quote is required because containerd Makefile
# can't handle spaces in the path.
${SUDO} make install -e DESTDIR="'${CONTAINERD_DIR}'"

# Clean the tmp GOPATH dir.
if ${CHECKOUT_CONTAINERD}; then
  rm -rf ${GOPATH}
fi
