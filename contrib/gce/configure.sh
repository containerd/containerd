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
DEPLOY_PATH=${DEPLOY_PATH:-"cri-containerd-release"}

# PKG_PREFIX is the prefix of the cri-containerd tarball name.
# By default use the release tarball with cni built in.
PKG_PREFIX=${PKG_PREFIX:-"cri-containerd-cni"}

# VERSION is the cri-containerd version to use. If not specified,
# the latest version will be used.
VERSION_METADATA="version"
VERSION=$(fetch_metadata "${VERSION_METADATA}")
if [ -z "${VERSION}" ]; then
  VERSION=$(curl -f --ipv4 --retry 6 --retry-delay 3 --silent --show-error \
    https://storage.googleapis.com/${DEPLOY_PATH}/latest)
fi

# TARBALL_GCS_PATH is the path to download cri-containerd tarball for node e2e.
TARBALL_GCS_PATH="https://storage.googleapis.com/${DEPLOY_PATH}/${PKG_PREFIX}-${VERSION}.linux-amd64.tar.gz"
# TARBALL is the name of the tarball after being downloaded.
TARBALL="cri-containerd.tar.gz"

# Download and untar the release tar ball.
curl -f --ipv4 -Lo "${TARBALL}" --connect-timeout 20 --max-time 300 --retry 6 --retry-delay 10 "${TARBALL_GCS_PATH}"
tar xvf "${TARBALL}"

# Copy crictl config.
cp "${CRI_CONTAINERD_HOME}/etc/crictl.yaml" /etc

echo "export PATH=${CRI_CONTAINERD_HOME}/usr/local/bin/:${CRI_CONTAINERD_HOME}/usr/local/sbin/:\$PATH" > \
  /etc/profile.d/cri-containerd_env.sh
