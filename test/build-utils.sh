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

# PROJECT is the gce project to upload tarball.
: "${PROJECT:=k8s-cri-containerd}"

# GOOGLE_APPLICATION_CREDENTIALS is the path of service account file.
if [ -n "${GOOGLE_APPLICATION_CREDENTIALS:-}" ]; then
  gcloud auth activate-service-account --key-file "${GOOGLE_APPLICATION_CREDENTIALS}" --project="${PROJECT}"
fi

cat /etc/os-release
apt-get update
apt-get install -y libseccomp2 libseccomp-dev

# PULL_REFS is from prow.
if [ -n "${PULL_REFS:-""}" ]; then
  DEPLOY_DIR=$(echo "${PULL_REFS}" | sha1sum | awk '{print $1}')
fi
