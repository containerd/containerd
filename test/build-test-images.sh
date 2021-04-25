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

# This script is used to build and upload images in integration/images
# directory to gcr.io/k8s-cri-containerd repository

set -o xtrace
set -o errexit
set -o nounset
set -o pipefail

source $(dirname "${BASH_SOURCE[0]}")/build-utils.sh
source $(dirname "${BASH_SOURCE[0]}")/init-buildx.sh
cd "${ROOT}"

# ignore errors if the image already exists
make -C integration/images/volume-copy-up push PROJ="gcr.io/${PROJECT:-k8s-cri-containerd}" || true
make -C integration/images/volume-ownership push PROJ="gcr.io/${PROJECT:-k8s-cri-containerd}" || true
