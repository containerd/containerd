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

# fetch_metadata fetches metadata from GCE metadata server.
# Var set:
# 1. Metadata key: key of the metadata.
fetch_metadata() {
  local -r key=$1
  local -r attributes="http://metadata.google.internal/computeMetadata/v1/instance/attributes"
  if curl --fail --retry 5 --retry-delay 3 --silent --show-error -H "X-Google-Metadata-Request: True" "${attributes}/" | \
    grep -q "${key}"; then
    curl --fail --retry 5 --retry-delay 3 --silent --show-error -H "X-Google-Metadata-Request: True" \
      "${attributes}/${key}"
  fi
}

# DEPLOY_PATH is the gcs path where cri-containerd tarball is stored.
DEPLOY_PATH=${DEPLOY_PATH:-"cri-containerd-staging"}
# PULL_REFS_METADATA is the metadata key of PULL_REFS from prow.
PULL_REFS_METADATA="PULL_REFS"
pull_refs=$(fetch_metadata "${PULL_REFS_METADATA}")
if [ ! -z "${pull_refs}" ]; then
  deploy_dir=$(echo "${pull_refs}" | sha1sum | awk '{print $1}') 
  DEPLOY_PATH="${DEPLOY_PATH}/${deploy_dir}"
fi

# PKG_PREFIX is the prefix of the cri-containerd tarball name.
# By default use the release tarball with cni built in.
PKG_PREFIX=${PKG_PREFIX:-"cri-containerd-cni"}
# PKG_PREFIX_METADATA is the metadata key of PKG_PREFIX.
PKG_PREFIX_METADATA="pkg_prefix"
pkg_prefix=$(fetch_metadata "${PKG_PREFIX_METADATA}")
if [ ! -z "${pkg_prefix}" ]; then
  PKG_PREFIX=${pkg_prefix}
fi

# VERSION is the latest cri-containerd version got from cri-containerd gcs
# bucket.
VERSION=$(curl -f --ipv4 --retry 6 --retry-delay 3 --silent --show-error \
  https://storage.googleapis.com/${DEPLOY_PATH}/latest)
# TARBALL_GCS_PATH is the path to download cri-containerd tarball for node e2e.
TARBALL_GCS_PATH="https://storage.googleapis.com/${DEPLOY_PATH}/${PKG_PREFIX}-${VERSION}.tar.gz"
# TARBALL is the name of the tarball after being downloaded.
TARBALL="cri-containerd.tar.gz"

# Download and untar the release tar ball.
curl -f --ipv4 -Lo "${TARBALL}" --connect-timeout 20 --max-time 300 --retry 6 --retry-delay 10 "${TARBALL_GCS_PATH}"
tar xvf "${TARBALL}"

# Copy crictl config.
cp "${CRI_CONTAINERD_HOME}/etc/crictl.yaml" /etc

# TODO(random-liu): Stop docker on the node, this may break docker.
echo "export PATH=${CRI_CONTAINERD_HOME}/usr/local/bin/:${CRI_CONTAINERD_HOME}/usr/local/sbin/:\$PATH" > \
  /etc/profile.d/cri-containerd_env.sh

# EXTRA_INIT_SCRIPT is the name of the extra init script after being downloaded.
EXTRA_INIT_SCRIPT="extra-init.sh"
# EXTRA_INIT_SCRIPT_METADATA is the metadata key of init script.
EXTRA_INIT_SCRIPT_METADATA="extra-init-sh"
extra_init=$(fetch_metadata "${EXTRA_INIT_SCRIPT_METADATA}")
# Return if extra-init-sh is not set.
if [ -z "${extra_init}" ]; then
  exit 0
fi
echo "${extra_init}" > "${EXTRA_INIT_SCRIPT}"
chmod 544 "${EXTRA_INIT_SCRIPT}"
./${EXTRA_INIT_SCRIPT}
