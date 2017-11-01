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

set -o xtrace
set -o errexit
set -o nounset
set -o pipefail

# CRI_CONTAINERD_HOME is the directory for cri-containerd.
CRI_CONTAINERD_HOME="/home/cri-containerd"
cd "${CRI_CONTAINERD_HOME}"

# ATTRIBUTES is the url of gce metadata attributes.
ATTRIBUTES="http://metadata.google.internal/computeMetadata/v1/instance/attributes"

# DEPLOY_PATH is the gcs path where cri-containerd tarball is stored.
DEPLOY_PATH=${DEPLOY_PATH:-"cri-containerd-staging"}
# PULL_REFS_METADATA is the metadata key of PULL_REFS from prow.
PULL_REFS_METADATA="PULL_REFS"
if curl --fail --retry 5 --retry-delay 3 --silent --show-error -H "X-Google-Metadata-Request: True" "${ATTRIBUTES}/" | \
  grep -q "${PULL_REFS_METADATA}"; then
  PULL_REFS=$(curl --fail --retry 5 --retry-delay 3 --silent --show-error -H "X-Google-Metadata-Request: True" \
    "${ATTRIBUTES}/${PULL_REFS_METADATA}")
  DEPLOY_DIR=$(echo "${PULL_REFS}" | sha1sum | awk '{print $1}')
  DEPLOY_PATH="${DEPLOY_PATH}/${DEPLOY_DIR}"
fi

# VERSION is the latest cri-containerd version got from cri-containerd gcs
# bucket.
VERSION=$(curl -f --ipv4 --retry 6 --retry-delay 3 --silent --show-error \
  https://storage.googleapis.com/${DEPLOY_PATH}/latest)
# TARBALL_GCS_PATH is the path to download cri-containerd tarball for node e2e.
TARBALL_GCS_PATH="https://storage.googleapis.com/${DEPLOY_PATH}/cri-containerd-cni-${VERSION}.tar.gz"
# TARBALL is the name of the tarball after being downloaded.
TARBALL="cri-containerd.tar.gz"

# Download and untar the release tar ball.
curl -f --ipv4 -Lo "${TARBALL}" --connect-timeout 20 --max-time 300 --retry 6 --retry-delay 10 "${TARBALL_GCS_PATH}"
tar xvf "${TARBALL}"

# EXTRA_INIT_SCRIPT is the name of the extra init script after being downloaded.
EXTRA_INIT_SCRIPT="extra-init.sh"
# EXTRA_INIT_SCRIPTINIT_SCRIPT_METADATA is the metadata key of init script.
EXTRA_INIT_SCRIPT_METADATA="extra-init-sh"

# Check whether extra-init-sh is set.
if ! curl --fail --retry 5 --retry-delay 3 --silent --show-error -H "X-Google-Metadata-Request: True" "${ATTRIBUTES}/" | \
  grep -q "${EXTRA_INIT_SCRIPT_METADATA}"; then
  exit 0
fi

# Run extra-init.sh if extra-init-sh is set.
curl --fail --retry 5 --retry-delay 3 --silent --show-error -H "X-Google-Metadata-Request: True" -o "${EXTRA_INIT_SCRIPT}" \
  "${ATTRIBUTES}/${EXTRA_INIT_SCRIPT_METADATA}"
chmod 544 "${EXTRA_INIT_SCRIPT}"
./${EXTRA_INIT_SCRIPT}
