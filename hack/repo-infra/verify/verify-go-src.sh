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

set -o errexit
set -o nounset
set -o pipefail

# This script is intended to be used via subtree in a top-level directory:
# <repo>/
#  repo-infra/
#    verify/
# Or via vendoring and passing root directory as vendor/repo-infra/verify-*.sh --rootdir **full path to your repo dir**
# <repo>/
#   vendor/
#      repo-infra/
#         ...
# 


SILENT=true
REPO_ROOT=$(dirname "${BASH_SOURCE}")/../..

# Convert long opts to short ones to read through getopts
for arg in "$@"; do
  shift
  case "$arg" in
    "--rootdir") set -- "$@" "-r";;
    *)
      set -- "$@" "$arg"
      ;;
  esac
done

OPTIND=1
while getopts "vr:" opt; do
  case ${opt} in
    v)
      SILENT=false
      ;;
    r)
      REPO_ROOT=${OPTARG}
      ;;
    \?)
      echo "Invalid flag: -${OPTARG}" >&2
      exit 1
      ;;
  esac
done

shift "$(($OPTIND-1))"

echo "Working directory: ${REPO_ROOT}"

GO_TOOLS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/go-tools"

function run-cmd {
  if ${SILENT}; then
    "$@" &> /dev/null
  else
    "$@"
  fi
}

# Some useful colors.
if [[ -z "${color_start-}" ]]; then
  declare -r color_start="\033["
  declare -r color_red="${color_start}0;31m"
  declare -r color_yellow="${color_start}0;33m"
  declare -r color_green="${color_start}0;32m"
  declare -r color_norm="${color_start}0m"
fi

function run-checks {
  local -r pattern=$1
  local -r runner=$2

  for t in $(ls ${pattern})
  do
    echo -e "Verifying ${t}"
    local start=$(date +%s)
    cd $REPO_ROOT && run-cmd "${runner}" "${t}" && tr=$? || tr=$?
    local elapsed=$(($(date +%s) - ${start}))
    if [[ ${tr} -eq 0 ]]; then
      echo -e "${color_green}SUCCESS${color_norm}  ${t}\t${elapsed}s"
    else
      echo -e "${color_red}FAILED${color_norm}   ${t}\t${elapsed}s"
      ret=1
    fi
  done
}

if ${SILENT} ; then
  echo "Running in the silent mode, run with -v if you want to see script logs."
fi

ret=0
run-checks "${GO_TOOLS_DIR}/*.sh" bash
exit ${ret}
