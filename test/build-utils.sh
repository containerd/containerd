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

ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"/..

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
apt-get update
if apt-cache show libbtrfs-dev > /dev/null; then
  apt-get install -y libbtrfs-dev
else
  apt-get install -y btrfs-tools
fi

# Kubernetes test infra uses jessie and stretch.
if cat /etc/os-release | grep jessie; then
  sh -c "echo 'deb http://ftp.debian.org/debian jessie-backports main' > /etc/apt/sources.list.d/backports.list"
  apt-get update
  apt-get install -y libseccomp2/jessie-backports
  apt-get install -y libseccomp-dev/jessie-backports
else
  apt-get install -y libseccomp2
  apt-get install -y libseccomp-dev
fi

# PULL_REFS is from prow.
if [ ! -z "${PULL_REFS:-""}" ]; then
  DEPLOY_DIR=$(echo "${PULL_REFS}" | sha1sum | awk '{print $1}')
fi
