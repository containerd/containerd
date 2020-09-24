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

cd $(dirname "${BASH_SOURCE[0]}")

# Install hcsshim
./install-hcsshim.sh

# Install cni
./install-cni.sh

# Install cni config
./install-cni-config.sh

# Install containerd
NOSUDO=true \
  BUILDTAGS="" \
  CONTAINERD_DIR='C:\Program Files\Containerd' \
  ../install-containerd.sh
# Containerd makefile always installs into a "bin" directory.
# Use slash instead of bach slash so that `*` can work.
mv C:/'Program Files'/Containerd/bin/* 'C:\Program Files\Containerd\'
rm -rf 'C:\Program Files\Containerd\bin'

#Install critools
NOSUDO=true \
  CRITOOL_DIR='C:\Program Files\Containerd' \
  CRICTL_RUNTIME_ENDPOINT="npipe:////./pipe/containerd-containerd" \
  CRICTL_CONFIG_DIR="C:\\Users\\$(id -u -n)\\.crictl" \
  ../install-critools.sh
