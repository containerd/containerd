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

source $(dirname "${BASH_SOURCE[0]}")/../utils.sh

# DESTDIR is the absolute dest path to install dependencies.
DESTDIR=${DESTDIR:-"/"}
# Make sure that DESTDIR is an absolute path.
if [[ ${DESTDIR} != /* ]]; then
  echo "DESTDIR is not an absolute path"
  exit 1
fi

# NOSUDO indicates not to use sudo during installation.
NOSUDO=${NOSUDO:-false}
SUDO="sudo"
if ${NOSUDO}; then
  SUDO=""
fi

# BUILDTAGS are bulid tags for runc and containerd.
BUILDTAGS=${BUILDTAGS:-seccomp apparmor selinux}

# checkout_repo checks out specified repository
# and switch to specified  version.
# Varset:
# 1) Pkg name;
# 2) Version;
# 3) Repo name;
checkout_repo() {
  local -r pkg=$1
  local -r version=$2
  local -r repo=$3
  path="${GOPATH}/src/${pkg}"
  if [ ! -d ${path} ]; then
    mkdir -p ${path}
    git clone https://${repo} ${path}
  fi
  cd ${path}
  git fetch --all
  git checkout ${version}
}
