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

# Dependencies:
# runc:
# - libseccomp-dev(Ubuntu,Debian)/libseccomp-devel(Fedora, CentOS, RHEL). Note that
# libseccomp in ubuntu <=trusty and debian <=jessie is not new enough, backport
# is required.
# - libapparmor-dev(Ubuntu,Debian)/libapparmor-devel(Fedora, CentOS, RHEL)
# containerd:
# - btrfs-tools(Ubuntu,Debian)/btrfs-progs-devel(Fedora, CentOS, RHEL)

set -o errexit
set -o nounset
set -o pipefail

ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"/..
. ${ROOT}/hack/versions

RUNC_PKG=github.com/opencontainers/runc
CNI_PKG=github.com/containernetworking/plugins
CNI_DIR=/opt/cni
CNI_CONFIG_DIR=/etc/cni/net.d
CONTAINERD_PKG=github.com/containerd/containerd

# Check GOPATH
if [[ -z "${GOPATH}" ]]; then
  echo "GOPATH is not set"
  exit 1
fi

# For multiple GOPATHs, keep the first one only
GOPATH=${GOPATH%%:*}

# Install runc
go get -d ${RUNC_PKG}/...
cd ${GOPATH}/src/${RUNC_PKG}
git fetch --all
git checkout ${RUNC_VERSION}
BUILDTAGS=${BUILDTAGS:-seccomp apparmor}
make BUILDTAGS="$BUILDTAGS"
sudo make install
which runc

# Install cni
go get -d ${CNI_PKG}/...
cd ${GOPATH}/src/${CNI_PKG}
git fetch --all
git checkout ${CNI_VERSION}
./build.sh
sudo mkdir -p ${CNI_DIR}
sudo cp -r ./bin ${CNI_DIR}
sudo mkdir -p ${CNI_CONFIG_DIR}
sudo bash -c 'cat >'${CNI_CONFIG_DIR}'/10-containerd-net.conflist <<EOF
{
    "cniVersion": "0.3.1",
    "name": "containerd-net",
    "plugins": [
	{
	    "type": "bridge",
	    "bridge": "cni0",
	    "isGateway": true,
	    "ipMasq": true,
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

# Install containerd
go get -d ${CONTAINERD_PKG}/...
cd ${GOPATH}/src/${CONTAINERD_PKG}
git fetch --all
git checkout ${CONTAINERD_VERSION}
make
sudo make install
which containerd
which containerd-shim
