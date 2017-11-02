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

# This script is used to build and upload cri-containerd in gcr.io/k8s-testimages/kubekins-e2e.

set -o xtrace
set -o errexit
set -o nounset
set -o pipefail

ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"/..
cd "${ROOT}"

# PROJECT is the gce project to upload tarball.
PROJECT=${PROJECT:-"k8s-cri-containerd"}

# GOOGLE_APPLICATION_CREDENTIALS is the path of service account file.
if [ -z ${GOOGLE_APPLICATION_CREDENTIALS} ]; then
  echo "GOOGLE_APPLICATION_CREDENTIALS is not set"
  exit 1
fi

# Activate gcloud service account.
gcloud auth activate-service-account --key-file "${GOOGLE_APPLICATION_CREDENTIALS}" --project="${PROJECT}"

# Install dependent libraries.
sh -c "echo 'deb http://ftp.debian.org/debian jessie-backports main' > /etc/apt/sources.list.d/backports.list"
apt-get update
apt-get install -y btrfs-tools
apt-get install -y libseccomp2/jessie-backports
apt-get install -y libseccomp-dev/jessie-backports
apt-get install -y libapparmor-dev

# PULL_REFS is from prow.
if [ ! -z "${PULL_REFS:-""}" ]; then
  DEPLOY_DIR=$(echo "${PULL_REFS}" | sha1sum | awk '{print $1}')
fi

# Make sure output directory is clean.
make clean
# Build and push e2e tarball.
DEPLOY_DIR=${DEPLOY_DIR:-""} make push
# Build and push node e2e tarball.
PUSH_VERSION=true DEPLOY_DIR=${DEPLOY_DIR:-""} \
  make push TARBALL_PREFIX=cri-containerd-cni INCLUDE_CNI=true
