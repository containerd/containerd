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

source "$(dirname "${BASH_SOURCE[0]}")/utils.sh"

# DEPLOY_BUCKET is the gcs bucket where the tarball should be stored in.
DEPLOY_BUCKET=${DEPLOY_BUCKET:-"k8s-staging-cri-tools"}
# DEPLOY_DIR is the directory in the gcs bucket to store the tarball.
DEPLOY_DIR=${DEPLOY_DIR:-""}
# BUILD_DIR is the directory of the build out.
BUILD_DIR=${BUILD_DIR:-"_output"}
# TARBALL is the tarball name.
TARBALL=${TARBALL:-"cri-containerd.tar.gz"}
# LATEST is the name of the latest version file.
LATEST=${LATEST:-"latest"}
# PUSH_VERSION indicates whether to push version.
PUSH_VERSION=${PUSH_VERSION:-false}

release_tar=${BUILD_DIR}/${TARBALL}
release_tar_checksum=${release_tar}.sha256
if [[ ! -e ${release_tar} || ! -e ${release_tar_checksum} ]]; then
  echo "Release tarball is not built"
  exit 1
fi

if ! gsutil ls "gs://${DEPLOY_BUCKET}" > /dev/null; then
  create_ttl_bucket "${DEPLOY_BUCKET}"
fi

if [ -z "${DEPLOY_DIR}" ]; then
  DEPLOY_PATH="${DEPLOY_BUCKET}"
else
  DEPLOY_PATH="${DEPLOY_BUCKET}/${DEPLOY_DIR}"
fi

gsutil cp "${release_tar}" "gs://${DEPLOY_PATH}/"
gsutil cp "${release_tar_checksum}" "gs://${DEPLOY_PATH}/"
echo "Release tarball is uploaded to:
  https://storage.googleapis.com/${DEPLOY_PATH}/${TARBALL}"

if ${PUSH_VERSION}; then
  if [[ -z "${VERSION}" ]]; then
    echo "VERSION is not set"
    exit 1
  fi
  echo "${VERSION}" | gsutil cp - "gs://${DEPLOY_PATH}/${LATEST}"
  echo "Latest version is uploaded to:
  https://storage.googleapis.com/${DEPLOY_PATH}/${LATEST}"
fi
