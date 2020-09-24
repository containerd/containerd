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

# Dependencies:
# runc:
# - libseccomp-dev(Ubuntu,Debian)/libseccomp-devel(Fedora, CentOS, RHEL). Note that
# libseccomp in ubuntu <=trusty and debian <=jessie is not new enough, backport
# is required.
# containerd:
# - btrfs-tools(Ubuntu,Debian)/btrfs-progs-devel(Fedora, CentOS, RHEL)

set -o errexit
set -o nounset
set -o pipefail

cd $(dirname "${BASH_SOURCE[0]}")

# Install runc
./install-runc.sh

# Install cni
./install-cni.sh

# Install cni config
./install-cni-config.sh

# Install containerd
./install-containerd.sh

#Install critools
./install-critools.sh
